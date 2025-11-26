/* SPDX-License-Identifier: GPL-3.0-or-later
 *
 * QAnotherRTSP
 * Copyright (C) 2025 e1z0 <e1z0@icloud.com>
 *
 * This file is part of QAnotherRTSP.
 *
 * QAnotherRTSP is free software: you can redistribute it and/or modify
 * it under the terms of the GNU General Public License as published by
 * the Free Software Foundation, either version 3 of the License, or
 * (at your option) any later version.
 *
 * QAnotherRTSP is distributed in the hope that it will be useful,
 * but WITHOUT ANY WARRANTY; without even the implied warranty of
 * MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
 * GNU General Public License for more details.
 *
 * You should have received a copy of the GNU General Public License
 * along with QAnotherRTSP.  If not, see <https://www.gnu.org/licenses/>.
 */
package main

import (
	"fmt"
	"log"
	"sync"
	"sync/atomic"
	"time"

	astiav "github.com/asticode/go-astiav"
	"github.com/mappu/miqt/qt"
)

// CamWindow represents one camera window.
type CamWindow struct {
	cfg  CameraConfig
	win  *qt.QMainWindow
	view *VideoWidget

	stop chan struct{}
	done chan struct{}

	buf frameBuf

	wantPlaying bool
	closing     bool

	// supervisor state
	lastAdvance      time.Time     // last time we saw progress
	backoff          time.Duration // starts at 1s, doubles to 30s max
	nextTry          time.Time     // when to attempt next reconnect
	saveTimer        *qt.QTimer
	idKey            string // stable key to find this camera in config (prefer ID, else Name)
	idx              int
	onClosed         func(idx int)
	suppressOnClosed bool // one-shot: do not call onClosed on next close
	isFullscreen     bool
	// geometry to restore when leaving fullscreen
	prevX, prevY int
	prevW, prevH int
	// Debounce saver skip persistence
	suppressSave  bool
	tmpBGRA       []byte
	tmpStride     int
	repaintTimer  *qt.QTimer
	contextHooked bool
	// some statistics metrics / overlay
	framesDecoded int64 // total decoded frames
	bytesVideo    int64 // total video bytes seen (via pkt.Size())
	framesDropped int64 // best-effort estimate (decode errors etc.)
	decodeErrs    int64
	fps           float64
	bitrateKbps   float64
	dropsPct      float64
	health        int32 // 0..5
	lastMAt       time.Time
	lastMFrames   int64
	lastMBytes    int64
	metricsTimer  *qt.QTimer
	lastMDrops    int64
	// timing for PTS-based gap estimator
	tbNum, tbDen   int   // stream timebase (vst.TimeBase)
	fpsNom, fpsDen int   // stream fps rational (AvgFrameRate or vctx.Framerate)
	lastPktPTS     int64 // last seen *packet* PTS (or DTS fallback)
	pktPtsInited   bool
	// --- CPU load estimator (per camera) ---
	busyNS      int64   // total busy nanoseconds accumulated
	lastMBusyNS int64   // snapshot for delta-per-second
	cpuPct      float64 // percent of one core, last interval
	// recording
	recording atomic.Bool
	recStop   chan struct{}
	recDone   chan struct{}
	recPath   string

	// per-camera recorder FFmpeg state (used in video.go)
	recCtx      *astiav.FormatContext
	recIO       *astiav.IOContext
	recStreamIx map[int]int // map[inputStreamIndex]outputStreamIndex

	aEncCtx    *astiav.CodecContext
	aEncStream *astiav.Stream
	aSwr       *astiav.SoftwareResampleContext
	aEncFrame  *astiav.Frame

	recMu sync.Mutex
}

func (w *CamWindow) SetOnClosed(fn func(int)) { w.onClosed = fn }

// Called by tray before Close()
func (w *CamWindow) SuppressOnClosedOnce() { w.suppressOnClosed = true }

