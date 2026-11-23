# VRCtrl Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A Go desktop app (Fyne) that lists videos in a folder and pushes the selected one to iPhone home-screen web apps, which play it muted, looping, and in sync.

**Architecture:** One binary. `internal/library` maps folder files to opaque IDs; `internal/server` serves the embedded phone web app, Range-capable `/video/{id}`, and a `/ws` hub holding the single playback state plus clock-sync pings; `main.go` is the Fyne GUI. Phone JS keeps pure sync math in `sync.js` (tested with `node --test`) and the DOM glue in `app.js`.

**Tech Stack:** Go 1.26, fyne.io/fyne/v2, github.com/coder/websocket, github.com/skip2/go-qrcode, vanilla JS, GitHub Actions.

**Spec:** `docs/superpowers/specs/2026-11-22-vr-video-sync-design.md`

## Global Constraints

- Module path `vrctrl`; binary/app name `VRCtrl` / `vrctrl`.
- Default port `8080`, listen on all interfaces.
- Playable extensions: `.mp4`, `.m4v`, `.mov` (case-insensitive). Shown-but-disabled: `.mkv`, `.avi`, `.webm`, `.wmv`, `.flv`.
- Switch lead time `1500` ms. Drift thresholds: `< 40 ms` none; `40–500 ms` playbackRate `0.95`/`1.05`; `> 500 ms` seek. Check every `500` ms. Clock resync every `10 s`, burst of `5` pings, keep lowest RTT.
- Phone video element: `muted playsinline autoplay loop`, no controls, black background, `object-fit: contain`.
- Phones never send file paths — only IDs.
- No git commits unless the user asks (project is not a repo yet).

## Review Focus

1. Folder changed / rescanned while a video is playing → the playing video's ID must still resolve (IDs derived from relative path hash, not list index).
2. Range requests from iOS (`bytes=0-1`, open-ended ranges) → 206 with correct `Content-Range` and `Content-Type: video/mp4` / `video/quicktime`.
3. Phone joins mid-loop → expected position wraps by duration; error computed across the loop boundary (e.g. expected 0.1 s, actual 9.9 s of 10 s video = 0.2 s error, not 9.8 s).
4. Unknown / traversal ID (`../etc/passwd`, random hex) → 404, never a file read outside the folder.
5. Many phones connecting/disconnecting while the operator clicks → no panic, no stuck broadcast; slow client is dropped, not blocking others.

---

### Task 1: Video library

**Files:**
- Create: `go.mod`, `internal/library/library.go`
- Test: `internal/library/library_test.go`

**Interfaces:**
- Produces:
  - `type Video struct { ID, Name, RelPath string; Playable bool; MIME string }`
  - `type Library struct` (mutex-guarded), `func New() *Library`
  - `func (l *Library) Scan(dir string) ([]Video, error)` — non-recursive, sorted by Name (case-insensitive), dotfiles skipped, non-video files skipped.
  - `func (l *Library) Path(id string) (absPath, mime string, ok bool)` — only for Playable videos in the current scan.
  - `func (l *Library) Dir() string`
  - ID = first 12 hex chars of SHA-1 of the relative path.

- [ ] **Step 1: Write failing tests** — `TestScanListsAndSorts` (temp dir with `b.MP4`, `a.mov`, `c.mkv`, `notes.txt`, `.hidden.mp4` → names `[a.mov b.MP4 c.mkv]`, playable `[true true false]`, MIMEs `video/quicktime`, `video/mp4`); `TestIDStableAcrossRescan` (same ID after adding another file and rescanning); `TestPathRejectsUnknownAndUnplayable` (random ID, `../x`, mkv ID → `ok == false`); `TestScanMissingDir` returns error.
- [ ] **Step 2:** `go test ./internal/library/` → FAIL (undefined).
- [ ] **Step 3:** Implement per Interfaces. MIME: `.mov`→`video/quicktime`, else `video/mp4`.
- [ ] **Step 4:** `go test ./internal/library/` → PASS.

### Task 2: Playback hub (WebSocket)

**Files:**
- Create: `internal/server/hub.go`
- Test: `internal/server/hub_test.go`

