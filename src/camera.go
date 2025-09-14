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
	"time"

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
		// Only notify if not suppressed
		if w.onClosed != nil && !w.suppressOnClosed {
			w.Close()
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
		if w.isFullscreen || w.suppressSave {
			return
		}
		w.saveTimer.Stop()
		w.saveTimer.Start2()
	})

	win.OnResizeEvent(func(super func(event *qt.QResizeEvent), event *qt.QResizeEvent) {
		if w.isFullscreen || w.suppressSave {
			return
		}
		w.saveTimer.Stop()
		w.saveTimer.Start2()
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
	return w, nil
}

func (w *CamWindow) Close() {
	if w == nil {
		return
	}
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

	t := qt.NewQTimer()
	t.SetSingleShot(true)
	t.SetInterval(0)
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
