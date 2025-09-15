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
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	astiav "github.com/asticode/go-astiav"
	"github.com/hajimehoshi/oto/v2"
)

//
// ===============================
// Small, threadsafe BGRA frameBuf
// ===============================
//
// videowidget.go reads the latest frame from here.
// We store frames as *tightly packed* BGRA (w*4).
//

type frameBuf struct {
	mu  sync.RWMutex
	seq uint64
	w   int
	h   int
	b   []byte
}

func (f *frameBuf) put(w, h int, src []byte) uint64 {
	f.mu.Lock()
	defer f.mu.Unlock()

	n := w * h * 4
	if cap(f.b) < n {
		f.b = make([]byte, n)
	} else {
		f.b = f.b[:n]
	}
	copy(f.b, src)

	f.w = w
	f.h = h
	return atomic.AddUint64(&f.seq, 1)
}

// get returns (seq, w, h, data). If seq==0 there is no frame yet.
func (f *frameBuf) get() (uint64, int, int, []byte) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return atomic.LoadUint64(&f.seq), f.w, f.h, f.b
}

//
// ==================================
// Universal BGRA converter (swscale)
// ==================================
//
// We ALWAYS run decoded frames through FFmpeg’s software scaler
// to BGRA. That way we never touch Y/U/V planes from Go.
//

type bgraScaler struct {
	ssc        *astiav.SoftwareScaleContext
	dst        *astiav.Frame
	srcW, srcH int
	srcPix     astiav.PixelFormat
	dstW, dstH int
}

func (s *bgraScaler) close() {
	if s.dst != nil {
		s.dst.Free()
		s.dst = nil
	}
	if s.ssc != nil {
		s.ssc.Free()
		s.ssc = nil
	}
}

func (s *bgraScaler) ensure(src *astiav.Frame) error {
	sw, sh := src.Width(), src.Height()
	sp := src.PixelFormat()

	if s.ssc != nil && sw == s.srcW && sh == s.srcH && sp == s.srcPix {
		return nil
	}

	// Free existing
	s.close()

	// Destination: same size, BGRA
	dw, dh := sw, sh
	flags := astiav.NewSoftwareScaleContextFlags() // default (bilinear)
	ssc, err := astiav.CreateSoftwareScaleContext(
		sw, sh, sp,
		dw, dh, astiav.PixelFormatBgra,
		flags,
	)
	if err != nil {
		return fmt.Errorf("CreateSoftwareScaleContext(%dx%d %v -> BGRA): %w", sw, sh, sp, err)
	}

	dst := astiav.AllocFrame()
	dst.SetWidth(dw)
	dst.SetHeight(dh)
	dst.SetPixelFormat(astiav.PixelFormatBgra)

	// allocate buffers for dst
	if err := dst.AllocBuffer(1); err != nil {
		dst.Free()
		ssc.Free()
		return fmt.Errorf("dst.AllocBuffer: %w", err)
	}

	s.ssc = ssc
	s.dst = dst
	s.srcW, s.srcH, s.srcPix = sw, sh, sp
	s.dstW, s.dstH = dw, dh

	log.Printf("scaler ready: %dx%d %s -> BGRA", sw, sh, sp.String())
	return nil
}

// toBGRA converts a decoded frame into a tightly packed BGRA slice.
func (s *bgraScaler) toBGRA(src *astiav.Frame) (int, int, []byte, error) {
	if err := s.ensure(src); err != nil {
		return 0, 0, nil, err
	}

	// IMPORTANT: src first, then dst
	if err := s.ssc.ScaleFrame(src, s.dst); err != nil {
		return 0, 0, nil, fmt.Errorf("ScaleFrame: %w", err)
	}

	// Copy the packed image into a contiguous Go slice
	n, err := s.dst.ImageBufferSize(1)
	if err != nil {
		return 0, 0, nil, fmt.Errorf("ImageBufferSize: %w", err)
	}
	out := make([]byte, n)
	if _, err := s.dst.ImageCopyToBuffer(out, 1); err != nil {
		return 0, 0, nil, fmt.Errorf("ImageCopyToBuffer: %w", err)
	}
	return s.dstW, s.dstH, out, nil
}

