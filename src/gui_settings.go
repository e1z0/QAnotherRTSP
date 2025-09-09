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

	"github.com/mappu/miqt/qt"
)

/*
 app setings unit
*/

// SettingsDialog owns the modal settings UI and a working copy of cameras.
type SettingsDialog struct {
	dlg  *qt.QDialog
	tabs *qt.QTabWidget
	// Cameras tab
	camPage         *qt.QWidget
	list            *qt.QListWidget
	btnAdd, btnEdit *qt.QPushButton
	btnRemove       *qt.QPushButton
	// Footer
	btnCancel, btnSave *qt.QPushButton
	noWinTitlesCh      *qt.QCheckBox
	snapCh             *qt.QCheckBox
	alwaysOnTopAllCh   *qt.QCheckBox
	activateOnTrayCh   *qt.QCheckBox
	activateOnWinCh    *qt.QCheckBox
	cams               []CameraConfig
}

// ShowSettingsDialog opens the modal dialog.
func ShowSettingsDialog(parent *qt.QWidget) {
	s := newSettingsDialog(parent)
	_ = s.dlg.Exec()
}

func newSettingsDialog(parent *qt.QWidget) *SettingsDialog {
	d := &SettingsDialog{
		dlg:  qt.NewQDialog(parent),
		tabs: qt.NewQTabWidget(nil),
	}
	d.dlg.SetWindowTitle("Settings")

	// Working copy of cameras
	configMu.Lock()
	d.cams = append(d.cams, globalConfig.Cameras...)
	configMu.Unlock()

	// ===== Cameras tab =====
	d.camPage = qt.NewQWidget(parent)
	d.list = qt.NewQListWidget(parent)
	d.btnAdd = qt.NewQPushButton5("Add", nil)
	d.btnEdit = qt.NewQPushButton5("Edit", nil)
	d.btnRemove = qt.NewQPushButton5("Remove", nil)

	row := qt.NewQHBoxLayout(nil)
	row.AddWidget(d.btnAdd.QWidget)
	row.AddWidget(d.btnEdit.QWidget)
	row.AddWidget(d.btnRemove.QWidget)
	row.AddStretch()

	// Put list and row into the Cameras page layout so it stretches with the dialog
	camLayout := qt.NewQVBoxLayout(nil)
	camLayout.AddWidget(d.list.QWidget)
	camLayout.AddLayout(row.QLayout)
	d.camPage.SetLayout(camLayout.QLayout)

	// ===== Settings tab =====
	settingsPage := qt.NewQWidget(nil)
	settingsForm := qt.NewQFormLayout(nil)
	// camera windows in frameless mode
	d.noWinTitlesCh = qt.NewQCheckBox4("Camera windows without titles", nil)
	d.noWinTitlesCh.SetChecked(globalConfig.NoWindowsTitles)
	settingsForm.AddRow3("", d.noWinTitlesCh.QWidget)
	// enable/disable snapping
	d.snapCh = qt.NewQCheckBox4("Enable window snapping (glue/stack)", nil)
	d.snapCh.SetChecked(globalConfig.SnapEnabled)
	settingsForm.AddRow3("", d.snapCh.QWidget)
	// camera windows always on top
	d.alwaysOnTopAllCh = qt.NewQCheckBox4("All camera windows always on top", nil)
	d.alwaysOnTopAllCh.SetChecked(globalConfig.AlwaysOnTopAll)
	settingsForm.AddRow3("", d.alwaysOnTopAllCh.QWidget)
	// activate windows on tray click
	d.activateOnTrayCh = qt.NewQCheckBox4("Activate camera windows on tray click", nil)
	d.activateOnTrayCh.SetChecked(globalConfig.ActiveOnTray)
	settingsForm.AddRow3("", d.activateOnTrayCh.QWidget)
	// activate all camera windows on one window click
	d.activateOnWinCh = qt.NewQCheckBox4("Activate all cameras on one camera click", nil)
	d.activateOnWinCh.SetChecked(globalConfig.ActiveOnWin)
	settingsForm.AddRow3("", d.activateOnWinCh.QWidget)

	settingsPage.SetLayout(settingsForm.QLayout)

	// ===== Advanced tab (scaffold) =====
	advancedPage := qt.NewQWidget(nil)
	advancedForm := qt.NewQFormLayout(nil)
	// TODO: Add advanced options here
	advancedPage.SetLayout(advancedForm.QLayout)

	// Add tabs (Cameras, Settings, Advanced)
	_ = d.tabs.AddTab(d.camPage, "Cameras")
	_ = d.tabs.AddTab(settingsPage, "Settings")
	_ = d.tabs.AddTab(advancedPage, "Advanced")

	// ===== Footer (Save / Cancel) =====
	d.btnSave = qt.NewQPushButton5("Save", nil)
	d.btnCancel = qt.NewQPushButton5("Cancel", nil)
	bottom := qt.NewQHBoxLayout(nil)
	bottom.AddStretch()
	bottom.AddWidget(d.btnSave.QWidget)
	bottom.AddWidget(d.btnCancel.QWidget)

	// ===== Dialog main layout =====
	mainV := qt.NewQVBoxLayout(nil)
	mainV.AddWidget(d.tabs.QWidget) // stretches to corners
	mainV.AddLayout(bottom.QLayout) // footer stays anchored
	d.dlg.SetLayout(mainV.QLayout)

	// Populate Cameras list
	d.refreshList()

	// Wire buttons
	d.btnAdd.OnClicked(func() { d.onAdd() })
	d.btnEdit.OnClicked(func() { d.onEdit() })
	d.btnRemove.OnClicked(func() { d.onRemove() })
	d.btnSave.OnClicked(func() { d.onSave() })
	d.btnCancel.OnClicked(func() { d.dlg.Reject() })

	// Enable/disable edit/remove based on selection
	updateButtons := func() {
		has := d.list.CurrentRow() >= 0
		d.btnEdit.SetEnabled(has)
		d.btnRemove.SetEnabled(has)
	}
	d.list.OnCurrentRowChanged(func(int) { updateButtons() })
	updateButtons()

	// Double-click to edit
	d.list.OnItemDoubleClicked(func(*qt.QListWidgetItem) { d.onEdit() })

	d.dlg.Resize(560, 420)
	d.dlg.Show()
	d.dlg.Raise()
	d.dlg.ActivateWindow()
	d.dlg.SetFocus()
	return d
}

