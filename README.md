# Intro - What is QAnotherRTSP and why was it created?

Those who follow me probably already know about my previous project, AnotherRTSP a small, simple, and lightweight player for monitoring security cameras. Since I was never an active Windows user (I only used it temporarily), I built that player in a way that seemed most comfortable and convenient for the Windows environment.
After a short break, I switched back to Unix systems, specifically macOS, where I used SecuritySpy. That made me think: why not create a similar product (maybe even the same) but cross-platform, so it could run not only on macOS or Windows but also on Linux, BSD, and others?
Currently, I’m practicing the Go programming language and recently started learning the Qt toolkit for building graphical UIs (check out my project Conan). That’s why I decided it was time to take on the challenge and build at least a comparable cross-platform alternative.
Once again, we learn from mistakes. In AnotherRTSP, the mistake was using the easyrtsp library, which is semi-closed, hard to configure, unsuitable in many cases, and offers almost zero options for advanced users. This time, I chose to use the widely adopted FFmpeg library. I can say that this approach worked out much better.
Of course, what you see now is just a preliminary version, but it already works quite well and conveniently—similar to the original AnotherRTSP (without MQTT or other advanced features). It’s a basic RTSP player with the same space-saving windows, plus a new feature: snap windows that stick together when placed side by side.
The core functionality hasn’t changed: cameras are still easy to enable or disable, and almost all popular formats and codecs are supported. Most importantly, the app now works across Windows, macOS, and Linux. (Linux binaries aren’t available yet, but I’ll soon release an AppBundle.)
This project required quite a bit of patience—integrating FFmpeg is not easy, and the chosen Qt bindings are still a relatively young project. But the results are very satisfying, and I believe they will please others too.
Of course, don’t expect an enterprise-grade product, but as a basic tool for viewing RTSP camera streams, it works perfectly on all three major platforms. And in the future, I can confidently say we’ll have support for even more systems—maybe Haiku, BSD, Solaris, or other more exotic operating systems.

QAnotherRTSP is completely open source and released under the GPL-3.0 license.

Thank you for your attention, and I wish you an enjoyable experience! :-)



# QAnotherRTSP

**QAnotherRTSP** is a lightweight cross-platform, multi-camera RTSP viewer with a system-tray UI, built in Go + Qt, focused on fast, live control.
* **Tray-based control**: one click to open/close each camera; states shown as checkboxes.
* **Instant edits**: add, edit, or remove cameras and the app applies changes live (no restart).
* **Borderless windows (optional)**: clean frameless view with a small name label; drag to move, edge/corner to resize.
* **Snap & stack**: magnetic window snapping; glued groups move together. Hold Alt to temporarily disable snap.
* **Per-camera tuning**: RTSP over TCP, always-on-top, mute, stretch, and an FFmpeg params field (-fOPTION=…, -cOPTION=…) for advanced input/decoder options.
* **Hardware decode**: VideoToolbox on macOS (auto-fallback to software when unsupported).
* **Simple config**: settings saved to a YAML file in your user config directory.
* **Formations (window presets)** — save/restore which cameras are open and where their windows are placed.
* **Diagnostics overlays** — per‑camera health chip (0–5) and optional FPS / bitrate / Drops % / CPU Usage text.

In short: **a fast, flexible RTSP viewer that lets you manage multiple feeds from the tray and tweak everything on the fly.**
---

# Screenshots

<img width="339" height="713" alt="QAnotherRTSP-pic1" src="https://github.com/user-attachments/assets/d0b06f15-00f6-47d5-bf3a-caaf2c60f118" />

<img width="527" height="255" alt="Screenshot 2025-09-16 at 23 54 12" src="https://github.com/user-attachments/assets/0d629731-30f4-4129-84b3-72693b78dcfa" />

---

## Quick Start

1. Launch the app.  
2. If you have no cameras configured, the **Settings** window opens.  
3. Add a camera in **Settings → Cameras → Add**, enter its Name, URL and options, then **Save**.  
4. The camera opens immediately and also appears in the tray menu.

---

## Settings Window

The Settings window is a dialog with three tabs and a footer:

- **Tabs**
  - **Cameras** — manage your camera list.
  - **Settings** — global toggles like borderless windows and snapping.
  - **Advanced** — (reserved for future options).

- **Footer**  
  - **Save** — writes changes to disk and applies them immediately.  
  - **Cancel** — closes without saving further changes.

---

## Managing Cameras

### Add
- Go to **Settings → Cameras → Add**.
- Fill in **Name**, **URL**, and optional flags.
- Click **OK**. The new camera:
  - appears in the list,
  - starts **immediately**,
  - is added to the tray menu.

### Edit
- Select a camera → **Edit**.
- Change fields and **OK**. The camera:
  - **restarts immediately** with the new settings,

### Remove
- Select a camera → **Remove** and confirm. The camera:
  - **closes immediately**,
  - is removed from the list,
  - and all cameras after it shift up; the app realigns internal indices and tray entries.

