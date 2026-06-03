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
	// formations UI
	formMenu        *qt.QMenu
	formSaveAct     *qt.QAction
	formDelMenu     *qt.QMenu
	formSubmenus    map[string]*qt.QMenu
	formMenuMounted bool
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

	t.formMenuMounted = false

	t.actions = make([]*qt.QAction, len(t.cfg.Cameras))

	for i := range t.cfg.Cameras {
		idx := i
		c := &t.cfg.Cameras[idx]

		title := c.Name
		if title == "" {
			title = c.URL
		}

		enabled := !c.Disabled && (*t.wins)[idx] != nil
		if enabled {
			camMenu := qt.NewQMenu4(title, menu.QWidget)
			menu.AddMenu(camMenu)

			topAction := camMenu.MenuAction()
			topAction.SetCheckable(true)
			topAction.BlockSignals(true)
			topAction.SetChecked(true)
			topAction.BlockSignals(false)

			disableAct := camMenu.AddAction("Disable camera")
			disableAct.OnTriggered(func() {
				t.onActionToggled(idx, false, topAction)
			})

			camMenu.AddSeparator()

			mutedAct := camMenu.AddAction("Mute audio")
			mutedAct.SetCheckable(true)
			mutedAct.BlockSignals(true)
			mutedAct.SetChecked(c.Mute)
			mutedAct.BlockSignals(false)
			mutedAct.OnToggled(func(checked bool) {
				t.onCameraMuteToggled(idx, checked, mutedAct)
			})

			topAct := camMenu.AddAction("Always on top")
			topAct.SetCheckable(true)
			topAct.BlockSignals(true)
			topAct.SetChecked(c.AlwaysOnTop)
			topAct.BlockSignals(false)
			topAct.OnToggled(func(checked bool) {
				t.onCameraAlwaysOnTopToggled(idx, checked, topAct)
			})

			camMenu.AddSeparator()
			settingsAct := camMenu.AddAction("Camera settings...")
			settingsAct.OnTriggered(func() {
				EditCameraAtIndex(nil, idx)
			})

			t.actions[idx] = topAction
			continue
		}

		act := menu.AddAction(title)
		act.SetCheckable(true)
		act.BlockSignals(true)
		act.SetChecked(false)
		act.BlockSignals(false)
		thisAction := act
		act.OnToggled(func(checked bool) {
			t.onActionToggled(idx, checked, thisAction)
		})
		t.actions[idx] = act
	}

	if len(t.cfg.Cameras) > 0 {
		menu.AddSeparator()
	}

	//if t.formMenu != nil {
	//	t.rebuildFormationsList() // refresh content only
	//} else {
	//	t.installFormationsMenu(menu) // formation menu
	//}
	t.installFormationsMenu(menu)

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

func (t *TrayController) onCameraMuteToggled(idx int, muted bool, act *qt.QAction) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if idx < 0 || idx >= len(t.cfg.Cameras) {
		return
	}

	t.cfg.Cameras[idx].Mute = muted
	if idx < len(*t.wins) && (*t.wins)[idx] != nil {
		(*t.wins)[idx].cfg.Mute = muted
	}

	act.BlockSignals(true)
	act.SetChecked(muted)
	act.BlockSignals(false)

	if err := SaveConfig(); err != nil {
		log.Printf("save config: %v", err)
	}
}

func (t *TrayController) onCameraAlwaysOnTopToggled(idx int, atop bool, act *qt.QAction) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if idx < 0 || idx >= len(t.cfg.Cameras) {
		return
	}

	t.cfg.Cameras[idx].AlwaysOnTop = atop
	if idx < len(*t.wins) && (*t.wins)[idx] != nil {
		(*t.wins)[idx].cfg.AlwaysOnTop = atop
		(*t.wins)[idx].ApplyWindowSettings()
	}

	act.BlockSignals(true)
	act.SetChecked(atop)
	act.BlockSignals(false)

	if err := SaveConfig(); err != nil {
		log.Printf("save config: %v", err)
	}
}

// Keep menu check state and actual window state in lockstep.
func (t *TrayController) onActionToggled(idx int, checked bool, act *qt.QAction) {
	t.mu.Lock()

	if idx < 0 || idx >= len(t.cfg.Cameras) {
		t.mu.Unlock()
		return
	}
	t.ensureWinsLen()

	c := &t.cfg.Cameras[idx]
	rebuildMenu := false

	if !checked {
		// Turn OFF → close window and mark disabled
		c.Disabled = true
		if act != nil {
			act.BlockSignals(true)
			act.SetChecked(false)
			act.BlockSignals(false)
		}
		// Grab and clear the window slot first, then close non-blocking.
		w := (*t.wins)[idx]
		(*t.wins)[idx] = nil
		if w != nil {
			w.SuppressOnClosedOnce()
			w.Close()
		}
		rebuildMenu = true

		if err := SaveConfig(); err != nil {
			log.Printf("save config: %v", err)
		}
		t.mu.Unlock()
		if rebuildMenu {
			rebuildTrayContextMenus()
		}
		return
	}

	// Turn ON → open the window and mark enabled
	c.Disabled = false
	if (*t.wins)[idx] == nil {
		w, err := newCamWindow(*c, idx)
		if err != nil {
			log.Printf("open cam %q: %v", c.Name, err)
			c.Disabled = true
			if act != nil {
				act.BlockSignals(true)
				act.SetChecked(false)
				act.BlockSignals(false)
			}
			t.mu.Unlock()
			rebuildTrayContextMenus()
			return
		}
		(*t.wins)[idx] = w
		// give hooks + context menu
		t.AttachWindowHooks(idx, w)
	}
	rebuildMenu = true

	if act != nil {
		act.BlockSignals(true)
		act.SetChecked(true)
		act.BlockSignals(false)
	}

	if err := SaveConfig(); err != nil {
		log.Printf("save config: %v", err)
	}
	t.mu.Unlock()
	if rebuildMenu {
		rebuildTrayContextMenus()
	}
}

// AttachWindowHooks wires:
//   - the same context menu to the camera window,
//   - a close-event hook to uncheck the tray item and update config.
func (t *TrayController) AttachWindowHooks(idx int, w *CamWindow) {
	if w == nil {
		return
	}
	if w.view != nil && t.tray != nil && t.tray.ContextMenu() != nil {
		if !w.contextHooked {
			w.view.SetContextMenu(t.tray.ContextMenu()) // single handler inside widget
			w.contextHooked = true
		} else {
			// If the tray rebuilt its menu, update the pointer:
			w.view.SetContextMenu(t.tray.ContextMenu())
		}
	}
	w.SetOnClosed(func(i int) { t.WindowWasClosed(i) })
}

func (t *TrayController) WindowWasClosed(idx int) {
	t.mu.Lock()

	if idx < 0 || idx >= len(t.cfg.Cameras) {
		t.mu.Unlock()
		return
	}
	t.ensureWinsLen()

	// Clear window slot and disable camera in config
	(*t.wins)[idx] = nil

	// If we are quitting the app, *do not* mark disabled and *do not* save here.
	if appQuitting.Load() {
		log.Printf("App is quitting, avoiding saving...\n")
		// Keep the tray checkbox visually in sync anyway.
		if idx < len(t.actions) && t.actions[idx] != nil {
			t.actions[idx].BlockSignals(true)
			t.actions[idx].SetChecked(false)
			t.actions[idx].BlockSignals(false)
		}
		t.mu.Unlock()
		return
	}

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
	t.mu.Unlock()
	rebuildTrayContextMenus()
}