**Interfaces:**
- Consumes: nothing from Task 1 (takes URL strings).
- Produces:
  - `type State struct { Stopped bool \`json:"stopped"\`; VideoID string \`json:"videoId,omitempty"\`; URL string \`json:"url,omitempty"\`; Name string \`json:"name,omitempty"\`; StartAt int64 \`json:"startAt,omitempty"\` }` (StartAt = server epoch ms)
  - `func NewHub(now func() time.Time) *Hub` — initial state `Stopped: true`.
  - `func (h *Hub) Play(videoID, name, url string)` — sets `StartAt = now + 1500ms`, broadcasts.
  - `func (h *Hub) Stop()`; `func (h *Hub) State() State`; `func (h *Hub) Count() int`
  - `func (h *Hub) OnCount(fn func(int))` — called on connect/disconnect.
  - `func (h *Hub) ServeWS(w http.ResponseWriter, r *http.Request)`
  - Wire messages (server→phone): `{"type":"state","state":State}`, `{"type":"pong","t0":<float>,"ts":<int64 ms>}`. Phone→server: `{"type":"ping","t0":<float>}`.
  - Each client has a buffered send channel (16); if full, client is closed.

- [ ] **Step 1: Write failing tests** (httptest server, coder/websocket client, fixed `now`): `TestStateOnJoin` (after `Play`, new client's first message is state with `StartAt == now+1500`); `TestBroadcastToAll` (2 clients both get new state after `Play`); `TestPingPong` (pong echoes t0, ts == now ms); `TestCountAndOnCount` (connect 2, close 1 → Count 1, OnCount saw 1,2,1); `TestStopBroadcast`.
- [ ] **Step 2:** `go test ./internal/server/ -run Hub` → FAIL.
- [ ] **Step 3:** Implement. `websocket.Accept` with `InsecureSkipVerify: true` (LAN, any origin). Read loop handles pings; write loop drains channel.
- [ ] **Step 4:** `go test -race ./internal/server/` → PASS.

### Task 3: HTTP server + phone web app

**Files:**
- Create: `internal/server/server.go`, `internal/server/web/index.html`, `web/app.js`, `web/sync.js`, `web/manifest.webmanifest`, `web/icon-180.png`, `web/icon-512.png`
- Test: `internal/server/server_test.go`, `internal/server/web/sync.test.mjs`

**Interfaces:**
- Consumes: `library.Library.Path`, `Hub.ServeWS`.
- Produces:
  - `func New(lib *library.Library, hub *Hub) http.Handler` — routes: `/` + static (embed `web/`, excluding `*.test.mjs`), `/video/{id}` via `http.ServeContent` with explicit `Content-Type`, `Cache-Control: no-cache`; `/ws`.
  - `func Listen(addr string, h http.Handler) (*http.Server, error)` — binds first (returns bind error synchronously, e.g. port busy), serves in a goroutine.
  - `sync.js` exports (ES module, also attached to `window.VRSync`):
    - `bestOffset(samples: {t0,t1,ts}[]) -> number` — offset from lowest-RTT sample: `ts - (t0+t1)/2`.
    - `expectedPosition(serverNowMs, startAtMs, durationS) -> number` — `<0` before start returns 0; else `((now-start)/1000) % duration`.
    - `loopError(actualS, expectedS, durationS) -> number` — signed, shortest way around the loop (`actual - expected`, wrapped into `[-d/2, d/2]`).
    - `correction(errS) -> {action:'none'|'rate'|'seek', rate?:number}` — `|e|<0.04` none rate 1; `<0.5` rate `e>0 ? 0.95 : 1.05`; else seek.

- [ ] **Step 1: Write failing Go tests:** `TestVideoRange` (Range `bytes=0-1` → 206, `Content-Range: bytes 0-1/<size>`, `Content-Type: video/mp4`); `TestVideoUnknownID404` (`/video/deadbeef0000`, `/video/..%2F..%2Fetc%2Fpasswd` → 404); `TestIndexServed` (`/` 200 contains `playsinline` and `apple-mobile-web-app-capable`); `TestManifestServed`; `TestListenPortBusy` (second Listen on same addr → error).
- [ ] **Step 2: Write failing JS tests** (`node --test`): `expectedPosition(11000, 0, 10) ≈ 1`; before start → 0; `loopError(9.9, 0.1, 10) ≈ -0.2`; `loopError(0.1, 9.9, 10) ≈ 0.2`; `correction(0.02)` none; `correction(0.1)` rate 0.95; `correction(-0.1)` rate 1.05; `correction(0.8)` seek; `bestOffset` picks lowest-RTT sample.
- [ ] **Step 3:** Run `go test ./internal/server/` and `node --test internal/server/web/` → FAIL.
- [ ] **Step 4:** Implement server and `sync.js`.
- [ ] **Step 5:** Implement `index.html` + `app.js`: meta `viewport` (`viewport-fit=cover`), `apple-mobile-web-app-capable=yes`, `apple-mobile-web-app-status-bar-style=black`, `apple-touch-icon`, manifest (`display: standalone`, `background_color/theme_color #000`, name `VRCtrl`). `app.js`: WebSocket to `ws://${location.host}/ws` with backoff (0.5 s doubling to 5 s); ping burst on open and every 10 s; on state: Stopped → pause, clear src, show black; new videoId → set src, `load()`, pause, wait for `serverNow() >= startAt` (setTimeout + rAF), then `currentTime = expected`, `play()`; 500 ms tick applies `correction`; `visibilitychange`→ visible: reconnect if needed, re-burst pings, reapply; Wake Lock requested on play and visibility. Status overlay (“Connecting…”, “Waiting for operator”, “Loading…”) visible only when not playing. Icons generated by a tiny Go `//go:build ignore` script `tools/genicon.go` (black bg, white “VR”) — outputs committed.
- [ ] **Step 6:** Both test suites PASS.

### Task 4: Fyne GUI + LAN address + QR

**Files:**
- Create: `main.go`, `internal/netaddr/netaddr.go`, `FyneApp.toml`, `Icon.png` (copy of icon-512)
- Test: `internal/netaddr/netaddr_test.go`

**Interfaces:**
- Consumes: Tasks 1–3.
- Produces: `func netaddr.LANIPv4s() ([]string, error)` — up, non-loopback IPv4s, excluding docker/`lxcbr`/`veth`/`virbr` interfaces, default-route address first (UDP "dial" to `192.0.2.1:9` to read local addr, no packets sent). `func netaddr.URL(ip string, port int) string`.

- [ ] **Step 1:** Failing tests: `TestURL` (`http://10.0.0.5:8080`), `TestLANIPv4sNoLoopback` (no `127.` entries; no error).
- [ ] **Step 2:** FAIL → implement → PASS.
- [ ] **Step 3:** `main.go`: window "VRCtrl" ~900×560. Left: “Choose folder…” (`dialog.NewFolderOpen`), path label, `widget.List` (unplayable rows disabled-looking with “— not playable on iPhone”; ▶ marker on current), Rescan, Stop (black screen). Right: IP dropdown, port entry + Apply (restarts listener; shows error on busy port), URL label, QR image (`go-qrcode` 256 px → `canvas.NewImageFromImage`), “Headsets connected: N” (updated via `hub.OnCount` → `fyne.Do`). Remember last folder/port in `app.Preferences()`; rescan on start. Clicking a playable row → `hub.Play(id, name, "/video/"+id)`.
- [ ] **Step 4:** `go build -o vrctrl .` succeeds; `go vet ./...` clean.

### Task 5: CI builds + README

**Files:**
- Create: `.github/workflows/build.yml`, `README.md`, `.gitignore`

- [ ] **Step 1:** Workflow triggers: `push`, `pull_request`, `workflow_dispatch`, tags `v*`. Jobs (all `actions/setup-go` with `go-version-file: go.mod`, run `go test ./...` and `node --test internal/server/web/` first):
  - `linux` (`ubuntu-latest`): apt `libgl1-mesa-dev xorg-dev`, `go build -o vrctrl .`, upload `vrctrl-linux-amd64`.
  - `windows` (`windows-latest`, preinstalled MinGW gcc): `go build -ldflags "-H=windowsgui" -o vrctrl.exe .`, upload.
  - `macos` (`macos-latest`): build `GOARCH=arm64` and `GOARCH=amd64` (`CGO_ENABLED=1`, `CC="clang -arch x86_64"` for amd64), `lipo -create`, install `fyne.io/tools/cmd/fyne`, `fyne package -os darwin --executable <universal> --name VRCtrl --icon Icon.png`, `ditto -c -k --keepParent VRCtrl.app VRCtrl-macos-universal.zip`, upload.
  - `release` (needs all three, `if: startsWith(github.ref, 'refs/tags/v')`): download artifacts, `softprops/action-gh-release` attach.
- [ ] **Step 2:** Validate locally: YAML parses (`python3 -c 'import yaml,sys; yaml.safe_load(open(".github/workflows/build.yml"))'`).
- [ ] **Step 3:** README: run, firewall prompt (Windows: allow private networks; macOS: allow incoming), iPhone setup (scan QR → Safari Share → Add to Home Screen → open from icon; Settings → Display → Auto-Lock → Never; Low Power Mode off), macOS first open (right-click → Open, or `xattr -dr com.apple.quarantine VRCtrl.app`), building locally, how to get CI builds (push to GitHub, Actions → artifacts; tag `v1.0.0` for a release).
- [ ] **Step 4:** Final: `go test -race ./...`, `node --test internal/server/web/`, `go build`; run the binary headless-check (`timeout 3 ./vrctrl` starts without crash under the desktop session) and curl `http://localhost:8080/` → 200.