//
// =======================================
// Decode loop glue (software-only decode)
// =======================================
//

func (w *CamWindow) decodeLoop() {
	defer close(w.done)
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	for {
		// allow stop without blocking
		select {
		case <-w.stop:
			return
		default:
		}

		if err := w.openAndDecode(); err != nil {
			log.Printf("[%s] decode error: %v", w.cfg.Name, err)
			w.setReconnectSoon()
		}

		// small backoff between reconnects
		select {
		case <-w.stop:
			return
		case <-time.After(1 * time.Second):
		}
	}
}

func (w *CamWindow) openAndDecode() error {
	const stallCutoff = 10 * time.Second

	// ---------- input ----------
	fc := astiav.AllocFormatContext()
	if fc == nil {
		return errors.New("AllocFormatContext")
	}
	defer fc.Free()

	rd := astiav.NewDictionary()
	defer rd.Free()

	if w.cfg.RTSPTCP {
		_ = rd.Set("rtsp_transport", "tcp", 0)
		_ = rd.Set("rtsp_flags", "prefer_tcp", 0)
	}
	_ = rd.Set("buffer_size", "1048576", 0) // 1 MiB
	_ = rd.Set("flags", "+low_delay", 0)
	_ = rd.Set("fflags", "+nobuffer+discardcorrupt+genpts", 0) // reduce latency
	_ = rd.Set("max_delay", "500000", 0)                       // 0.5s
	_ = rd.Set("use_wallclock_as_timestamps", "1", 0)
	if w.cfg.Probesize > 0 {
		_ = rd.Set("probesize", fmt.Sprintf("%d", w.cfg.Probesize), 0)
	} else {
		_ = rd.Set("probesize", "5000000", 0) // default 5MB
	}
	_ = rd.Set("reorder_queue_size", "0", 0)
	_ = rd.Set("stimeout", "5000000", 0) // 5s (µs)

	applyFmtParams(w.cfg.FFmpegParams, rd)

	log.Printf("[%s] ffmpeg options: %s", w.cfg.Name, JoinDict(rd))

	if err := fc.OpenInput(w.cfg.URL, nil, rd); err != nil {
		return fmt.Errorf("OpenInput: %w", err)
	}
	if err := fc.FindStreamInfo(nil); err != nil {
		return fmt.Errorf("FindStreamInfo: %w", err)
	}

	// ---------- auto select video stream ----------
	vIdx := -1
	for i, s := range fc.Streams() {
		if s.CodecParameters().MediaType() == astiav.MediaTypeVideo {
			vIdx = i
			break
		}
	}
	if vIdx < 0 {
		return errors.New("no video stream")
	}

	// --- find audio stream (optional) ---
	aIdx := -1
	for i, s := range fc.Streams() {
		if s.CodecParameters().MediaType() == astiav.MediaTypeAudio {
			aIdx = i
			break
		}
	}

	vst := fc.Streams()[vIdx]

	// ---------- decoder (SW only) ----------
	vpar := vst.CodecParameters()
	vdec := astiav.FindDecoder(vpar.CodecID())
	if vdec == nil {
		return errors.New("FindDecoder(video) nil")
	}
	vctx := astiav.AllocCodecContext(vdec)
	if vctx == nil {
		return errors.New("AllocCodecContext(video) nil")
	}
	defer vctx.Free()

	if err := vpar.ToCodecContext(vctx); err != nil {
		return fmt.Errorf("ToCodecContext(video): %w", err)
	}

	// HEVC on Intel benefits from low thread count (stability).
	if w.cfg.Threads > 0 {
		vctx.SetThreadCount(w.cfg.Threads)
	} else if n := vdec.Name(); n == "hevc" || n == "h265" {
		vctx.SetThreadCount(1)
	}

	// force software decode; avoid hardware frames
	vopts := astiav.NewDictionary()
	defer vopts.Free()

	hwaccel := w.cfg.HwAccel
	if hwaccel == "" {
		hwaccel = "none"
	}

	_ = vopts.Set("hwaccel", hwaccel, 0)
	_ = vopts.Set("err_detect", "careful", 0)
	_ = vopts.Set("flags2", "+showall", 0)
	_ = vopts.Set("skip_frame", "default", 0)

	applyDecParams(w.cfg.FFmpegParams, vopts)

	log.Printf("[%s] ffmpeg video options: %s", w.cfg.Name, JoinDict(vopts))

	if err := vctx.Open(vdec, vopts); err != nil {
		return fmt.Errorf("open video: %w", err)
	}

	// initialize PTS gap estimator
	w.pktPtsInited = false
	tb := vst.TimeBase()
	w.tbNum, w.tbDen = tb.Num(), tb.Den()

	r := vst.AvgFrameRate()
	if r.Num() <= 0 || r.Den() <= 0 {
		r = vctx.Framerate() // fallback
	}
	w.fpsNom, w.fpsDen = r.Num(), r.Den()

	// --- audio decoder ---
	var (
		aCtx    *astiav.CodecContext
		aFrame  *astiav.Frame
		aPlayer oto.Player
		aPipeW  *io.PipeWriter
	)
	if aIdx >= 0 && !w.cfg.Mute { // add Mute: true to settings to silence
		aPar := fc.Streams()[aIdx].CodecParameters()
		aDec := astiav.FindDecoder(aPar.CodecID())
		if aDec != nil {
			aCtx = astiav.AllocCodecContext(aDec)
			if aCtx != nil && aPar.ToCodecContext(aCtx) == nil {
				if aCtx.Open(aDec, nil) == nil {
					aFrame = astiav.AllocFrame()
				} else {
					aCtx.Free()
					aCtx = nil
				}
			}
		}
	}
	defer func() {
		if aFrame != nil {
			aFrame.Free()
		}
		if aPlayer != nil {
			_ = aPlayer.Close()
		}
		if aPipeW != nil {
			_ = aPipeW.Close()
		}
		if aCtx != nil {
			aCtx.Free()
		}
	}()

	// ---------- runtime ----------
	var scaler bgraScaler
	defer scaler.close()

	pkt := astiav.AllocPacket()
	defer pkt.Free()
	vf := astiav.AllocFrame()
	defer vf.Free()

	lastProgress := time.Now()

	for {
		// allow graceful stop
		select {
		case <-w.stop:
			return nil
		default:
		}

		if err := fc.ReadFrame(pkt); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			// Ignore transient RTSP hiccups and continue.
			if time.Since(lastProgress) > stallCutoff {
				return fmt.Errorf("stalled (>%s without progress)", stallCutoff)
			}
			time.Sleep(10 * time.Millisecond)
			continue
		}

		// --- audio path ---
		if aCtx != nil && pkt.StreamIndex() == aIdx {
			if err := aCtx.SendPacket(pkt); err == nil || errors.Is(err, astiav.ErrEagain) {
				for {
					if err := aCtx.ReceiveFrame(aFrame); err != nil {
						// EAGAIN / EOF => done with current packet
						break
					}

					// play only packed S16, mono, 8 kHz (typical G.711).
					if aFrame.SampleFormat() == astiav.SampleFormatS16 &&
						aFrame.ChannelLayout().Channels() == 1 &&
						aFrame.SampleRate() == 8000 {

						// Create an Oto Player once per camera.
						if aPlayer == nil || aPipeW == nil {
							pr, pw := io.Pipe()
							p := GlobalAudioContext.NewPlayer(pr)
							if p == nil {
								_ = pw.Close()
								log.Printf("audio: NewPlayer failed")
								aFrame.Unref()
								continue
							}
							p.Play()
							aPlayer = p
							aPipeW = pw
						}

						// For packed S16 mono: data[0] holds nb_samples * 2 bytes.
						if pcm, err := aFrame.Data().Bytes(0); err == nil && len(pcm) > 0 {
							// Clamp to the reported sample count.
							need := aFrame.NbSamples() * 2 // bytes per sample
							if need > len(pcm) {
								need = len(pcm)
							}
							// Fire-and-forget; if the pipe back-pressures a bit, it's fine.
							_, _ = aPipeW.Write(pcm[:need])
						}
					} else {
						// Other formats (e.g., fltp/AAC) are ignored for now.
						// To support them, we need to add a swresample step to S16/8k/mono.
					}
					aFrame.Unref()
				}
			}
			pkt.Unref()
			continue // move to next packet
		}

		if pkt.StreamIndex() == vIdx {
			if globalConfig.ShowDrops { // if frame drop display is enabled we will start to collect the samples
				// accumulate payload size even before decode
				atomic.AddInt64(&w.bytesVideo, int64(pkt.Size()))
				// --- PTS-based gap estimator ---
				pts := pkt.Pts()
				if pts <= 0 { // AV_NOPTS_VALUE or missing
					pts = pkt.Dts()
				}
				if pts > 0 && w.tbDen > 0 && w.fpsNom > 0 && w.fpsDen > 0 {
					if !w.pktPtsInited {
						w.lastPktPTS = pts
						w.pktPtsInited = true
					} else {
						dPTS := pts - w.lastPktPTS
						if dPTS > 0 {
							deltaSec := float64(dPTS) * float64(w.tbNum) / float64(w.tbDen)
							frameDur := float64(w.fpsDen) / float64(w.fpsNom)
							// Ignore absurd gaps (seek/reconnect) and only count realistic misses
							if deltaSec <= 3.0 && frameDur > 0 {
								// expected frames between those two packets
								exp := int(math.Round(deltaSec / frameDur))
								// assume ~1 frame per packet → missing = expected - 1
								miss := exp - 1
								if miss > 0 && miss < 120 { // clamp
									atomic.AddInt64(&w.framesDropped, int64(miss))
								}
							}
						}
						w.lastPktPTS = pts
					}
				}
			}
			pktStart := time.Now() // start measure cpu utilization
			if err := vctx.SendPacket(pkt); err == nil {
				for {
					err := vctx.ReceiveFrame(vf)
					// EAGAIN: no more frames for this packet; not a drop
					if errors.Is(err, astiav.ErrEagain) || errors.Is(err, astiav.ErrEof) {
						break
					}
					if err != nil {
						// count hard decode errors as "drops"
						if globalConfig.ShowDrops {
							atomic.AddInt64(&w.decodeErrs, 1)
							atomic.AddInt64(&w.framesDropped, 1)
						}
						break
					}

					// success...

					// (optional) log the source geometry
					if false {
						ls := vf.Linesize()
						log.Printf("[%s] src fmt=%s w=%d h=%d L0=%d L1=%d L2=%d",
							w.cfg.Name, vf.PixelFormat().String(),
							vf.Width(), vf.Height(), ls[0], ls[1], ls[2])
					}
					// measure usage on colorspace convert (this is usually the hottest bit)
					t0 := time.Now()
					bw, bh, bgra, err := scaler.toBGRA(vf)
					atomic.AddInt64(&w.busyNS, time.Since(t0).Nanoseconds())
					if err != nil {
						log.Printf("[%s] toBGRA error: %v", w.cfg.Name, err)
						vf.Unref()
						continue
					}
					// copy into our buffer (still CPU)
					t1 := time.Now()
					w.buf.put(bw, bh, bgra)
					atomic.AddInt64(&w.busyNS, time.Since(t1).Nanoseconds()) // measure cpu usage
					atomic.AddInt64(&w.framesDecoded, 1)                     // bump the frame counter
					w.lastAdvance = time.Now()
					lastProgress = time.Now()

					vf.Unref()
				}
			}
			// cpu usage: include a little for avcodec plumbing itself
			atomic.AddInt64(&w.busyNS, time.Since(pktStart).Nanoseconds()/10)
		}

		pkt.Unref()

		if time.Since(lastProgress) > stallCutoff {
			return fmt.Errorf("[%s] stall watchdog: no progress for %s", w.cfg.Name, stallCutoff)
		}
	}

	// flush
	_ = vctx.SendPacket(nil)
	for vctx.ReceiveFrame(vf) == nil {
		bw, bh, bgra, err := scaler.toBGRA(vf)
		if err == nil {
			w.buf.put(bw, bh, bgra)
			w.lastAdvance = time.Now()
		}
		vf.Unref()
	}

	return nil
}