> **Note:** “Save” persists changes to `settings.yml`. The live add/edit/remove effects happen right away so you can verify results instantly.

---

---

## Formations (Window Presets)

**What it is.** A *Formation* is a named layout. It remembers which camera windows are open and their geometry (x/y/width/height). Switch between workflows instantly (e.g., *Office*, *Yard*, *Night*).

**Where to find it.** Open the **tray icon → Formations**. You’ll see:
- **Save current as…** — prompts for a name and saves the current layout (overwrites if that name already exists).
- One **submenu per formation**:
  - The submenu title has a **✓ checkmark** when that formation is active.
  - **Apply** - Applies the formation
  - **Save (overwrite)** — update the formation with current window positions.
  - **Delete** — remove the formation from the config.

> Loading happens when you open a formation’s submenu (click its title). You immediately get Save/Delete actions in that submenu.

**What gets saved**
- Open/closed state of each camera window
- Window X/Y position, Width/Height
- Camera is matched by its `ID` (falls back to `Name` if `ID` is empty)

**Behavior**
- Applying a formation opens and positions all listed windows, and closes other camera windows.
- Tray checkboxes are synced to the loaded formation.
- Config is saved after apply/overwrite/delete.

## Tray Menu

- Each camera has a checkbox item: **checked = enabled/open**, **unchecked = disabled/closed**.
- The tray refreshes when you add/edit/remove cameras, so it always reflects the current list and states.

---

## Window Modes & Controls

### Camera Windows Without Titles (Borderless Mode)
- **Settings → Camera windows without titles** (checkbox).
- When enabled, camera windows are frameless (no OS title bar).
- A small **name label** appears at the **top-left** of the video so you can still see which camera you’re viewing.
- When you disable borderless mode, normal title bars return and the overlay label hides.

### Move & Resize (Borderless)
- **Move:** click-drag anywhere that isn’t a resize edge.
- **Resize:** drag near an edge or corner (about an 8-pixel margin). The cursor changes to indicate the resize direction.
- Works only when borderless mode is enabled (and not fullscreen).

### Snapping & Stacking (“Glue”)
- **Settings → Enable window snapping (glue/stack)** toggles this behavior.
- With snapping enabled:
  - Windows **magnetically snap** to the edges of other windows when you drag them within ~12 px.
  - If windows are already touching edge-to-edge, they become a **stack**: dragging one moves the whole glued group together.
- **Hold Alt** while dragging to **temporarily disable** snapping and stacking. (You move only the active window with no magnets.)
- Snapping/stacking only applies in **borderless** mode (and not fullscreen).

---

## Camera Options (Per-Camera)

- **Use RTSP over TCP** — helps with unstable networks/NATs.
- **Always on top** — keep the window above others.
- **Mute audio** — disable audio playback for this camera.
- **FFmpeg params** — advanced options (see below).
- **Stretch video to window** — fill the window area.
- **HW acceleration** — choose a hardware decoder (platform dependent).

---

## FFmpeg Params (Advanced)

Each camera’s **FFmpeg params** field accepts extra decoder/open options in a compact form:

- `-fOPTION=value` → **input/format** option (applies when opening the stream).
- `-cOPTION=value` → **decoder** option (applies when starting the codec).

**Examples**
```text
-frtsp_transport=tcp -fstimeout=5000000 -cthreads=2 -cflags=+low_delay
-fuser_agent="AnotherRTSP/1.0"
-chwaccel=videotoolbox -chwaccel_output_format=nv12
```

**Notes**
- Space-separated; values may be in quotes.
- Invalid tokens (without `=`) are ignored.
- The app’s UI options (like **RTSP over TCP**, **HwAccel**) are applied **before** params and you can **override** default entries.
- For debugging, the app logs the **effective** options it set before opening.

**Common keys**
- Format/input (`-f...`): `rtsp_transport`, `stimeout`, `max_delay`, `user_agent`, `probesize`, `analyzeduration`.
- Decoder (`-c...`): `threads`, `flags`, `hwaccel`, `hwaccel_output_format`.

---

## Hardware Decoding (macOS)

- Use **VideoToolbox** via **HwAccel = `videotoolbox`**.
- Good default pairing: `-chwaccel_output_format=nv12` (HW decode to NV12 on CPU; easy to render).
- For 10-bit HEVC (if supported): `-chwaccel_output_format=p010le`.
- If HW decode isn’t supported on your machine/stream, the app automatically falls back to software.


---

## Diagnostics Overlays

Turn these on/off in **Settings → Overlays**:
- **Show health chip (0–5)** — a five‑bar indicator shown at the **top‑right** of each video window:
  - **5**: smooth (≥24 FPS), **4**: good (≥15 FPS), **3**: OK (≥5 FPS), **2**: low (>0), **0**: stalled.
  - The level is reduced by one step when Drops% is high in the last second.
