# VRCtrl

Play the same video on every phone-powered VR headset at once, and switch
videos from a laptop without taking the phones out.

- The **laptop app** lists the videos in a folder. Click one and every headset
  switches to it.
- Each **iPhone** runs a home-screen web app that plays the video fullscreen,
  muted and looping, in sync with the other phones (usually within a few
  hundredths of a second).
- Phones that join late or reconnect jump straight to the current spot.

Videos should already be **side-by-side** (left eye | right eye). Playable formats are
**.mp4, .m4v, .mov** (H.264 or HEVC). Other formats show up greyed out.

## Using it

1. Put the laptop and the iPhones on the **same Wi-Fi network**.
2. Start VRCtrl and click **Choose folder…** to pick your video folder. VRCtrl
   remembers the folder next time.
3. The first time you start it, allow incoming connections when your system asks:
   - **Windows:** tick *Private networks* in the firewall prompt.
   - **macOS:** click *Allow* in the firewall prompt.
4. On each iPhone:
   1. Scan the QR code shown in VRCtrl with the Camera app and open the link in Safari.
   2. Tap **Share → Add to Home Screen**, then open **VRCtrl** from the home screen.
   3. **Settings → Display & Brightness → Auto-Lock → Never.** iOS pauses video
      when the screen locks.
   4. Turn **Low Power Mode off**. It can block autoplay.
   5. Put the phone in the headset. The screen stays black until you pick a video.
5. Click a video in VRCtrl. About 1.5 s later every headset starts it together.
   Click another to switch. **Stop (black screen)** blanks all headsets.

### The control window

- **Library:** subfolders show as folder tiles (use the breadcrumb or ↑ to go
  back), and the search box finds videos in every subfolder. Thumbnails and
  lengths come from **ffmpeg** if it is installed (on PATH, next to the app, or
  in Homebrew). Without ffmpeg, a video gets its thumbnail the first time it
  plays on a headset.
- **Live preview:** the big picture is what the headsets show (left eye),
  updated about every 1.5 s. Under it are **Pause/Resume**, **±10 s**,
  **Restart** and **Stop**. Keyboard: Space pauses, ←/→ skip 10 s.
- **Headsets tab:** one tile per phone with its own picture and status
  (playing / buffering / *needs a tap* / not responding, plus how far out of
  sync it is). 👁 shows the headset's name on its screen so you can find it,
  ✎ renames it, and ⛶ pins it to the big preview.

If port 8080 is busy, change it in the app and click **Apply**, then re-scan
the QR code. If the laptop has several networks, pick the Wi-Fi address in
the **Address** list.

If iOS ever refuses to autoplay, the phone shows **Tap to start**. Tap once and
it rejoins in sync.

## Downloads (GitHub Actions)

Every push builds all three platforms. Open **Actions → latest run → Artifacts**:

| Platform | File |
| --- | --- |
| macOS (Apple Silicon + Intel) | `VRCtrl-macos-universal.zip` → `VRCtrl.app` |
| Windows | `vrctrl-windows-amd64.zip` → `vrctrl.exe` |
| Linux | `vrctrl-linux-amd64.tar.gz` → `vrctrl` |

Push a tag like `v1.0.0` to attach the builds to a GitHub Release:

```sh
git tag v1.0.0 && git push origin v1.0.0
```

**macOS first launch:** the app isn't signed with an Apple Developer ID, so
right-click **VRCtrl.app → Open → Open** the first time. You can also run
`xattr -dr com.apple.quarantine VRCtrl.app`.

## Building locally

You need Go 1.26+ and a C compiler.

- **Linux:** `sudo apt install gcc libgl1-mesa-dev xorg-dev libwayland-dev libxkbcommon-dev`, then `go build -o vrctrl .`.
  Without the Wayland headers, build X11-only with `go build -tags x11 -o vrctrl .`.
  That version still runs on Wayland desktops.
- **Windows:** install a MinGW-w64 gcc (e.g. via MSYS2), then `go build -ldflags "-H=windowsgui" -o vrctrl.exe .`
- **macOS:** `xcode-select --install`, then `go build -o vrctrl .`

The first build takes a few minutes while the graphics library compiles.

Tests: `go test ./...` and `node --test internal/server/web/sync.test.mjs`.

## How the sync works

When you pick a video, the laptop chooses a start time 1.5 s in the future and
tells every phone. Each phone measures how far its clock is from the laptop's
clock (repeated every 10 s), loads the video, holds on frame 0, and starts at
exactly that moment. Every half second it compares where it is with where it
should be. Small drift is fixed by playing 5% faster or slower for a moment,
and anything over half a second is fixed by jumping.
