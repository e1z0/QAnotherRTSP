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
	"runtime"
	"time"

	"github.com/mappu/miqt/qt"
)

type AboutInfo struct {
	AppName     string
	Version     string
	Build       string
	Lines       string
	HomepageURL string
	SupportURL  string
	LicenseText string
	CreditsHTML string
	Icon        *qt.QIcon
}

// ShowAboutDialog creates and runs a modal About dialog
func ShowAboutDialog(parent *qt.QWidget, info AboutInfo) {
	d := qt.NewQDialog(parent)
	d.SetWindowTitle(fmt.Sprintf("About %s", nz(info.AppName, "Application")))
	d.SetModal(true)
	// use the 2-suffixed variant like in VideoWidget for attributes
	d.SetAttribute2(qt.WA_DeleteOnClose, true)

	// Root layout
	root := qt.NewQVBoxLayout(nil)
	root.SetContentsMargins(18, 18, 18, 18)
	root.SetSpacing(12)
	d.SetLayout(root.QLayout)

	// Header: icon + title/version
	header := qt.NewQHBoxLayout(nil)
	header.SetSpacing(12)

	iconLbl := qt.NewQLabel(nil)
	iconLbl.SetFixedSize2(64, 64)
	// resolve icon
	icon := info.Icon
	if icon == nil {
		icon = qt.QApplication_WindowIcon()
	}
	if icon != nil {
		pm := icon.Pixmap2(64, 64)
		iconLbl.SetPixmap(pm)
	}
	header.AddWidget(iconLbl.QWidget)

	titleBox := qt.NewQVBoxLayout(nil)
	// header
	appTitle := qt.NewQLabel(nil)
	appTitle.SetTextFormat(qt.RichText)
	appTitle.SetText(fmt.Sprintf("<b style='font-size:18px'>%s</b>", nz(info.AppName, "Application")))
	appTitle.SetTextInteractionFlags(qt.TextSelectableByMouse)
	titleBox.AddWidget(appTitle.QWidget)
	version := qt.NewQLabel(nil)
	version.SetText("Version " + nz(info.Version, "0.0.0"))
	version.SetTextInteractionFlags(qt.TextSelectableByMouse)
	titleBox.AddWidget(version.QWidget)

	header.AddLayout(titleBox.QLayout)
	header.AddStretch()
	root.AddLayout(header.QLayout)

	// Links row
	if info.HomepageURL != "" || info.SupportURL != "" {
		linkLbl := qt.NewQLabel(nil)
		linkLbl.SetTextFormat(qt.RichText)
		linkLbl.SetOpenExternalLinks(true)
		linkLbl.SetText(linkRowHTML(info.HomepageURL, info.SupportURL))
		root.AddWidget(linkLbl.QWidget)
	}

	// About text (QTextBrowser)
	sysInfo := qt.NewQTextBrowser(nil)
	sysInfo.SetOpenExternalLinks(true)
	sysInfo.SetReadOnly(true)
	sysInfo.SetMinimumHeight(160)
	sysInfo.SetHtml(aboutHTML(info))
	root.AddWidget(sysInfo.QWidget)

	// Buttons
	btnRow := qt.NewQHBoxLayout(nil)
	btnRow.AddStretch()

	btnCopy := qt.NewQPushButton5("Copy build info", nil)
	btnRow.AddWidget(btnCopy.QWidget)

	btnHomepage := qt.NewQPushButton5("Open homepage", nil)
	btnHomepage.SetEnabled(info.HomepageURL != "")
	btnRow.AddWidget(btnHomepage.QWidget)

	btnCredits := qt.NewQPushButton5("Credits…", nil)
	btnCredits.SetEnabled(info.CreditsHTML != "")
	btnRow.AddWidget(btnCredits.QWidget)

	btnLicense := qt.NewQPushButton5("License…", nil)
	btnLicense.SetEnabled(info.LicenseText != "")
	btnRow.AddWidget(btnLicense.QWidget)

	btnOk := qt.NewQPushButton5("OK", nil)
	btnOk.SetDefault(true)
	btnRow.AddWidget(btnOk.QWidget)

	root.AddLayout(btnRow.QLayout)

	// Signals
	btnCredits.OnClicked(func() { showCreditsDialog(d.QWidget, info) })

	btnOk.OnClicked(func() {
		d.Accept()
	})
	btnCopy.OnClicked(func() {
		clip := qt.QGuiApplication_Clipboard()
		if clip != nil {
			clip.SetText2(fmt.Sprintf("%s\nVersion: %s\n%s\nGo: %s %s/%s",
				nz(info.AppName, "Application"),
				nz(info.Version, "0.0.0"),
				nz(info.Build, defaultBuildString()),
				runtime.Version(), runtime.GOOS, runtime.GOARCH),
				qt.QClipboard__Clipboard)
		}
	})
	btnHomepage.OnClicked(func() {
		if info.HomepageURL != "" {
			qt.QDesktopServices_OpenUrl(qt.NewQUrl4(info.HomepageURL, qt.QUrl__TolerantMode))
		}
	})
	btnLicense.OnClicked(func() { showLicenseDialog(d.QWidget, info) })

	// Sizing & placement
	d.Resize(560, 420)
	d.Exec()
}

