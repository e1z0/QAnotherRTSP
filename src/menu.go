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
	"sync"

	"github.com/mappu/miqt/qt"
)

/*
Menu and context menu generation unit
*/

type TrayController struct {
	mu      sync.Mutex
	tray    *qt.QSystemTrayIcon
	cfg     *AppConfig
	wins    *[]*CamWindow
	actions []*qt.QAction // one per camera index
}

func NewTrayController(cfg *AppConfig, winsA *[]*CamWindow) *TrayController {
	t := &TrayController{
		cfg:  cfg,
		wins: winsA,
	}

	// Always-present icon (tiny gray square) so SetIcon never crashes.
	pm := qt.NewQPixmap2(16, 16)
	pm.FillWithFillColor(qt.NewQColor11(80, 80, 80, 255))

	t.tray = qt.NewQSystemTrayIcon()
	t.tray.SetIcon(globalIcon)
	t.tray.SetToolTip(app)
	t.tray.SetVisible(true)
	t.tray.OnActivated(func(reason qt.QSystemTrayIcon__ActivationReason) {
		if reason == qt.QSystemTrayIcon__Trigger {
			if globalConfig.ActiveOnTray {
				log.Printf("Tray icon clicked, activating all windows...\n")
				for _, w := range wins {
					if w == nil || w.win == nil {
						continue
					}
					w.win.Show()
					w.win.Raise()
				}
			}
		}
	})

	t.rebuild()
	return t
}

// Make sure wins has a slot for each camera.
func (t *TrayController) ensureWinsLen() {
	if len(*t.wins) < len(t.cfg.Cameras) {
		*t.wins = append(*t.wins, make([]*CamWindow, len(t.cfg.Cameras)-len(*t.wins))...)
	} else if len(*t.wins) > len(t.cfg.Cameras) {
		*t.wins = (*t.wins)[:len(t.cfg.Cameras)]
	}
}

// Rebuild menu from scratch
func (t *TrayController) rebuild() {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.ensureWinsLen()

	menu := qt.NewQMenu(nil)

	cams := qt.NewQMenu4("Cameras", menu.QWidget)
	t.actions = make([]*qt.QAction, len(t.cfg.Cameras))

	for i := range t.cfg.Cameras {
		idx := i
		c := &t.cfg.Cameras[idx]

		title := c.Name
		if title == "" {
			title = c.URL
		}
		act := cams.AddAction(title)
		act.SetCheckable(true)

		// Enabled = has a live window AND not marked disabled
		enabled := !c.Disabled && (*t.wins)[idx] != nil

		// Set initial state without firing the signal
		act.BlockSignals(true)
		act.SetChecked(enabled)
		act.BlockSignals(false)

		// Capture this QAction so we can update its check state immediately.
		thisAction := act
		act.OnToggled(func(checked bool) {
			t.onActionToggled(idx, checked, thisAction)
		})

		t.actions[idx] = act
	}

	menu.AddActions(t.actions) // as main menu
	menu.AddSeparator()

	optionsMenu := qt.NewQMenu(nil)
	optionsMenu.SetTitle("Settings")

	settingsItem := optionsMenu.AddAction("Settings")
	settingsItem.OnTriggered(func() {
		log.Printf("Tray settings clicked, showing settings window...\n")
		ShowSettingsDialog(nil)
	})

	disableCamsItem := optionsMenu.AddAction("Pause cameras")
	disableCamsItem.OnTriggered(func() {
		log.Printf("Disable cameras clicked...\n")
		for _, w := range wins {
			if w == nil || w.win == nil {
				continue
			}
			w.StopCamera()
		}
	})

	enableCamsItem := optionsMenu.AddAction("Resume cameras")
	enableCamsItem.OnTriggered(func() {
		log.Printf("Enable cameras clicked...\n")
		for _, w := range wins {
			if w == nil || w.win == nil {
				continue
			}
			w.StartCamera()
		}
	})

	configLocItem := optionsMenu.AddAction("Config location")
	configLocItem.OnTriggered(func() {
		log.Printf("Tray config location clicked, opening config dir...\n")
		openFileOrDir(env.configDir)
	})

	logFileItem := optionsMenu.AddAction("Logfile")
	logFileItem.OnTriggered(func() {
		log.Printf("Tray log file clicked, opening log file...\n")
		openFileOrDir(env.appDebugLog)
	})

	restartItem := optionsMenu.AddAction("Restart app")
	restartItem.OnTriggered(func() {
		log.Printf("Tray restart clicked, restarting app...\n")
		doRestart()
	})

	updateTrayItem := optionsMenu.AddAction("Update traymenu")
	updateTrayItem.OnTriggered(func() {
		log.Printf("Tray update clicked, updating tray menu...\n")
		tray.rebuild()
	})

	aboutItem := optionsMenu.AddAction("About...")
	aboutItem.OnTriggered(func() {
		log.Printf("About clicked, showing about dialog...\n")
		ShowAboutDialog(nil, AboutInfo{
			AppName:     app,
			Version:     version,
			Build:       build,
			Lines:       lines,
			HomepageURL: "https://github.com/e1z0/QAnotherRTSP",
			SupportURL:  "https://github.com/e1z0/QAnotherRTSP/issues",
			LicenseText: LicenseText, // string const with license
			CreditsHTML: `<p>Built with <b>Go</b>, <b>Qt</b>, <b>MIQT</b>, <b>FFmpeg</b>, and love.</p>`,
			Icon:        globalIcon, // app icon *qt.QIcon
		})
	})

	menu.AddMenu(optionsMenu)

	menu.AddAction("Quit").OnTriggered(func() {
		qt.QCoreApplication_Exit()
	})

	t.tray.SetContextMenu(menu)
}

