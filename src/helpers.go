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
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strings"
	"sync/atomic"
	"unicode"

	astiav "github.com/asticode/go-astiav"
	"github.com/mappu/miqt/qt"
	"github.com/mappu/miqt/qt/mainthread"
)

var appQuitting atomic.Bool   // import "sync/atomic"
var appQuitFilter *quitFilter // keep a global to prevent GC

// postToUI runs fn on the Qt event loop (next tick) using a single-shot QTimer.
// It can be parent to any QObject (e.g., the window) so it won't be GC'd.
func postToUI(parent qt.QObject, fn func()) {
	t := qt.NewQTimer()
	t.SetSingleShot(true)
	t.OnTimeout(func() {
		fn()
		t.DeleteLater()
	})
	t.Start(0) // 0 ms => next event loop iteration
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// generate ID for camera
func genID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// find camera by it's id
func findCameraIndexByID(cfg *AppConfig, id string) int {
	for i := range cfg.Cameras {
		if cfg.Cameras[i].ID == id {
			return i
		}
	}
	return -1
}

// DictPairs returns key=value ffmpeg settings pairs for logging.
func DictPairs(d *astiav.Dictionary) []string {
	if d == nil {
		return nil
	}
	var pairs []string
	var prev *astiav.DictionaryEntry
	flags := astiav.NewDictionaryFlags(astiav.DictionaryFlagIgnoreSuffix) // iterate all keys
	for {
		e := d.Get("", prev, flags)
		if e == nil {
			break
		}
		pairs = append(pairs, fmt.Sprintf("%s=%s", e.Key(), e.Value()))
		prev = e
	}
	sort.Strings(pairs)
	return pairs
}

// JoinDict is a convenience to print in one line.
func JoinDict(d *astiav.Dictionary) string {
	return strings.Join(DictPairs(d), " ")
}

// --- FFmpeg params parsing ---------------------------------------------------
// parseFFmpegParams splits a camera's FFmpeg params string into two maps:
// -fOPTION=value -> fopts[OPTION]=value
// -cOPTION=value -> copts[OPTION]=value
func parseFFmpegParams(s string) (fopts map[string]string, copts map[string]string) {
	fopts = make(map[string]string)
	copts = make(map[string]string)

	for _, tok := range strings.Fields(s) { // ignores extra whitespace
		if len(tok) < 3 || tok[0] != '-' {
			continue
		}
		prefix := tok[1] // 'f' or 'c'
		rest := tok[2:]  // OPTION=value
		eq := strings.IndexByte(rest, '=')
		if eq <= 0 || eq == len(rest)-1 {
			continue // need both key and value
		}
		key := rest[:eq]
		val := rest[eq+1:]

		// strip matching quotes
		if len(val) >= 2 {
			if (val[0] == '"' && val[len(val)-1] == '"') ||
				(val[0] == '\'' && val[len(val)-1] == '\'') {
				val = val[1 : len(val)-1]
			}
		}

		switch prefix {
		case 'f':
			fopts[key] = val
		case 'c':
			copts[key] = val
		}
	}
	return
}

// Apply only -f…=… tokens to the input/format dictionary (rd).
func applyFmtParams(params string, rd *astiav.Dictionary) {
	if params == "" || rd == nil {
		return
	}
	fopts, _ := parseFFmpegParams(params)
	for k, v := range fopts {
		rd.Set(k, v, 0)
	}
}

// Apply only -c…=… tokens to the decoder dictionary (vopts).
func applyDecParams(params string, vopts *astiav.Dictionary) {
	if params == "" || vopts == nil {
		return
	}
	_, copts := parseFFmpegParams(params)
	for k, v := range copts {
		vopts.Set(k, v, 0)
	}
}

// runs function in main thread
func CallOnQtMain(fn func()) {
	mainthread.Wait(fn)
}

// open file (default association) or folder (supports windows, linux, mac)
func openFileOrDir(file string) {
	log.Printf("Opening external: %s\n", file)
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", file)
	case "linux":
		cmd = exec.Command("xdg-open", file)
	case "windows":
		cmd = exec.Command("explorer", file)
	default:
		return
	}
	_ = cmd.Start()
}

// restart the application
func doRestart() {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	args := os.Args[1:]
	cmd := exec.Command(exe, args...)
	cmd.Start()
	os.Exit(0)
}

// Returns true while the Alt/Option key is held.
func altPressed() bool {
	// Most miqt builds expose QGuiApplication_KeyboardModifiers
	mods := qt.QGuiApplication_KeyboardModifiers()
	return (mods & qt.AltModifier) != 0
}

func showAndFocus(win *qt.QMainWindow) {
	if win == nil {
		return
	}
	win.Show()
	win.Raise()
	win.ActivateWindow()
	// Some platforms like a second bump on the next GUI tick
	postToUI(*win.QObject, func() {
		win.Raise()
		win.ActivateWindow()
	})
}

// SanitizeString removes all Unicode whitespace characters from s
// (spaces, tabs, newlines, carriage returns, non-breaking spaces, etc.).
func SanitizeString(s string) string {
	if s == "" {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if !unicode.IsSpace(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

type quitFilter struct{ *qt.QObject }

func newQuitFilter() *quitFilter {
	f := &quitFilter{QObject: qt.NewQObject()}
	f.OnEventFilter(func(super func(*qt.QObject, *qt.QEvent) bool, watched *qt.QObject, e *qt.QEvent) bool {
		if e.Type() == qt.QEvent__Quit {
			log.Println("QEvent::Quit caught early; marking appQuitting")
			appQuitting.Store(true)
		}
		return super(watched, e)
	})
	return f
}

func promptText(title, label string) (string, bool) {
	d := qt.NewQDialog(nil)
	d.SetWindowTitle(title)
	in := qt.NewQLineEdit(nil)
	in.SetMinimumWidth(280)

	lbl := qt.NewQLabel6(label, nil, 0)
	ok := qt.NewQPushButton5("Save", nil)
	cc := qt.NewQPushButton5("Cancel", nil)

	ok.OnClicked(func() { d.Accept() })
	cc.OnClicked(func() { d.Reject() })

	row1 := qt.NewQVBoxLayout(nil)
	row1.AddWidget(lbl.QWidget)
	row1.AddWidget(in.QWidget)

	btns := qt.NewQHBoxLayout(nil)
	btns.AddStretch()
	btns.AddWidget(ok.QWidget)
	btns.AddWidget(cc.QWidget)

	root := qt.NewQVBoxLayout(nil)
	root.AddLayout(row1.QLayout)
	root.AddLayout(btns.QLayout)
	d.SetLayout(root.QLayout)

	if d.Exec() == int(qt.QDialog__Accepted) {
		return strings.TrimSpace(in.Text()), true
	}
	return "", false
}

func addAct(m *qt.QMenu, a *qt.QAction) { m.AddActions([]*qt.QAction{a}) }
func addSep(m *qt.QMenu) {
	sep := qt.NewQAction2("")
	sep.SetSeparator(true)
	addAct(m, sep)
}

// sanitizeFSComponent makes a string safe to use as a single path component.
func sanitizeFSComponent(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "camera"
	}
	// Very conservative: replace things that can break paths
	replacer := strings.NewReplacer(
		"/", "_",
		"\\", "_",
		":", "_",
		"*", "_",
		"?", "_",
		"\"", "_",
		"<", "_",
		">", "_",
		"|", "_",
	)
	return replacer.Replace(s)
}
