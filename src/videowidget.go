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
	"log"
	"unsafe"

	"github.com/mappu/miqt/qt"
)

// VideoWidget repaints from the shared frameBuf.
type VideoWidget struct {
	*qt.QWidget
	buf     *frameBuf
	Stretch bool
	// drag/resize state for frameless windows ---
	owner    *CamWindow
	dragging bool
	resizing bool
	edgeMask int // bitmask: 1=L,2=R,4=T,8=B
	pressGX  int // global mouse pos at press
	pressGY  int
	origX    int // original window geometry
	origY    int
	origW    int
	origH    int
	titleLbl *qt.QLabel // camera name label
	// group of glued windows (including owner) and their original positions
	group    []*CamWindow
	groupPos map[*CamWindow]struct{ X, Y int }
}

const (
	edgeLeft  = 1
	edgeRight = 2
	edgeTop   = 4
	edgeBot   = 8
)

func NewVideoWidget(buf *frameBuf, parent *qt.QWidget, stretch bool) *VideoWidget {
	w := &VideoWidget{
		QWidget: qt.NewQWidget(parent),
		buf:     buf,
		Stretch: stretch,
	}
	// --- overlay camera name label (top-left) ---
	w.titleLbl = qt.NewQLabel(nil)
	w.titleLbl.SetParent(w.QWidget)
	w.titleLbl.SetText("") // set later via SetOverlayTitle
	// readable on any video; can be tweaked
	w.titleLbl.SetStyleSheet(
		"color: rgba(255,255,255,0.97);" +
			"background: rgba(0,0,0,0.55);" +
			"padding: 2px 8px;" +
			"border-radius: 6px;" +
			"font-size: 11pt;",
	)
	// Don't intercept mouse (so drag/resize still works if you have it)
	w.titleLbl.SetAttribute2(qt.WA_TransparentForMouseEvents, true)
	w.titleLbl.Hide()

	w.SetAttribute2(qt.WA_OpaquePaintEvent, true)
	w.SetAutoFillBackground(false)
	w.SetMinimumSize2(32, 32)

	w.OnPaintEvent(func(super func(event *qt.QPaintEvent), event *qt.QPaintEvent) {
		p := qt.NewQPainter2(w.QPaintDevice)
		defer p.End()
		// background
		p.FillRect6(w.Rect(), qt.NewQColor11(0, 0, 0, 255)) // black background

		// latest frame
		seq, srcW, srcH, data := w.buf.get()
		if seq == 0 || srcW <= 0 || srcH <= 0 || len(data) < srcW*srcH*4 {
			return
		}

		// Build a QImage that we own (Format_RGB32 == 4 bytes/pixel, BGRA layout on little-endian)
		img := qt.NewQImage3(srcW, srcH, qt.QImage__Format_RGB32)
		defer img.Delete()

		// Copy the BGRA buffer into the QImage
		// NOTE: NewQImage3 gives bytesPerLine = 4*width, so we can copy in one shot.
		bits := img.Bits() // unsafe.Pointer to the first pixel
		dst := unsafe.Slice((*byte)(bits), srcW*srcH*4)
		copy(dst, data[:srcW*srcH*4])

		// Compute destination rect (letterbox by default)
		dstW, dstH := w.Width(), w.Height()
		if dstW <= 0 || dstH <= 0 {
			return
		}

		var dest *qt.QRect
		if w.Stretch {
			// fill widget (may distort)
			dest = qt.NewQRect4(0, 0, dstW, dstH)
		} else {
			// keep aspect (letterbox/pillarbox)
			sx := float64(dstW) / float64(srcW)
			sy := float64(dstH) / float64(srcH)
			s := sx
			if sy < s {
				s = sy
			}
			outW := int(float64(srcW)*s + 0.5)
			outH := int(float64(srcH)*s + 0.5)
			offX := (dstW - outW) / 2
			offY := (dstH - outH) / 2
			dest = qt.NewQRect4(offX, offY, outW, outH)
		}

		srcRect := qt.NewQRect4(0, 0, srcW, srcH)
		p.SetRenderHint2(qt.QPainter__SmoothPixmapTransform, true)
		p.DrawImage2(dest, img, srcRect)
	})
	w.SetMouseTracking(true) // track hover to update resize cursor

	// Set appropriate hover cursor
	setHoverCursor := func(mask int) {
		if mask == 0 || !w.isFramelessActive() {
			w.UnsetCursor()
			return
		}

		switch mask {
		case edgeLeft | edgeTop, edgeRight | edgeBot:
			w.SetCursor(qt.NewQCursor2(qt.SizeFDiagCursor))
		case edgeRight | edgeTop, edgeLeft | edgeBot:
			w.SetCursor(qt.NewQCursor2(qt.SizeBDiagCursor))
		case edgeLeft, edgeRight:
			w.SetCursor(qt.NewQCursor2(qt.SizeHorCursor))
		case edgeTop, edgeBot:
			w.SetCursor(qt.NewQCursor2(qt.SizeVerCursor))
		default:
			w.SetCursor(qt.NewQCursor2(qt.ArrowCursor))
		}
	}

	// Mouse press: enter move/resize mode
	w.OnMousePressEvent(func(super func(event *qt.QMouseEvent), ev *qt.QMouseEvent) {
		if ev.Button() != qt.LeftButton || !w.isFramelessActive() {
			super(ev)
			return
		}
		if globalConfig.ActiveOnWin && !w.IsFullScreen() {
			log.Printf("focus frameless")
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

		top := w.QWidget.Window()
		if top == nil {
			super(ev)
			return
		}

		// Record original window geometry
		g := top.Geometry()
		w.origX, w.origY = g.X(), g.Y()
		w.origW, w.origH = g.Width(), g.Height()

		// Where did we click, relative to widget?
		lp := ev.Pos()
		w.edgeMask = w.hitEdges(lp.X(), lp.Y())

		// Global press position (for delta)
		gp := w.QWidget.MapToGlobal(lp)
		w.pressGX, w.pressGY = gp.X(), gp.Y()

		if w.edgeMask != 0 {
			w.resizing = true
			setHoverCursor(w.edgeMask)
		} else {
			w.dragging = true
			w.SetCursor(qt.NewQCursor2(qt.SizeAllCursor))
			if w.snapActive() {
				w.buildGroup() // only build glue group when enabled
			} else {
				w.group = nil
				w.groupPos = nil
			}
		}
		ev.Accept()
	})

	// update geometry or cursor
	w.OnMouseMoveEvent(func(super func(event *qt.QMouseEvent), ev *qt.QMouseEvent) {
		if !w.isFramelessActive() {
			super(ev)
			return
		}
		top := w.QWidget.Window()
		if top == nil {
			super(ev)
			return
		}

		lp := ev.Pos()

		// While not dragging, just update hover cursor near edges
		if !w.dragging && !w.resizing {
			setHoverCursor(w.hitEdges(lp.X(), lp.Y()))
			super(ev)
			return
		}

		// Compute delta from press (in global coords)
		gp := w.QWidget.MapToGlobal(lp)
		dx := gp.X() - w.pressGX
		dy := gp.Y() - w.pressGY

		// Min sizes (respect your current min; widget has 32x32, use window min if set)
		minW, minH := 160, 120
		if top.MinimumSize().Width() > 0 {
			minW = top.MinimumSize().Width()
		}
		if top.MinimumSize().Height() > 0 {
			minH = top.MinimumSize().Height()
		}

		nx, ny := w.origX, w.origY
		nw, nh := w.origW, w.origH

		if w.resizing {
			if w.edgeMask&edgeLeft != 0 {
				nx = w.origX + dx
				nw = w.origW - dx
				if nw < minW {
					nx = w.origX + (w.origW - minW)
					nw = minW
				}
			}
			if w.edgeMask&edgeRight != 0 {
				nw = w.origW + dx
				if nw < minW {
					nw = minW
				}
			}
			if w.edgeMask&edgeTop != 0 {
				ny = w.origY + dy
				nh = w.origH - dy
				if nh < minH {
					ny = w.origY + (w.origH - minH)
					nh = minH
				}
			}
			if w.edgeMask&edgeBot != 0 {
				nh = w.origH + dy
				if nh < minH {
					nh = minH
				}
			}
		} else if w.dragging {
			nx = w.origX + dx
			ny = w.origY + dy
		}

		// Apply geometry
		top.SetGeometry(nx, ny, nw, nh)

		if w.snapActive() {
			// snap the lead window, then offset the group by the same delta
			nx, ny = w.snapXY(nx, ny, w.origW, w.origH)
			sdx := nx - (w.origX + dx)
			sdy := ny - (w.origY + dy)

			// move glued group (if any); always include owner
			moved := false
			for _, cw := range w.group {
				if cw == nil || cw.win == nil {
					continue
				}
				op := w.groupPos[cw]
				cw.win.Move(op.X+dx+sdx, op.Y+dy+sdy)
				moved = true
			}
			if !moved && w.owner != nil && w.owner.win != nil {
				// fallback: move only this window
				w.owner.win.Move(nx, ny)
			}
		} else {
			if w.owner != nil && w.owner.win != nil {
				w.owner.win.Move(nx, ny)
			}
		}
		ev.Accept()
	})

	// Mouse release: leave move/resize mode
	w.OnMouseReleaseEvent(func(super func(event *qt.QMouseEvent), ev *qt.QMouseEvent) {
		if (w.dragging || w.resizing) && ev.Button() == qt.LeftButton {
			w.dragging, w.resizing = false, false
			w.edgeMask = 0
			w.UnsetCursor()
			w.group = nil
			w.groupPos = nil
			ev.Accept()
			return
		}
		super(ev)
	})

	// keep the label placed at top-left on resize
	w.OnResizeEvent(func(super func(*qt.QResizeEvent), ev *qt.QResizeEvent) {
		super(ev)
		if w.titleLbl != nil && w.titleLbl.IsVisible() {
			const margin = 8
			w.titleLbl.Move(margin, margin)
		}
	})
	// ensure the central content can force the window to be visible-sized
	w.SetSizePolicy2(qt.QSizePolicy__Expanding, qt.QSizePolicy__Expanding)
	w.SetMinimumSize2(200, 140) // ~ 16:9-ish; adjust if needed

	return w
}

// Present requests a repaint from any thread.
func (w *VideoWidget) Present() { w.Update() }

// We call this from the tray controller.
func (v *VideoWidget) SetContextMenu(menu *qt.QMenu) {
	if v == nil || v.QWidget == nil || menu == nil {
		return
	}
	v.QWidget.SetContextMenuPolicy(qt.CustomContextMenu)
	v.QWidget.OnCustomContextMenuRequested(func(pos *qt.QPoint) {
		global := v.QWidget.MapToGlobal(pos)
		menu.Popup(global)
	})
}

// SetOverlayTitle updates the small top-left label shown in frameless mode.
func (w *VideoWidget) SetOverlayTitle(text string, visible bool) {
	if w == nil || w.titleLbl == nil {
		return
	}
	w.titleLbl.SetText(text)
	if visible {
		const margin = 8
		w.titleLbl.AdjustSize() // fit content
		w.titleLbl.Move(margin, margin)
		w.titleLbl.Show()
		w.titleLbl.Raise()
	} else {
		w.titleLbl.Hide()
	}
}

func (w *VideoWidget) SetOwner(cw *CamWindow) { w.owner = cw }

func (w *VideoWidget) isFramelessActive() bool {
	top := w.QWidget.Window()
	if top == nil {
		return false
	}
	if !globalConfig.NoWindowsTitles {
		return false
	} // only in borderless mode
	return !top.IsFullScreen()
}

func (w *VideoWidget) hitEdges(px, py int) int {
	const m = 8
	r := w.Rect()
	mask := 0
	if px <= m {
		mask |= edgeLeft
	}
	if px >= r.Width()-m {
		mask |= edgeRight
	}
	if py <= m {
		mask |= edgeTop
	}
	if py >= r.Height()-m {
		mask |= edgeBot
	}
	return mask
}

func (w *VideoWidget) buildGroup() {
	if !w.snapActive() {
		// no-op glue when disabled
		w.group = nil
		w.groupPos = nil
		return
	}
	w.group = nil
	w.groupPos = map[*CamWindow]struct{ X, Y int }{}
	if w.owner == nil || w.owner.win == nil {
		return
	}
	if !w.isFramelessActive() {
		return
	}

	// quick accessors
	getRect := func(cw *CamWindow) (x, y, rw, rh int) {
		g := cw.win.Geometry()
		return g.X(), g.Y(), g.Width(), g.Height()
	}
	touching := func(a, b *CamWindow) bool {
		if a == nil || b == nil || a == b || a.win == nil || b.win == nil {
			return false
		}
		ax, ay, aw, ah := getRect(a)
		bx, by, bw, bh := getRect(b)
		const tol = 1
		// edges aligned and ranges overlap
		ax2, ay2 := ax+aw, ay+ah
		bx2, by2 := bx+bw, by+bh
		overlapX := !(ax2 <= bx || bx2 <= ax)
		overlapY := !(ay2 <= by || by2 <= ay)
		return (abs(ax2-bx) <= tol && overlapY) || // a right to b left
			(abs(bx2-ax) <= tol && overlapY) || // a left to b right
			(abs(ay2-by) <= tol && overlapX) || // a bottom to b top
			(abs(by2-ay) <= tol && overlapX) // a top to b bottom
	}

	visited := map[*CamWindow]bool{}
	queue := []*CamWindow{w.owner}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if cur == nil || visited[cur] {
			continue
		}
		visited[cur] = true
		w.group = append(w.group, cur)
		x, y, _, _ := getRect(cur)
		w.groupPos[cur] = struct{ X, Y int }{x, y}
		// enqueue neighbors that touch cur
		for _, other := range wins {
			if other == nil || other.win == nil || other.isFullscreen {
				continue
			}
			if other == cur {
				continue
			}
			if touching(cur, other) {
				queue = append(queue, other)
			}
		}
	}
}

