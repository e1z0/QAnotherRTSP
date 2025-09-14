package main

import (
	"log"

	"github.com/mappu/miqt/qt"
)

// --- add near other config types ---
type Formation struct {
	Name  string          `yaml:"name"`
	Items []FormationItem `yaml:"items"`
}

type FormationItem struct {
	CameraID string `yaml:"camera_id"`
	X        int    `yaml:"x"`
	Y        int    `yaml:"y"`
	Width    int    `yaml:"width"`
	Height   int    `yaml:"height"`
	Visible  bool   `yaml:"visible,omitempty"` // keeps whether the window was open
}

func (t *TrayController) installFormationsMenu(root *qt.QMenu) {
	// Create once
	if t.formMenu == nil {
		t.formMenu = qt.NewQMenu3("Formations")
		t.formSubmenus = map[string]*qt.QMenu{}
	}
	// MOUNT ONLY ONCE per root build, at the exact place you call this.
	if !t.formMenuMounted {
		addAct(root, t.formMenu.MenuAction()) // ← put it in-place now
		t.formMenuMounted = true
	}
	// (Re)build contents
	t.rebuildFormationsList()
}
func (t *TrayController) rebuildFormationsList() {
	t.formMenu.Clear()
	t.formSubmenus = map[string]*qt.QMenu{}

	// "Save current as..."
	if t.formSaveAct == nil {
		t.formSaveAct = qt.NewQAction2("Save current as…")
		t.formSaveAct.OnTriggered(func() { t.saveFormationInteractive() })
	}
	addAct(t.formMenu, t.formSaveAct)
	addSep(t.formMenu)

	if len(t.cfg.Formations) == 0 {
		none := qt.NewQAction2("(No formations yet)")
		none.SetEnabled(false)
		addAct(t.formMenu, none)
		return
	}

	for i := range t.cfg.Formations {
		f := t.cfg.Formations[i] // capture by value

		sub := qt.NewQMenu3(f.Name)
		act := sub.MenuAction()
		act.SetCheckable(true)
		act.SetChecked(t.cfg.LastFormation == f.Name)

		// <<< THIS is the key: clicking the submenu name (opening it) applies formation
		// sub.OnAboutToShow(func() {
		// 	if t.cfg.LastFormation != f.Name {
		// 		t.applyFormation(f)
		// 		t.cfg.LastFormation = f.Name
		// 		_ = SaveConfig()
		// 		t.refreshFormationChecks(f.Name) // update ticks without rebuilding
		// 	}
		// })

		applyA := qt.NewQAction2("Apply")
		applyA.OnTriggered(func() {
			t.applyFormation(f)
			t.cfg.LastFormation = f.Name
			_ = SaveConfig()
			t.refreshFormationChecks(f.Name)
		})

		// Inner actions: overwrite + delete
		saveA := qt.NewQAction2("Save (overwrite)")
		saveA.OnTriggered(func() {
			t.saveFormationFromWindows(f.Name)
			// no rebuild here; list didn’t change
		})

		delA := qt.NewQAction2("Delete")
		delA.OnTriggered(func() {
			t.deleteFormationByName(f.Name)
			t.rebuildFormationsList() // list changed -> rebuild contents only
		})

		sub.AddActions([]*qt.QAction{applyA, saveA, delA})

		addAct(t.formMenu, sub.MenuAction())
		t.formSubmenus[f.Name] = sub
	}
}

