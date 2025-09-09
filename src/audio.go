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

	"github.com/hajimehoshi/oto/v2"
)

// Global singleton Oto v2 context expected by camera.go
var (
	GlobalAudioContext *oto.Context
	globalMu           sync.Mutex
	globalRate         int
	globalCh           int
)

// InitGlobalAudio initializes the global Oto context once.
// We call this early on the main thread before starting cameras.
func InitGlobalAudio(sampleRate, channels int) error {
	globalMu.Lock()
	defer globalMu.Unlock()

	if GlobalAudioContext != nil {
		// Keep the first created context; Oto v2 mixes internally.
		if globalRate != sampleRate || globalCh != channels {
			log.Printf("audio: keeping existing Oto context %d Hz/%d ch (requested %d/%d)",
				globalRate, globalCh, sampleRate, channels)
		}
		return nil
	}

	ctx, ready, err := oto.NewContext(sampleRate, channels, oto.FormatSignedInt16LE)

	if err != nil {
		return err
	}

	// Consume readiness asynchronously (required on some platforms).
	go func() {
		<-ready // just wait; it's chan struct{}
		log.Printf("audio: context ready")
	}()

	GlobalAudioContext = ctx
	globalRate = sampleRate
	globalCh = channels
	log.Printf("audio: initialized Oto v2 context %d Hz/%d ch", globalRate, globalCh)
	return nil
}