// newCamWindow creates the Qt window + videowidget and starts the decoder loop.
func newCamWindow(cfg CameraConfig, idx int) (*CamWindow, error) {
	w := &CamWindow{
		cfg:         cfg,
		stop:        make(chan struct{}),
		done:        make(chan struct{}),
		wantPlaying: true,
		backoff:     time.Second,
		idx:         idx,
	}

	w.idKey = cfg.ID
	if w.idKey == "" {
		w.idKey = genID()
	}

	title := cfg.Name
	if title == "" {
		title = cfg.URL
	}

	log.Printf("Opening camera: %s", title)

	win := qt.NewQMainWindow(nil)

	// Toggle frameless flag
	win.SetWindowFlag2(qt.FramelessWindowHint, globalConfig.NoWindowsTitles)
	// If titles are visible again, make sure the title is set
	if !globalConfig.NoWindowsTitles {
		title := safeCamTitle(w.cfg)
		win.SetWindowTitle(fmt.Sprintf("Cam: %s", title))
	}

	effectiveTop := globalConfig.AlwaysOnTopAll || cfg.AlwaysOnTop
	win.SetWindowFlag2(qt.WindowStaysOnTopHint, effectiveTop)

	var width, height int
	if cfg.Width > 0 {
		width = cfg.Width
	} else {
		width = 640
	}
	if cfg.Height > 0 {
		height = cfg.Height
	} else {
		height = 480
	}

	win.Resize(width, height)
	if cfg.X > 0 && cfg.Y > 0 {
		win.Move(cfg.X, cfg.Y)
	} else {
		win.Move(0, 0)
	}
	if cfg.AlwaysOnTop {
		win.SetWindowFlag2(qt.WindowStaysOnTopHint, true)
	}

	win.OnCloseEvent(func(super func(event *qt.QCloseEvent), event *qt.QCloseEvent) {
		super(event)
		if w.closing {
			return
		}
		w.closing = true
		w.Close()

		// Only notify if not suppressed
		if w.onClosed != nil && !w.suppressOnClosed {
			cb := w.onClosed
			i := w.idx
			// Post it to run after the event returns (prevents re-entrancy)
			postToUI(*w.win.QObject, func() { cb(i) })
		}
		// consume the one-shot flag
		w.suppressOnClosed = false
	})

	// Debounced saver
	w.saveTimer = qt.NewQTimer()
	w.saveTimer.SetSingleShot(true)
	w.saveTimer.SetInterval(600) // ms; tweak as you like
	w.saveTimer.OnTimeout(func() {
		if w == nil || w.win == nil || w.closing {
			return
		}
		// ---- skip persisting if window looks fullscreen-ish ----
		if looksFullscreenish(w.win) {
			// don’t write this transient geometry
			return
		}
		// Persist to config.yml
		if err := UpdateCameraGeometry(w.idKey, w.win.Pos().X(), w.win.Pos().Y(), w.win.Size().Width(), w.win.Size().Height()); err != nil {
			log.Printf("save geometry failed: %v", err)
		}
	})

	view := NewVideoWidget(&w.buf, nil, cfg.Stretch)
	win.SetCentralWidget(view.QWidget)
	view.SetOverlayTitle(safeCamTitle(cfg), globalConfig.NoWindowsTitles)
	view.SetOwner(w)

	// Single-click on the camera window
	win.OnMousePressEvent(func(super func(event *qt.QMouseEvent), event *qt.QMouseEvent) {
		if w.isFullscreen {
			return
		}
		env.activeWin = w

		if !globalConfig.NoWindowsTitles && globalConfig.ActiveOnWin {
			log.Printf("focus activated")
			for _, w := range wins {
				if w == nil {
					continue
				}
				log.Printf("Activating window: %s", w.cfg.Name)
				w.win.Raise()
				w.win.ActivateWindow()
				w.win.SetFocus()
			}
		}
		log.Printf("active window set to: %s", env.activeWin.cfg.Name)
	})

	// Double-click on the camera window
	win.OnMouseDoubleClickEvent(func(super func(event *qt.QMouseEvent), event *qt.QMouseEvent) {
		//super(event)
		w.ToggleFullscreen()
	})

	// Double-click on the video area
	view.OnMouseDoubleClickEvent(func(super func(event *qt.QMouseEvent), event *qt.QMouseEvent) {
		//super(event)
		w.ToggleFullscreen()

	})

	// Restart debounce whenever the user moves or resizes the window
	win.OnMoveEvent(func(super func(event *qt.QMoveEvent), event *qt.QMoveEvent) {
		//if w.isFullscreen || w.suppressSave {
		if w.isFullscreen || w.suppressSave || win.IsFullScreen() || win.IsMaximized() {

			return
		}
		log.Printf("[%s] window moved to %dx%d", w.cfg.Name, event.Pos().X(), event.Pos().Y())
		w.saveTimer.Stop()
		w.saveTimer.Start2()
	})

	win.OnResizeEvent(func(super func(event *qt.QResizeEvent), event *qt.QResizeEvent) {
		//if w.isFullscreen || w.suppressSave {
		if w.isFullscreen || w.suppressSave || win.IsFullScreen() || win.IsMaximized() {

			return
		}
		w.saveTimer.Stop()
		w.saveTimer.Start2()
	})

	// Allow SPACE to toggle recording when this window has focus
	win.OnKeyPressEvent(func(super func(event *qt.QKeyEvent), ev *qt.QKeyEvent) {
		if ev.Key() == int(qt.Key_Space) {
			if env.activeWin != nil {
				env.activeWin.ToggleRecording()
				//w.ToggleRecording()
			}
			ev.Accept()
			return
		}
		super(ev)
	})

	w.win = win
	w.view = view

	win.Show()
	win.Raise()
	win.ActivateWindow()

	// Start decoder loop
	go w.decodeLoop()

	// Very important: repaint on the GUI thread ~30 FPS
	// when creating it (in newCamWindow)
	t := qt.NewQTimer2(win.QObject) // parent to the window
	w.repaintTimer = t
	t.SetInterval(33)
	t.OnTimeout(func() {
		if w != nil && w.view != nil {
			w.view.Present()
		}
	})
	t.Start2()

	w.lastMAt = time.Now()
	w.metricsTimer = qt.NewQTimer()
	w.metricsTimer.SetInterval(1000) // 1s
	w.metricsTimer.OnTimeout(func() {
		now := time.Now()
		dt := now.Sub(w.lastMAt).Seconds()
		if dt <= 0 {
			return
		}
		fd := atomic.LoadInt64(&w.framesDecoded)
		by := atomic.LoadInt64(&w.bytesVideo)
		dr := atomic.LoadInt64(&w.framesDropped)

		dF := fd - w.lastMFrames
		dB := by - w.lastMBytes
		dD := dr - w.lastMDrops

		if dF < 0 {
			dF = 0
		}
		if dB < 0 {
			dB = 0
		}
		if dD < 0 {
			dD = 0
		}

		// cpu metrics
		busy := atomic.LoadInt64(&w.busyNS)
		dBusy := busy - w.lastMBusyNS
		if dBusy < 0 {
			dBusy = 0
		}

		if dt > 0 {
			pct := (float64(dBusy) / (dt * 1e9)) * 100.0 // percent of one core
			if pct < 0 {
				pct = 0
			}
			if pct > 400 {
				pct = 400
			} // clamp (multi-threaded decoders could exceed 100)
			w.cpuPct = pct
		}
		w.lastMBusyNS = busy

		w.fps = float64(dF) / dt
		// bits/sec -> kbps
		w.bitrateKbps = (float64(dB) * 8.0 / dt) / 1000.0
		den := dF + dD
		if den > 0 {
			pct := 100.0 * float64(dD) / float64(den)
			if pct < 0 {
				pct = 0
			}
			if pct > 100 {
				pct = 100
			}
			w.dropsPct = pct
		} else {
			w.dropsPct = 0
		}

		// simple health heuristic 0..5
		score := 0
		switch {
		case w.fps >= 24:
			score = 5
		case w.fps >= 15:
			score = 4
		case w.fps >= 5:
			score = 3
		case w.fps > 0:
			score = 2
		default: // stalled
			score = 0
		}
		if w.dropsPct > 10 {
			if score > 0 {
				score--
			}
		}
		atomic.StoreInt32(&w.health, int32(score))

		w.lastMFrames = fd
		w.lastMBytes = by
		w.lastMDrops = dr
		w.lastMAt = now

		// ask widget to repaint overlays even if frame size unchanged
		if w.view != nil && w.view.QWidget != nil {
			w.view.Update() // safe to call from UI thread (timer is UI)
		}
	})
	w.metricsTimer.Start2()

	// ensure timer stops when window closes
	w.win.OnDestroyed(func() {
		if w.metricsTimer != nil {
			w.metricsTimer.Stop()
			w.metricsTimer.DeleteLater()
			w.metricsTimer = nil
		}
	})

	return w, nil
}