func (w *VideoWidget) snapXY(x, y, ww, wh int) (int, int) {
	if !w.isFramelessActive() {
		return x, y
	}
	if !w.snapActive() {
		return x, y
	}

	const snap = 12 // px
	bestDx, bestDy := 0, 0
	bestAbsX, bestAbsY := snap+1, snap+1 // “no snap” unless closer than snap

	// snap against all OTHER windows not in the current group
	inGroup := map[*CamWindow]bool{}
	for _, g := range w.group {
		inGroup[g] = true
	}

	for _, other := range wins {
		if other == nil || other.win == nil || other.isFullscreen {
			continue
		}
		if inGroup[other] {
			continue
		}
		og := other.win.Geometry()
		ox, oy, ow, oh := og.X(), og.Y(), og.Width(), og.Height()

		// Candidate deltas for horizontal snap (left/right)
		// my left to other right
		if d := x - (ox + ow); abs(d) < bestAbsX && abs(d) <= snap {
			bestAbsX, bestDx = abs(d), -d
		}
		// my right to other left
		if d := (x + ww) - ox; abs(d) < bestAbsX && abs(d) <= snap {
			bestAbsX, bestDx = abs(d), -d
		}

		// Vertical (top/bottom)
		// my top to other bottom
		if d := y - (oy + oh); abs(d) < bestAbsY && abs(d) <= snap {
			bestAbsY, bestDy = abs(d), -d
		}
		// my bottom to other top
		if d := (y + wh) - oy; abs(d) < bestAbsY && abs(d) <= snap {
			bestAbsY, bestDy = abs(d), -d
		}
	}
	return x + bestDx, y + bestDy
}

func (w *VideoWidget) snapActive() bool {
	// snap requires frameless + setting enabled + not fullscreen
	top := w.QWidget.Window()
	if top == nil {
		return false
	}
	if !globalConfig.NoWindowsTitles {
		return false
	}
	if !globalConfig.SnapEnabled {
		return false
	}
	if altPressed() {
		return false
	} // Holding Alt disables snap/glue

	return !top.IsFullScreen()
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