- **Overlay FPS** — frames per second averaged over ~1s.
- **Overlay bitrate** — kbps computed from video packets only.
- **Overlay dropped frames %** — percentage of **missing/failed** frames during the last second.
- **Overlay CPU** - The overlay reports the **busy fraction** of one core of CPU:

**How Drops% works (short version)**
- Normal decoder churn (`EAGAIN` / `EOF`) does **not** count as a drop.
- We count **hard decode errors** and **implied missing frames** using packet **PTS/DTS** gaps:
  - If timestamps jump by more than one frame interval (but less than ~2s), we treat the gap as missing frames.
- The overlay shows: `drops% = missing / (shown + missing)` over the last second, clamped to 0–100%.
- Smooth, steady cameras often show **0.0%**; brief network hiccups make it non‑zero for a moment.

**How CPU usage is measured**
The overlay reports the **busy fraction** of one core:
1. The camera’s decode path accumulates “busy nanoseconds” while it performs CPU‑intensive work:
   - codec output handling
   - colorspace conversion to BGRA
   - copying the frame into the UI buffer
2. Every ~1s the app computes:
   \[ `cpu% = busy_delta_ns / (elapsed_seconds * 1e9) * 100` \]
3. The value is **clamped** to a reasonable range (e.g., 0–400%) to avoid runaway spikes.

Why this approach?
- It’s **portable** across OSes (no per‑thread OS accounting required).
- It correlates well with what actually slows the app: per‑camera decode + convert + copy time.
- FFmpeg may use additional worker threads; the overlay still gives a **useful per‑camera estimate** of load.

Limitations
- The overlay measures **app busy time**, not total system CPU usage.
- FFmpeg may spawn additional worker threads; the app’s overlay number is a **conservative estimate** of total per‑camera CPU usage.
- On extremely bursty streams, numbers can momentarily spike; they should settle within a second.
- The overlay draw itself is very cheap (simple text rendering).

How to read the number
- **0–20%**: very light stream (low resolution/fps or hardware‑friendly encoding).
- **20–60%**: normal 1080p H.264, stable network, no scaling bottlenecks.
- **60–100%**: heavy stream (e.g., 4K, HEVC, or aggressive scaling).
- **>100%**: multi‑threaded decoding is using more than one core’s worth of CPU for this camera.

Tips to reduce CPU cost
- **Lower resolution/fps** on the camera or switch to a **sub‑stream** for live view.
- Prefer **H.264** over HEVC/H.265 when CPU is constrained (or ensure hardware decode).
- Reduce scaling/conversion cost (use native pixel formats where possible).
- Keep the window size close to the source size to minimize scaling work.
- Avoid showing too many high‑FPS windows at once; use **Formations** to switch layouts.

**Rendering positions**
- **Health chip:** top right.
- **Stats text:** bottom‑left with a subtle shadow for readability.

**Performance**
- Overlays cost very little (simple draw calls). Metrics update once per second.

---

## Enabling / Disabling (Settings)

Open **Settings** (GUI) and toggle under **Overlays**:
- “Show health chip (0–5)”
- “Overlay FPS”
- “Overlay bitrate”
- “Overlay dropped frames %”

Changes apply immediately and persist to the YAML config.

---

## Troubleshooting

- **After restart, camera is slow to reconnect or logs “Operation not permitted”**  
  Many RTSP servers briefly hold sessions after close. The app waits for the previous decoder to stop, then retries with a short grace period and small backoff. If you still see transient failures:
  - keep **RTSP over TCP** on,
  - ensure the URL is correct,
  - try increasing `-fstimeout` or reducing latency keys like `-fmax_delay`.

- **No video after changing FFmpeg params**  
  Remove recent custom flags to bisect. UI settings override conflicting entries.
- **Video flickering (grey window)**
  Add ffmpeg parameter: `-cskip_frame=nokey`

- **Window won’t snap/stack**  
  Make sure **borderless mode** and **Enable window snapping (glue/stack)** are both enabled. Hold **Alt** only if you want to temporarily disable magnets.

---

## Shortcuts & Tips

- **Drag + Alt** — temporarily disable snapping/stacking while moving a borderless window.
- **Resize from corners/edges** — hover near edges to get the resize cursor.
- **Name overlay** (top-left) appears only in borderless mode; it updates when you rename a camera.
- **Formations + multi‑monitor:** Formations restore geometry on the current display setup. After monitor changes, apply the formation and re‑save (overwrite) if needed.
- **Meaning of Drops%:** It’s a best‑effort signal derived from timestamps; it won’t necessarily match values reported by your camera firmware.
- **Window features:** Title visibility, Always‑on‑Top, and snapping work alongside formations.

---

## Where the Config Lives

- The app saves configuration to your user config directory (e.g., `~/.config/another-rtsp/settings.yml`).
- **Save** in the Settings dialog writes changes immediately.


# Other tools

* [Best tool for discover cameras](https://github.com/Ullaakut/cameradar)