func (w *CamWindow) Close() {
	if w == nil {
		return
	}

	// Make sure we stop recording first
	//if w.IsRecording() {
	//	w.stopRecording()
	//}

	log.Printf("[%s] closing camera", w.cfg.Name)
	w.closing = true
	w.wantPlaying = false

	// stop current decoder
	select {
	case <-w.stop:
		// already closed
	default:
		close(w.stop)
	}
	<-w.done

	if w.repaintTimer != nil {
		w.repaintTimer.Stop()
		w.repaintTimer.DeleteLater()
		w.repaintTimer = nil
	}
	if w.win != nil {
		w.win.Close()
	}
}

func (w *CamWindow) setReconnectSoon() {
	if w.backoff == 0 {
		w.backoff = time.Second
	}
	if w.backoff > 30*time.Second {
		w.backoff = 30 * time.Second
	}
	w.nextTry = time.Now().Add(w.backoff)
	if w.backoff < 30*time.Second {
		w.backoff *= 2
		if w.backoff > 30*time.Second {
			w.backoff = 30 * time.Second
		}
	}
}

// Provide a context menu for the camera window that mirrors the tray menu.
// We build it on-demand to always reflect the latest state.
func (w *CamWindow) SetContextMenu(menu *qt.QMenu) {
	if w == nil || w.view == nil || menu == nil {
		return
	}
	w.view.SetContextMenu(menu)
}