func (d *SettingsDialog) refreshList() {
	d.list.Clear()
	for i := range d.cams {
		c := d.cams[i]
		title := c.Name
		if title == "" {
			title = c.URL
		}
		item := qt.NewQListWidgetItem7(title, d.list)
		_ = item // keep reference alive per miqt semantics
	}
}

// --- Button handlers ---

func (d *SettingsDialog) onAdd() {
	var c CameraConfig
	if ok := editCameraDialog(d.dlg.QWidget, &c); ok {
		// Working copy
		d.cams = append(d.cams, c)
		d.refreshList()
		newIdx := len(d.cams) - 1
		d.list.SetCurrentRow(newIdx)

		// Mirror to runtime config
		configMu.Lock()
		globalConfig.Cameras = append(globalConfig.Cameras, c)
		configMu.Unlock()
		// Make sure wins has a slot for the new index
		if len(wins) < len(globalConfig.Cameras) {
			wins = append(wins, make([]*CamWindow, len(globalConfig.Cameras)-len(wins))...)
		}
		if DEBUG {
			log.Printf("new idx: %d  total cams: %d  wins: %d  total in config: %d",
				newIdx, len(d.cams), len(wins), len(globalConfig.Cameras))
		}
		if !c.Disabled {
			w, err := newCamWindow(c, newIdx) // <-- use the local 'c'; its index is newIdx
			if err != nil {
				log.Printf("open cam %q: %v", safeCamTitle(c), err)
				configMu.Lock()
				globalConfig.Cameras[newIdx].Disabled = true
				configMu.Unlock()
				return
			}
			wins[newIdx] = w
			tray.rebuild()
			tray.AttachWindowHooks(newIdx, w)

		}
	}
}