// Keep menu check state and actual window state in lockstep.
func (t *TrayController) onActionToggled(idx int, checked bool, act *qt.QAction) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if idx < 0 || idx >= len(t.cfg.Cameras) {
		return
	}
	t.ensureWinsLen()

	c := &t.cfg.Cameras[idx]

	if !checked {
		// Turn OFF → close window and mark disabled
		c.Disabled = true
		// Force checkbox to OFF without re-triggering the slot
		act.BlockSignals(true)
		act.SetChecked(false)
		act.BlockSignals(false)
		// Grab and clear the window slot first, then close non-blocking.
		w := (*t.wins)[idx]
		(*t.wins)[idx] = nil
		if w != nil {
			w.SuppressOnClosedOnce()
			w.Close()
		}

		if err := SaveConfig(); err != nil {
			log.Printf("save config: %v", err)
		}
		return
	}

	// Turn ON → open the window and mark enabled
	c.Disabled = false
	if (*t.wins)[idx] == nil {
		w, err := newCamWindow(*c, idx)
		if err != nil {
			log.Printf("open cam %q: %v", c.Name, err)
			// revert checkbox to OFF if failed
			act.BlockSignals(true)
			act.SetChecked(false)
			act.BlockSignals(false)
			c.Disabled = true
			return
		}
		(*t.wins)[idx] = w
		// give hooks + context menu
		t.AttachWindowHooks(idx, w)
	}

	// Force checkbox to ON (should already be true, but keep them in sync)
	act.BlockSignals(true)
	act.SetChecked(true)
	act.BlockSignals(false)

	if err := SaveConfig(); err != nil {
		log.Printf("save config: %v", err)
	}
}

// AttachWindowHooks wires:
//   - the same context menu to the camera window,
//   - a close-event hook to uncheck the tray item and update config.
func (t *TrayController) AttachWindowHooks(idx int, w *CamWindow) {
	if w == nil {
		return
	}
	// Same context menu on the video widget (right-click)
	if w.view != nil && t.tray != nil && t.tray.ContextMenu() != nil {
		w.view.SetContextMenu(t.tray.ContextMenu())
	}

	// When a user closes the camera window, uncheck in tray + mark disabled
	w.SetOnClosed(func(i int) {
		t.WindowWasClosed(i)
	})
}

func (t *TrayController) WindowWasClosed(idx int) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if idx < 0 || idx >= len(t.cfg.Cameras) {
		return
	}
	t.ensureWinsLen()

	// Clear window slot and disable camera in config
	(*t.wins)[idx] = nil
	t.cfg.Cameras[idx].Disabled = true

	// Uncheck the corresponding tray action, if any
	if idx < len(t.actions) && t.actions[idx] != nil {
		t.actions[idx].BlockSignals(true)
		t.actions[idx].SetChecked(false)
		t.actions[idx].BlockSignals(false)
	}

	if err := SaveConfig(); err != nil {
		log.Printf("save config: %v", err)
	}
}