func showCreditsDialog(parent *qt.QWidget, info AboutInfo) {
	cd := qt.NewQDialog(parent)
	cd.SetWindowTitle("Credits")
	cd.SetModal(true)

	v := qt.NewQVBoxLayout(nil)
	tb := qt.NewQTextBrowser(nil)
	tb.SetOpenExternalLinks(true)
	tb.SetReadOnly(true)
	if info.CreditsHTML != "" {
		tb.SetHtml(info.CreditsHTML)
	} else {
		tb.SetHtml("<p><i>No credits provided.</i></p>")
	}
	v.AddWidget(tb.QWidget)

	row := qt.NewQHBoxLayout(nil)
	row.AddStretch()
	btnClose := qt.NewQPushButton5("Close", nil)
	row.AddWidget(btnClose.QWidget)
	v.AddLayout(row.QLayout)

	cd.SetLayout(v.QLayout)
	btnClose.OnClicked(func() { cd.Accept() })
	cd.Resize(620, 480)
	cd.Exec()
}

func showLicenseDialog(parent *qt.QWidget, info AboutInfo) {
	ld := qt.NewQDialog(parent)
	ld.SetWindowTitle("License")
	ld.SetModal(true)

	v := qt.NewQVBoxLayout(nil)
	ed := qt.NewQPlainTextEdit(nil)
	ed.SetReadOnly(true)
	ed.SetPlainText(nz(info.LicenseText, "No license text provided."))
	v.AddWidget(ed.QWidget)

	row := qt.NewQHBoxLayout(nil)
	row.AddStretch()
	btnClose := qt.NewQPushButton5("Close", nil)
	row.AddWidget(btnClose.QWidget)
	v.AddLayout(row.QLayout)

	ld.SetLayout(v.QLayout)
	btnClose.OnClicked(func() { ld.Accept() })
	ld.Resize(620, 480)
	ld.Exec()
}

// --- helpers ---

func linkRowHTML(home, support string) string {
	html := "<div style='margin-top:2px'>"
	first := true
	if home != "" {
		html += fmt.Sprintf(`<a href="%s">Homepage</a>`, home)
		first = false
	}
	if support != "" {
		if !first {
			html += " &nbsp;•&nbsp; "
		}
		html += fmt.Sprintf(`<a href="%s">Support</a>`, support)
	}
	html += "</div>"
	return html
}

func aboutHTML(info AboutInfo) string {
	return fmt.Sprintf(`
<style>
 ul{margin:0 0 0 1.1em; padding:0}
 li{margin:0.2em 0}
</style>
<p><b>%s</b> — a cross-platform video surveillance application.</p>
<ul>
  <li><b>Version:</b> %s</li>
  <li><b>Build:</b> %s</li>
  <li><b>Go runtime:</b> %s</li>
  <li><b>Platform:</b> %s/%s</li>
  <li><b>Lines of code:</b> %s</li>
</ul>
<p>© %d Justinas K (e1z0@icloud.com)</p>
`, nz(info.AppName, "Application"),
		nz(info.Version, "0.0.0"),
		nz(info.Build, defaultBuildString()),
		runtime.Version(),
		runtime.GOOS, runtime.GOARCH, info.Lines,
		time.Now().Year())
}

func defaultBuildString() string { return "Build: unknown" }

func nz(s, alt string) string {
	if s == "" {
		return alt
	}
	return s
}

// LicenseText is the full license string used in the About dialog.
const LicenseText = `
QAnotherRTSP is free software, licensed under the GNU GPL‑3.0‑or‑later.

© 2025 e1z0 <e1z0@icloud.com>

This program is free software: you can redistribute it and/or modify it under
the terms of the GNU General Public License as published by the Free Software
Foundation, either version 3 of the License, or (at your option) any later
version.

This program is distributed in the hope that it will be useful, but WITHOUT ANY
WARRANTY; without even the implied warranty of MERCHANTABILITY or FITNESS FOR A
PARTICULAR PURPOSE. See the GNU General Public License for more details.

You should have received a copy of the GNU General Public License along with
this program. If not, see https://www.gnu.org/licenses/.

Third‑party components used by this application include Qt, MIQT and FFmpeg. Their
licenses and source offers are provided in the bundled NOTICE.md.
`