// ToggleFullscreen switches between normal windowed mode and fullscreen.
// While fullscreen, we suppress move/resize persistence.
// On exit we restore the exact previous geometry.
func (w *CamWindow) ToggleFullscreen() {
	if w == nil || w.win == nil {
		return
	}

	if !w.isFullscreen {
		// going fullscreen: remember current geometry
		g := w.win.Geometry()
		w.prevX, w.prevY = g.X(), g.Y()
		w.prevW, w.prevH = g.Width(), g.Height()

		// don’t persist any geometry changes triggered by FS transition
		w.suppressSave = true
		w.isFullscreen = true
		w.win.ShowFullScreen()

		// small single-shot to re-enable normal saves after FS settled
		t := qt.NewQTimer()
		t.SetSingleShot(true)
		t.SetInterval(0)
		t.OnTimeout(func() {
			w.suppressSave = false
			t.DeleteLater()
		})
		t.Start2()
		return
	}

	// leaving fullscreen: restore previous geometry
	w.suppressSave = true
	w.win.ShowNormal()
	if w.prevW > 0 && w.prevH > 0 {
		w.win.SetGeometry(w.prevX, w.prevY, w.prevW, w.prevH)
	}
	w.isFullscreen = false
	w.saveTimer.Stop()
	w.suppressSave = true

	t := qt.NewQTimer()
	t.SetSingleShot(true)
	t.SetInterval(750)
	t.OnTimeout(func() {
		w.suppressSave = false
		t.DeleteLater()
	})
	t.Start2()
}

// OnResumeFromSleep is called when the app detects a system wake.
// We just restart the decoder loop (non-blocking).
func (w *CamWindow) OnResumeFromSleep() {
	if w == nil || w.closing || w.cfg.Disabled {
		return
	}
	// Restart in its own goroutine so we don't block UI.
	go w.restartDecoder("Wake")
}