func (d *SettingsDialog) onEdit() {
	row := d.list.CurrentRow()
	if row < 0 || row >= len(d.cams) {
		return
	}

	edited := d.cams[row]
	if ok := editCameraDialog(d.dlg.QWidget, &edited); ok {
		d.cams[row] = edited
		id := d.cams[row].ID
		d.refreshList()
		d.list.SetCurrentRow(row)

		configMu.Lock()
		globalConfig.Cameras[row] = edited
		configMu.Unlock()
		if !edited.Disabled {
			for _, w := range wins {
				if w == nil {
					continue
				}
				if w.cfg.ID == id {
					w.RestartWith(edited, "re-open")
				}
			}
		}
		if tray != nil {
			tray.mu.Lock()
			// keep tray controller's config in sync with dialog working copy
			tray.cfg.Cameras = append([]CameraConfig(nil), d.cams...)
			tray.mu.Unlock()
			log.Printf("rebuilding the tray...")
			tray.rebuild()
		}
	}
}

func (d *SettingsDialog) onRemove() {
	row := d.list.CurrentRow()
	if row < 0 || row >= len(d.cams) {
		return
	}

	mb := qt.NewQMessageBox(d.dlg.QWidget)
	mb.SetWindowTitle("Confirm delete")
	mb.SetIcon(qt.QMessageBox__Question)
	name := d.cams[row].Name
	id := d.cams[row].ID
	if name == "" {
		name = d.cams[row].URL
	}
	mb.SetText(fmt.Sprintf("Delete camera:\n\n%s\n\nAre you sure?", name))
	mb.SetStandardButtons(qt.QMessageBox__Yes | qt.QMessageBox__No)
	if mb.Exec() != int(qt.QMessageBox__Yes) {
		return
	}

	// Remove
	d.cams = append(d.cams[:row], d.cams[row+1:]...)
	d.refreshList()
	if row >= len(d.cams) {
		row = len(d.cams) - 1
	}
	d.list.SetCurrentRow(row)

	// Remove from global config (in-memory)
	configMu.Lock()
	if row < len(globalConfig.Cameras) {
		globalConfig.Cameras = append(globalConfig.Cameras[:row], globalConfig.Cameras[row+1:]...)
	}
	configMu.Unlock()

	for _, w := range wins {
		if w == nil {
			continue
		}
		if w.cfg.ID == id {
			w.win.Close()
		}
	}

	if tray != nil {
		tray.mu.Lock()
		// keep tray controller's config in sync with dialog working copy
		tray.cfg.Cameras = append([]CameraConfig(nil), d.cams...)
		tray.mu.Unlock()
		tray.rebuild()
	}

}

func (d *SettingsDialog) onSave() {
	// Persist the working copy to global config + YAML
	configMu.Lock()
	globalConfig.Cameras = make([]CameraConfig, len(d.cams))
	copy(globalConfig.Cameras, d.cams)
	globalConfig.NoWindowsTitles = d.noWinTitlesCh.IsChecked()
	globalConfig.SnapEnabled = d.snapCh.IsChecked()
	globalConfig.AlwaysOnTopAll = d.alwaysOnTopAllCh.IsChecked()
	globalConfig.ActiveOnTray = d.activateOnTrayCh.IsChecked()
	globalConfig.ActiveOnWin = d.activateOnWinCh.IsChecked()
	configMu.Unlock()

	// Apply immediately to open windows (frameless ↔ titled)
	for i, w := range wins {
		if w == nil || w.win == nil {
			continue
		}
		// Global override wins; otherwise fall back to per-camera flag
		atop := globalConfig.AlwaysOnTopAll
		if !atop && i < len(globalConfig.Cameras) {
			atop = globalConfig.Cameras[i].AlwaysOnTop
		}
		w.win.SetWindowFlag2(qt.WindowStaysOnTopHint, atop)
		// Toggle frameless flag
		w.win.SetWindowFlag2(qt.FramelessWindowHint, globalConfig.NoWindowsTitles)
		// toggle the overlay label
		if w.view != nil {
			w.view.SetOverlayTitle(safeCamTitle(w.cfg), globalConfig.NoWindowsTitles)
		}

		// If titles are visible again, make sure the title is set
		if !globalConfig.NoWindowsTitles {
			title := safeCamTitle(w.cfg)
			w.win.SetWindowTitle("Cam: " + title)
		}
		// Re-polish to show changes right away
		w.win.Show()
	}

	if err := SaveConfig(); err != nil {
		log.Printf("Save settings failed: %v", err)
		mb := qt.NewQMessageBox(d.dlg.QWidget)
		mb.SetWindowTitle("Error")
		mb.SetIcon(qt.QMessageBox__Critical)
		mb.SetText(fmt.Sprintf("Failed to save settings:\n\n%v", err))
		mb.SetStandardButtons(qt.QMessageBox__Ok)
		mb.Exec()
		return
	}

	d.dlg.Accept()
}

