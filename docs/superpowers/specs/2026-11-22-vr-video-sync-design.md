# VRCtrl — Synchronized video for phone-powered VR headsets

Date: 2026-11-22

## Intent

A group of people each wear a headset with an iPhone inside. One operator at a
laptop picks which video everyone watches. Phones must switch videos with no
one removing a phone or touching it, and every headset must show the same
video at the same moment. Hassle-free operation is the point.

## Goals

- Desktop GUI on the laptop: choose a folder, see its videos, click one to send
  it to every connected headset.
- The same program serves a web app the iPhones install to the home screen.
- On the phone: video plays fullscreen, muted, autoplays, loops forever.
- Switching videos requires zero interaction on the phone.
- All phones start the new video at the same instant and stay in sync while
  looping (target: within ~50 ms of each other).
- Phones that join late, reconnect, or come back from background jump straight
  to the current position.
- Runs on Linux, Windows, and macOS.

## Non-goals

- Audio (videos are muted; that is what lets iOS autoplay without a tap).
- 360°/180° video, head tracking, WebXR. Videos are already side-by-side;
  the phone just shows them fullscreen.
- Transcoding. Only formats iPhones play natively are offered (see below).
- Internet access, accounts, or remote control from outside the LAN.

## Anti-goals (would be a failure even if it "works")

- Any step that requires touching the phone once it is in the headset.
- Phones visibly out of step with each other.
- Setup per phone beyond: open URL / scan QR, Add to Home Screen, launch.

## Constraints

- iOS Safari only autoplays **muted, `playsinline`** video; programmatic
  `play()` after changing `src` is allowed under those conditions.
- iOS requires HTTP Range request support for video.
- iOS suspends web apps when the screen locks or the app is backgrounded.
  The page requests a Screen Wake Lock (where supported) and the operator
  instructions tell users to set Auto-Lock to "Never" on headset phones as
  the reliable fallback.
- Plain HTTP on the LAN (no HTTPS needed: no motion sensors, no service worker
  required for add-to-home-screen).
- Go with Fyne requires cgo; cross-OS builds need either the target OS or
  `fyne-cross` (Docker).

## Approaches considered

1. **Native Fyne desktop window + Go server** — real folder picker, connected
   count, QR code; single binary; slower first build. **Chosen**: the user
   asked for a GUI on the laptop, and native picker + QR make room setup fast.
2. Browser-based control panel served on localhost — zero GUI deps, but no
   real folder picker; feels less like an app.
3. Webview-wrapped web UI — needs WebKitGTK (not installed) and offers nothing
   over option 1.

## Design

### Shape

One Go binary (`vrctrl`):

- `main.go` — Fyne GUI, wires GUI to server.
- `internal/library` — scans the chosen folder, assigns stable IDs, maps
  ID → file path (phones never send paths, so no path traversal).
- `internal/server` — HTTP server: static web app (embedded via `embed`),
  `/video/{id}` with Range support via `http.ServeContent`, `/ws` WebSocket
  hub, clock-sync endpoint over the same socket.
- `internal/server/web/` — `index.html`, `app.js`, `manifest.webmanifest`,
  icons. Embedded into the binary.

### Laptop GUI

```
┌ VRCtrl ─────────────────────────────────────────────┐
│ [Choose folder…]  /home/max/Videos/VR               │
│ ┌ Videos ─────────────────┐  Phone URL:             │
│ │ ▶ intro_sbs.mp4   (now) │  http://192.168.150.150:8080
│ │   forest_walk.mp4       │  ┌────────┐             │
│ │   ocean.mov             │  │  QR    │             │
│ │   ...                   │  └────────┘             │
│ └─────────────────────────┘  Headsets connected: 6  │
│ [Rescan]   [Stop (black screen)]                    │
└─────────────────────────────────────────────────────┘
```

- Clicking a video sends it to all phones immediately.
- Last folder is remembered (Fyne preferences) and reopened on launch.
- Playable extensions: `.mp4`, `.m4v`, `.mov`. Other video files
  (`.mkv`, `.avi`, `.webm`) are shown disabled with "not playable on iPhone".
- If the laptop has several LAN addresses, a dropdown picks which one the
  URL/QR uses; default is the address of the default-route interface.

### Sync protocol (WebSocket, JSON)

Server holds the single source of truth:
`{ videoId, url, startAt (server epoch ms) }` or `{ stopped: true }`.

- **Clock sync:** on connect and every 10 s, the phone sends a burst of
  pings `{t0}`; server replies `{t0, ts}`; phone keeps the sample with the
  lowest round-trip and computes `offset = ts - (t0 + t1)/2`.
  `serverNow() = Date.now() + offset`.
- **Switch:** on click, server sets `startAt = now + 1500 ms` and broadcasts.
  Phones set `src`, preload, pause at 0, and call `play()` when
  `serverNow()` reaches `startAt`.
- **Steady state:** expected position =
  `((serverNow() - startAt) / 1000) mod duration`. Every 500 ms the phone
  compares with `currentTime` (taking wrap-around into account):
  - error < 40 ms: do nothing, `playbackRate = 1`
  - 40–500 ms: nudge `playbackRate` to 0.95/1.05 until within tolerance
  - > 500 ms: seek directly.
- **Loop:** the `<video loop>` attribute handles the wrap; the drift
  correction keeps loops aligned.
- **Join/reconnect:** server sends current state on connect; phone seeks to
  the expected position and plays. Socket auto-reconnects with backoff;
  `visibilitychange` → resync immediately.
- **Stop:** phones pause and show black.

### Phone web app

- Black fullscreen page, `<video muted playsinline autoplay loop>`,
  `object-fit: contain`, no controls, no UI chrome.
- `apple-mobile-web-app-capable`, `display: standalone`, black status bar,
  home-screen icon and name "VRCtrl".
- Small overlay shown only when *not* playing (e.g. "Connecting…",
  "Waiting for operator") — hidden during playback so it never appears in
  the headset mid-session.
- Screen Wake Lock requested when supported, re-requested on visibility.

### Builds / distribution

- Linux: native `go build` on this laptop.
- GitHub Actions workflow (`.github/workflows/build.yml`) builds natively on
  `ubuntu-latest`, `windows-latest`, and `macos-latest` on every push / tag /
  manual run and uploads artifacts:
  - macOS: universal (arm64 + amd64 via `lipo`) `VRCtrl.app`, zipped.
    Unsigned — README explains right-click → Open the first time.
  - Windows: `vrctrl.exe` (GUI subsystem, no console window).
  - Linux: `vrctrl` binary (amd64).
  - On a `v*` tag the artifacts are also attached to a GitHub Release.
- README covers: running, Windows firewall prompt (allow on private network),
  iPhone setup (scan QR → Share → Add to Home Screen → set Auto-Lock Never).

### Testing

- Unit tests: library scanning/ID mapping, Range serving, sync math
  (expected position, wrap-around error), hub broadcast/state-on-join.
- Manual: two browser tabs locally to watch sync; then real iPhones (user).

## Calls I made (correct any)

- Port 8080 by default, changeable in the GUI if busy.
- 1.5 s lead time on switch (enough for phones to buffer the start over Wi-Fi).
- "Stop (black screen)" button added; no pause/seek controls.
- No transcoding; unsupported formats shown disabled.
- Videos are served straight from disk; no copying.

## Left to the builder

- Exact Fyne widget layout and styling.
- WebSocket library choice (`github.com/coder/websocket` suggested).
- QR library (`github.com/skip2/go-qrcode` suggested).
- Icon artwork (simple generated icon is fine).
