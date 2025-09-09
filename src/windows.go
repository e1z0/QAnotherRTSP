//go:build windows
// +build windows

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

/*
#cgo pkg-config: libavformat libavcodec libavutil libswscale libswresample
*/
import "C"

import (
	"log"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

/*
Windows related functions
Such as detect wake/sleep
*/

var (
	user32               = windows.NewLazySystemDLL("user32.dll")
	kernel32             = windows.NewLazySystemDLL("kernel32.dll")
	procRegisterClassExW = user32.NewProc("RegisterClassExW")
	procCreateWindowExW  = user32.NewProc("CreateWindowExW")
	procDefWindowProcW   = user32.NewProc("DefWindowProcW")
	procGetMessageW      = user32.NewProc("GetMessageW")
	procTranslateMessage = user32.NewProc("TranslateMessage")
	procDispatchMessageW = user32.NewProc("DispatchMessageW")
	procGetModuleHandleW = kernel32.NewProc("GetModuleHandleW")
	HWND_MESSAGE         = windows.Handle(^uintptr(2))
)

const (
	WM_POWERBROADCAST      = 0x0218
	PBT_APMSUSPEND         = 0x0004
	PBT_APMRESUMEAUTOMATIC = 0x0012
	PBT_APMRESUMESUSPEND   = 0x0007
)

const (
	CS_VREDRAW uint32 = 0x0001
	CS_HREDRAW uint32 = 0x0002
)

type wndClassEx struct {
	Size       uint32
	Style      uint32
	WndProc    uintptr
	ClsExtra   int32
	WndExtra   int32
	Instance   windows.Handle
	Icon       windows.Handle
	Cursor     windows.Handle
	Background windows.Handle
	MenuName   *uint16
	ClassName  *uint16
	IconSm     windows.Handle
}

type msg struct {
	Hwnd    windows.Handle
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      struct{ X, Y int32 }
}

var pwOnce sync.Once

// dummy wrapper
func IgnoreSignum() {

}

func HandleSleep(wins []*CamWindow) {
	log.Printf("Windows handle sleep function loaded...")
	startWindowsPowerWatcher(func(kind string) {
		// Only restart windows that are currently visible
		if kind == "resume" {
			log.Println("machine awake")
			for _, w := range wins {
				if w != nil && !w.cfg.Disabled && w.win.IsVisible() {
					CallOnQtMain(w.OnResumeFromSleep)
				}
			}
		}
	})
}

// Call this once (safe to call multiple times).
func startWindowsPowerWatcher(onEvent func(kind string)) {
	pwOnce.Do(func() {
		go powerMsgLoop(onEvent)
	})
}

func powerMsgLoop(onEvent func(string)) {
	className, _ := windows.UTF16PtrFromString("QAnotherRTSP.PowerSink")
	hInstance := getModuleHandle()

	wc := wndClassEx{
		Size:      uint32(unsafe.Sizeof(wndClassEx{})),
		Style:     CS_HREDRAW | CS_VREDRAW,
		Instance:  hInstance,
		ClassName: className,
		WndProc: windows.NewCallback(func(hwnd windows.Handle, m uint32, wparam, lparam uintptr) uintptr {
			if m == WM_POWERBROADCAST {
				switch wparam {
				case PBT_APMSUSPEND:
					if onEvent != nil {
						onEvent("suspend")
					}
					return 1
				case PBT_APMRESUMEAUTOMATIC, PBT_APMRESUMESUSPEND:
					if onEvent != nil {
						onEvent("resume")
					}
					return 1
				}
			}
			ret, _, _ := procDefWindowProcW.Call(uintptr(hwnd), uintptr(m), wparam, lparam)
			return ret
		}),
	}

	if r, _, err := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc))); r == 0 {
		log.Printf("power watcher: RegisterClassEx failed: %v", err)
		return
	}

	hwnd, _, err := procCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(className)),
		0, 0,
		0, 0, 0, 0,
		uintptr(HWND_MESSAGE), 0, uintptr(hInstance), 0,
	)
	if hwnd == 0 {
		log.Printf("power watcher: CreateWindowEx failed: %v", err)
		return
	}

	var m msg
	for {
		r, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		switch int32(r) {
		case -1:
			log.Printf("power watcher: GetMessageW error")
			return
		case 0:
			return // WM_QUIT
		default:
			procTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
			procDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
		}
	}
}

// Get HINSTANCE without relying on windows.GetModuleHandle (not present in x/sys v0.7.0).
func getModuleHandle() windows.Handle {
	r, _, _ := procGetModuleHandleW.Call(0) // NULL => current module
	return windows.Handle(r)
}