// --- Add/Edit dialog ---

func editCameraDialog(parent *qt.QWidget, c *CameraConfig) bool {
	dlg := qt.NewQDialog(parent)
	dlg.SetWindowTitle("Camera")

	form := qt.NewQFormLayout(nil)

	edName := qt.NewQLineEdit(nil)
	edURL := qt.NewQLineEdit(nil)
	chRTSP := qt.NewQCheckBox4("Use RTSP over TCP", nil)
	chTop := qt.NewQCheckBox4("Always on top", nil)
	chMute := qt.NewQCheckBox4("Mute audio", nil)
	// NEW: Stretch & HwAccel
	chStretch := qt.NewQCheckBox4("Stretch video to window", nil)
	cbHw := qt.NewQComboBox(nil)
	// Populate combo
	cbHw.AddItem("none")
	cbHw.AddItem("videotoolbox")
	cbHw.AddItem("vaapi")
	cbHw.AddItem("nvdec")
	edFF := qt.NewQLineEdit(nil)

	hwaccel := c.HwAccel
	if hwaccel == "" {
		hwaccel = "none"
	}

	// initial values
	edName.SetText(c.Name)
	edURL.SetText(c.URL)
	chRTSP.SetChecked(c.RTSPTCP)
	chTop.SetChecked(c.AlwaysOnTop)
	chMute.SetChecked(c.Mute)
	chStretch.SetChecked(c.Stretch)
	idx := cbHw.FindText2(hwaccel, qt.MatchFixedString)
	if idx >= 0 {
		cbHw.SetCurrentIndex(idx)
	}
	edFF.SetText(c.FFmpegParams) // may be empty

	form.AddRow3("Name:", edName.QWidget)
	form.AddRow3("URL:", edURL.QWidget)
	form.AddRow3("", chRTSP.QWidget)
	form.AddRow3("", chTop.QWidget)
	form.AddRow3("", chMute.QWidget)
	form.AddRow3("", chStretch.QWidget)
	form.AddRow3("HW acceleration:", cbHw.QWidget)
	form.AddRow3("FFmpeg params:", edFF.QWidget)

	// Make text inputs + combo expand
	setExpand := func(w *qt.QWidget) {
		w.SetSizePolicy2(qt.QSizePolicy__Expanding, qt.QSizePolicy__Fixed)
		// a small min width helps initial layout look less cramped
		w.SetMinimumWidth(420)
	}
	setExpand(edName.QWidget)
	setExpand(edURL.QWidget)
	setExpand(edFF.QWidget)
	setExpand(cbHw.QWidget)

	btnOk := qt.NewQPushButton5("OK", nil)
	btnCancel := qt.NewQPushButton5("Cancel", nil)

	// Footer row (right aligned)
	btns := qt.NewQHBoxLayout(nil)
	btns.AddStretch()
	btns.AddWidget(btnOk.QWidget)
	btns.AddWidget(btnCancel.QWidget)

	// Main vertical layout
	v := qt.NewQVBoxLayout(nil)
	v.AddLayout(form.QLayout)
	v.AddLayout(btns.QLayout)
	dlg.SetLayout(v.QLayout)

	// Simple validation: URL required
	valid := func() bool {
		return edURL.Text() != ""
	}
	btnOk.SetEnabled(valid())
	edURL.OnTextChanged(func(string) { btnOk.SetEnabled(valid()) })

	btnOk.OnClicked(func() {
		c.Name = edName.Text()
		c.URL = SanitizeString(edURL.Text())
		c.RTSPTCP = chRTSP.IsChecked()
		c.AlwaysOnTop = chTop.IsChecked()
		c.Mute = chMute.IsChecked()
		c.Stretch = chStretch.IsChecked()
		c.HwAccel = cbHw.CurrentText()
		c.FFmpegParams = edFF.Text()
		dlg.Accept()
	})
	btnCancel.OnClicked(func() { dlg.Reject() })

	dlg.Resize(560, 0)
	return dlg.Exec() == int(qt.QDialog__Accepted)
}
