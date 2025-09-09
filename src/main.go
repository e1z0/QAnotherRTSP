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
	"flag"
	"log"
	"os"
	"runtime"
	"strings"

	astiav "github.com/asticode/go-astiav"
	"github.com/mappu/miqt/qt"
)

/*
This is the main unit of the application
*/

var tray *TrayController
var wins []*CamWindow
var globalIcon *qt.QIcon
var debugFrames *bool
var version string
var build string
var lines string
var debugging = "false"
var DEBUG bool

var app = "QAnotherRTSP"

func main() {
	debugG := flag.Bool("debug", false, "General debugging override")
	DebugFF := flag.Bool("debugstreams", false, "Debug streams")
	debugFrames = flag.Bool("debugframes", false, "Debug frames per camera")
	flag.Parse()
	runtime.LockOSThread()
	defer runtime.UnlockOSThread() // this will run after Exec() returns
	if *debugG {
		debugging = "true"
	}
	log.Printf("Running %s v%s (build: %s)", app, version, build)
	if *DebugFF {
		astiav.SetLogLevel(astiav.LogLevelDebug)
		astiav.SetLogCallback(func(c astiav.Classer, l astiav.LogLevel, fmt, msg string) {
			var cs string
			if c != nil {
				if cl := c.Class(); cl != nil {
					cs = " - class: " + cl.String()
				}
			}
			log.Printf("ffmpeg log: %s%s - level: %d\n", strings.TrimSpace(msg), cs, l)
		})
	}
	// Turn on Qt’s internal logs
	//_ = os.Setenv("QT_LOGGING_RULES", "qt.multimedia.*=true;qt.network.*=true")

	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	qt.QCoreApplication_SetQuitLockEnabled(true)
	qt.QGuiApplication_SetQuitOnLastWindowClosed(false)
	qt.QCoreApplication_SetAttribute2(qt.AA_ShareOpenGLContexts, true)

	qt.NewQApplication(os.Args)

	pixmap := qt.NewQPixmap()
	pixmap.Load(":/icon.png")
	globalIcon = qt.NewQIcon2(pixmap)
	qt.QApplication_SetWindowIcon(globalIcon)
	qt.QGuiApplication_SetWindowIcon(globalIcon)

	// Initialize global audio on the main (Qt) thread to avoid crash.
	if err := InitGlobalAudio(8000, 1); err != nil {
		log.Fatalf("audio init failed: %v", err)
	}

	// Initialize and load configuration
	cfg, err := loadConfig(env.settingsFile)
	if err != nil {
		qt.QMessageBox_Critical(nil, "Error", "Failed to load configuration. I will create a new file for you!")
		log.Printf("config: %v", err)
		SaveConfig()
		loadConfig(env.settingsFile)
	}

	globalConfig = cfg
	ensureCameraIDs(globalConfig.Cameras) // ensure that the cameras have identification numbers

	wins = make([]*CamWindow, len(globalConfig.Cameras))

	// Start any enabled cameras
	for i := range globalConfig.Cameras {
		if globalConfig.Cameras[i].Disabled {
			log.Printf("camera %q: disabled, skipping", safeCamTitle(globalConfig.Cameras[i]))
			continue
		}
		w, err := newCamWindow(globalConfig.Cameras[i], i)
		if err != nil {
			log.Printf("open cam %q: %v", safeCamTitle(globalConfig.Cameras[i]), err)
			cfg.Cameras[i].Disabled = true
			continue
		}
		wins[i] = w
	}

	// Tray controller (builds the checkable Cameras menu)
	tray = NewTrayController(&globalConfig, &wins)

	// Give existing windows hooks + the same context menu as the tray
	for i, w := range wins {
		if w == nil {
			continue
		}
		tray.AttachWindowHooks(i, w)
	}
	IgnoreSignum()

	go HandleSleep(wins)

	if len(cfg.Cameras) == 0 {
		qt.QMessageBox_Critical(nil, "Error", "No cameras defined in the configuration")
		ShowSettingsDialog(nil)
	}

	code := qt.QApplication_Exec()
	// cleanup
	SaveConfig()
	for _, w := range wins {
		w.Close()
	}
	os.Exit(code)
}

// helper title
func safeCamTitle(c CameraConfig) string {
	if c.Name != "" {
		return c.Name
	}
	return c.URL
}

// entrypoint for runtime variables initialization
func init() {
	InitializeEnvironment()
}