func (t *TrayController) applyFormation(f Formation) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.ensureWinsLen()

	want := map[string]FormationItem{}
	for _, it := range f.Items {
		want[it.CameraID] = it
	}

	// 1) Close windows that aren't part of the formation
	for i, w := range *t.wins {
		if w == nil {
			continue
		}
		id := t.cfg.Cameras[i].ID
		if id == "" {
			id = t.cfg.Cameras[i].Name
		}
		if _, ok := want[id]; !ok {
			w.SuppressOnClosedOnce()
			w.Close()
			(*t.wins)[i] = nil
			t.cfg.Cameras[i].Disabled = true
			if i < len(t.actions) && t.actions[i] != nil {
				t.actions[i].BlockSignals(true)
				t.actions[i].SetChecked(false)
				t.actions[i].BlockSignals(false)
			}
		}
	}

	// 2) Open/position all required windows
	for _, it := range f.Items {
		idx := findCameraIndexByID(t.cfg, it.CameraID)
		if idx < 0 {
			continue
		} // camera not found in current config

		// Ensure ON
		t.cfg.Cameras[idx].Disabled = false
		if (*t.wins)[idx] == nil {
			w, err := newCamWindow(t.cfg.Cameras[idx], idx)
			if err != nil {
				log.Printf("formation open %q: %v", it.CameraID, err)
				continue
			}
			(*t.wins)[idx] = w
			t.AttachWindowHooks(idx, w)
		}

		// Place window; avoid spamming geometry saver while we’re placing.
		if w := (*t.wins)[idx]; w != nil && w.win != nil {
			w.suppressSave = true
			w.win.SetGeometry(it.X, it.Y, it.Width, it.Height)
			// turn saving back on next tick
			tm := qt.NewQTimer()
			tm.SetSingleShot(true)
			tm.OnTimeout(func() {
				if w != nil {
					w.suppressSave = false
				}
				tm.DeleteLater()
			})
			tm.Start(0)
			w.win.Show() // ensure visible
			// keep tray checkbox in sync
			if idx < len(t.actions) && t.actions[idx] != nil {
				t.actions[idx].BlockSignals(true)
				t.actions[idx].SetChecked(true)
				t.actions[idx].BlockSignals(false)
			}
		}
	}

	t.cfg.LastFormation = f.Name
	_ = SaveConfig()
}

func (t *TrayController) refreshFormationChecks(active string) {
	if t.formSubmenus == nil {
		return
	}
	for name, m := range t.formSubmenus {
		if m == nil {
			continue
		}
		m.MenuAction().SetChecked(name == active)
	}
}

func (t *TrayController) saveFormationInteractive() {
	name, ok := promptText("Save Formation", "Name this formation:")
	if !ok || name == "" {
		return
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	t.ensureWinsLen()

	var items []FormationItem
	for i, w := range *t.wins {
		if w == nil || w.win == nil {
			continue
		}
		id := t.cfg.Cameras[i].ID
		if id == "" {
			id = t.cfg.Cameras[i].Name
		}
		g := w.win.Geometry()
		items = append(items, FormationItem{
			CameraID: id,
			X:        g.X(), Y: g.Y(), Width: g.Width(), Height: g.Height(),
			Visible: true,
		})
	}

	// upsert by name
	replaced := false
	for i := range t.cfg.Formations {
		if t.cfg.Formations[i].Name == name {
			t.cfg.Formations[i].Items = items
			replaced = true
			break
		}
	}
	if !replaced {
		t.cfg.Formations = append(t.cfg.Formations, Formation{Name: name, Items: items})
	}
	t.cfg.LastFormation = name
	_ = SaveConfig()

	// refresh menu now that list changed
	// (assuming you have the root menu handy; if not, pass it through or rebuild tray)
	if t.tray != nil && t.tray.ContextMenu() != nil {
		t.installFormationsMenu(t.tray.ContextMenu())
	}
}

func (t *TrayController) saveFormationFromWindows(name string) {
	if name == "" {
		return
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	t.ensureWinsLen()

	var items []FormationItem
	for i, w := range *t.wins {
		if w == nil || w.win == nil {
			continue
		}
		id := t.cfg.Cameras[i].ID
		if id == "" {
			id = t.cfg.Cameras[i].Name
		}
		g := w.win.Geometry()
		items = append(items, FormationItem{
			CameraID: id,
			X:        g.X(), Y: g.Y(), Width: g.Width(), Height: g.Height(),
			Visible: true,
		})
	}

	// overwrite existing by name
	for i := range t.cfg.Formations {
		if t.cfg.Formations[i].Name == name {
			t.cfg.Formations[i].Items = items
			_ = SaveConfig()
			return
		}
	}
	// if not found, append (safety)
	t.cfg.Formations = append(t.cfg.Formations, Formation{Name: name, Items: items})
	_ = SaveConfig()
}

func (t *TrayController) deleteFormationByName(name string) {
	if name == "" {
		return
	}
	out := t.cfg.Formations[:0]
	for _, f := range t.cfg.Formations {
		if f.Name != name {
			out = append(out, f)
		}
	}
	t.cfg.Formations = out
	if t.cfg.LastFormation == name {
		t.cfg.LastFormation = ""
	}
	_ = SaveConfig()

	if t.tray != nil && t.tray.ContextMenu() != nil {
		t.installFormationsMenu(t.tray.ContextMenu())
	}
}
