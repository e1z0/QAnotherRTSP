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
	"io"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"sync"

	"gopkg.in/yaml.v2"
)

var appName = "another-rtsp"
var globalConfig AppConfig
var env Environment
var configMu sync.Mutex

type Environment struct {
	configDir    string // configuration directory ~/.config/another-rtsp
	settingsFile string // configuration path ~/.config/another-rtsp/settings.ini
	homeDir      string // home directory ~/
	appPath      string // application directory where the binary lies
	tmpDir       string // OS Temp directory
	appDebugLog  string // app debug.log
	os           string // current operating system
}

type AppConfig struct {
	Cameras         []CameraConfig `yaml:"cameras"`
	NoWindowsTitles bool           `yaml:"nowindowstitles,omitempty"`
	SnapEnabled     bool           `yaml:"snap_enabled,omitempty"`      //enable/disable snapping+glue
	AlwaysOnTopAll  bool           `yaml:"always_on_top_all,omitempty"` //all camera windows are always on top
	ActiveOnTray    bool           `yaml:"activate_on_tray,omitempty"`
	ActiveOnWin     bool           `yaml:"activate_in_win,omitempty"`
	Formations      []Formation    `yaml:"formations,omitempty"`
	LastFormation   string         `yaml:"last_formation,omitempty"`
	// overlays
	HealthChip   bool `yaml:"health_chip,omitempty"` // show 0–5 health chip on each camera
	ShowFPS      bool `yaml:"show_fps,omitempty"`
	ShowBitrate  bool `yaml:"show_bitrate,omitempty"`
	ShowDrops    bool `yaml:"show_drops,omitempty"`
	ShowCPUUsage bool `yaml:"show_cpu,omitempty"` // overlay "CPU: xx%"
}

type CameraConfig struct {
	ID          string `yaml:"id,omitempty"`       // camera uuid
	Name        string `yaml:"name"`               // camera name
	Disabled    bool   `yaml:"disabled,omitempty"` // if camera is disabled
	URL         string `yaml:"url"`                // camera url, rtsp://...
	RTSPTCP     bool   `yaml:"rtsp_tcp"`           // enable tcp for rtsp?
	Caching     int    `yaml:"caching_ms"`         // network caching (ms)
	X           int    `yaml:"x,omitempty"`        // camera window position X on screen
	Y           int    `yaml:"y,omitempty"`        // camera window position Y on screen
	Width       int    `yaml:"width"`              // camera window width
	Height      int    `yaml:"height"`             // camera window height
	AlwaysOnTop bool   `yaml:"always_on_top"`      // camera windows are always on top
	Mute        bool   `yaml:"mute,omitempty"`     // mute camera
	Stretch     bool   `yaml:"stretch,omitempty"`  // when true, fill the widget and allow stretching (no aspect lock)

	FFmpegParams string `yaml:"ffmpeg_params,omitempty"` // ffmpeg parameters

	Volume    *int   `yaml:"volume,omitempty"`     // 0..100 not implemented yet
	Probesize int64  `yaml:"probesize,omitempty"`  // probesize param (bytes)
	AnalyzeUS int64  `yaml:"analyze_us,omitempty"` // analyze (microseconds)
	Threads   int    `yaml:"threads,omitempty"`    // threads count per stream, 0=auto
	HwAccel   string `yaml:"hwaccel,omitempty"`    // "none","videotoolbox","vaapi","nvdec" (not wired here)
}

func initlog() {
	home, err := os.UserHomeDir()
	if err != nil {
		log.Printf("Unable to retrieve home directory: %s\n", err)
		return
	}
	// create directory if it does not exist
	dir := filepath.Join(home, ".config", appName)
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		os.MkdirAll(dir, 0755)
	}

	// Open the log file
	file, err := os.OpenFile(filepath.Join(dir, "debug.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0666)
	if err != nil {
		log.Fatal(err)
		return
	}
	// we always write to log file; if DEBUG=true we write to stdout too)
	if debugging == "true" {
		DEBUG = true
		log.SetOutput(io.MultiWriter(file, os.Stdout))
	} else {
		log.SetOutput(io.MultiWriter(file))
	}
	log.SetFlags(log.LstdFlags | log.Lshortfile)
}

func InitializeEnvironment() {
	// initialize the logging
	initlog()
	// gather all required directories
	log.Printf("App Path: %s\n", appPath())
	log.Printf("Initializing environment...")
	homeDir, err := os.UserHomeDir()
	if err != nil {
		log.Printf("Unable to determine the user home folder: %s\n", err)
	}
	configDir := filepath.Join(homeDir, ".config", appName)
	settingsFile := filepath.Join(configDir, "settings.yml")
	environ := Environment{
		configDir:    configDir,
		settingsFile: settingsFile,
		homeDir:      homeDir,
		appPath:      appPath(),
		tmpDir:       os.TempDir(),
		appDebugLog:  filepath.Join(configDir, "debug.log"),
		os:           GetOS(),
	}
	env = environ
}

// return app path
func appPath() string {
	exePath, err := os.Executable()
	if err != nil {
		return ""
	}

	// Resolve any symlinks and clean path
	realPath, err := filepath.EvalSymlinks(exePath)
	if err != nil {
		return ""
	}
	return filepath.Dir(realPath)
}

func GetOS() string {
	return runtime.GOOS
}

// ensure IDs exist
func ensureCameraIDs(cs []CameraConfig) {
	for i := range cs {
		if cs[i].ID == "" {
			cs[i].ID = genID()
		}
	}
}

// UpdateCameraGeometry updates a camera's saved X/Y/Width/Height and persists the YAML.
// key: usually camera ID; if empty/unique-if not set, pass the Name.
// Returns error if writing to disk fails (update in memory still occurs).
func UpdateCameraGeometry(key string, x, y, w, h int) error {
	configMu.Lock()
	defer configMu.Unlock()

	// Update in-memory config
	idx := -1
	for i := range globalConfig.Cameras {
		c := &globalConfig.Cameras[i]
		if (c.ID != "" && c.ID == key) || (c.ID == "" && c.Name == key) || (key == c.URL) {
			idx = i
			break
		}
	}
	if idx >= 0 {
		c := &globalConfig.Cameras[idx]
		c.X = x
		c.Y = y
		c.Width = w
		c.Height = h
	}

	// atomic write: write to tmp then rename
	tmp := env.settingsFile + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	enc := yaml.NewEncoder(f)

	if err := enc.Encode(&globalConfig); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, env.settingsFile); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// load app configuration
func loadConfig(path string) (AppConfig, error) {
	var cfg AppConfig
	b, err := os.ReadFile(path)
	if err != nil {
		return cfg, err
	}
	if err := yaml.Unmarshal(b, &cfg); err != nil {
		return cfg, err
	}
	return cfg, nil
}

// save app configuration
func SaveConfig() error {
	configMu.Lock()
	defer configMu.Unlock()

	log.Printf("Saving config to %s\n", env.settingsFile)

	tmp := env.settingsFile + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	enc := yaml.NewEncoder(f)

	if err := enc.Encode(&globalConfig); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, env.settingsFile)
}