// Update config then restart decode pipeline.
func (w *CamWindow) RestartWith(c CameraConfig, reason string) {
	w.cfg = c
	w.backoff = 250 * time.Millisecond
	w.restartDecoder(reason)
}

func (w *CamWindow) StopCamera() {
	if w.stop != nil {
		select {
		case <-w.stop: // already closed
		default:
			close(w.stop)
		}
	}
}

func (w *CamWindow) StartCamera() {
	w.restartDecoder("Start")
}

// Restart stops the current decode goroutine (if any) and starts a fresh one.
// Safe to call from any goroutine. Never blocks the UI thread indefinitely.
func (w *CamWindow) restartDecoder(reason string) {
	if w == nil {
		return
	}
	// Stop the current loop, if running.
	if w.stop != nil {
		select {
		case <-w.stop: // already closed
		default:
			close(w.stop)
		}
	}

	// Wait for the loop to report done, but don't hang forever.
	waitCh := make(chan struct{})
	go func(done <-chan struct{}) {
		if done != nil {
			<-done
		}
		close(waitCh)
	}(w.done)

	select {
	case <-waitCh:
	case <-time.After(2 * time.Second):
		// Timed out waiting; proceed anyway.
	}

	// small grace so the RTSP server releases the old session
	time.Sleep(350 * time.Millisecond)
	// Also ensure the next retry cadence is short
	if w.backoff == 0 || w.backoff > time.Second {
		w.backoff = 250 * time.Millisecond
	}

	// Re-create channels and start decoding again.
	w.stop = make(chan struct{})
	w.done = make(chan struct{})

	// Optional: reset “progress” markers if we have them
	// (only touch fields we actually have in our struct).
	// w.lastAdvance = time.Time{}

	log.Printf("[%s] restarting decoder (%s)", w.cfg.Name, reason)
	go w.decodeLoop()
}

func (w *CamWindow) MetricsSnapshot() (fps, kbps, drops, cpu float64, health int) {
	return w.fps, w.bitrateKbps, w.dropsPct, w.cpuPct, int(atomic.LoadInt32(&w.health))
}

func looksFullscreenish(win *qt.QMainWindow) bool {
	if win == nil {
		return false
	}
	if win.IsFullScreen() || win.IsMaximized() {
		return true
	}
	// Compare against the current screen’s *available* geometry
	scr := win.Screen()
	if scr == nil {
		scr = qt.QGuiApplication_PrimaryScreen()
	}
	if scr == nil {
		return false
	}
	sg := scr.AvailableGeometry() // excludes taskbar/dock
	wg := win.Geometry()

	// consider it fullscreen-ish if it occupies (≈) the whole screen
	const tol = 8 // px tolerance
	samePos := abs(wg.X()-sg.X()) <= tol && abs(wg.Y()-sg.Y()) <= tol
	sameW := abs(wg.Width()-sg.Width()) <= tol
	sameH := abs(wg.Height()-sg.Height()) <= tol
	if samePos && sameW && sameH {
		return true
	}

	// also treat >95% of each dimension as fullscreen-ish (frameless edge cases)
	if wg.Width() >= int(float64(sg.Width())*0.95) &&
		wg.Height() >= int(float64(sg.Height())*0.95) {
		return true
	}
	return false
}

// IsRecording reports whether this camera is currently recording.
func (w *CamWindow) IsRecording() bool {
	if w == nil {
		return false
	}
	return w.recording.Load()
}

// ToggleRecording starts or stops recording for this camera.
func (w *CamWindow) ToggleRecording() {
	if w == nil {
		return
	}

	now := !w.recording.Load()
	w.recording.Store(now)

	// Optional: clear last path when starting
	if now {
		w.recPath = ""
	}

	// Just repaint OSD
	/*
		if w.view != nil {
			CallOnQtMain(func() {
				w.view.Update()
			})
		}
	*/

	log.Printf("[%s] recording %s", w.cfg.Name, map[bool]string{true: "ON", false: "OFF"}[now])
}
