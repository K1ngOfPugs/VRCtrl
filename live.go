package main

import (
	"fmt"
	"image/color"
	"math"
	"strconv"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"vrctrl/internal/library"
	"vrctrl/internal/server"
)

// staleAfter: a headset that hasn't reported for this long is "not responding".
const staleAfter = 5 * time.Second

type live struct {
	current *library.Video // video the headsets are told to play (nil = stopped)
	names   map[string]string
	pinned  string // headset ID shown in the big preview; "" = automatic

	preview    *canvas.Image
	previewMsg *canvas.Text
	previewKey string
	sourceLbl  *widget.Label
	titleLbl   *widget.Label
	progress   *widget.ProgressBar
	pauseBtn   *widget.Button
	transport  []*widget.Button // disabled while stopped

	tabs        *container.AppTabs
	headsetTab  *container.TabItem
	headsetGrid *fyne.Container
	headsetNone *widget.Label
	hsTiles     map[string]*hsTile
}

type hsTile struct {
	root    fyne.CanvasObject
	img     *canvas.Image
	msg     *canvas.Text
	name    *widget.Label
	status  *widget.Label
	snapKey string
	pinBtn  *widget.Button
}

func (u *ui) buildLive(connect fyne.CanvasObject) fyne.CanvasObject {
	u.hsTiles = map[string]*hsTile{}

	// Big preview: the latest frame from a headset (left eye).
	black := canvas.NewRectangle(color.Black)
	u.preview = canvas.NewImageFromResource(nil)
	u.preview.FillMode = canvas.ImageFillContain
	u.preview.SetMinSize(fyne.NewSize(400, 225))
	u.previewMsg = canvas.NewText("", color.NRGBA{0x99, 0x99, 0x99, 0xff})
	u.previewMsg.Alignment = fyne.TextAlignCenter
	u.sourceLbl = widget.NewLabel("")
	u.sourceLbl.SizeName = theme.SizeNameCaptionText
	u.sourceLbl.Importance = widget.LowImportance
	u.sourceLbl.Truncation = fyne.TextTruncateEllipsis

	u.titleLbl = widget.NewLabelWithStyle("Stopped", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	u.titleLbl.Truncation = fyne.TextTruncateEllipsis
	u.progress = widget.NewProgressBar()
	u.progress.TextFormatter = func() string { return "" }

	back := widget.NewButtonWithIcon("10 s", theme.MediaFastRewindIcon(), func() { u.seek(-10) })
	fwd := widget.NewButtonWithIcon("10 s", theme.MediaFastForwardIcon(), func() { u.seek(10) })
	u.pauseBtn = widget.NewButtonWithIcon("Pause", theme.MediaPauseIcon(), u.togglePause)
	u.pauseBtn.Importance = widget.HighImportance
	restart := widget.NewButtonWithIcon("Restart", theme.MediaReplayIcon(), func() {
		if u.current != nil {
			u.play(*u.current)
		}
	})
	stop := widget.NewButtonWithIcon("Stop", theme.MediaStopIcon(), u.stop)
	stop.Importance = widget.DangerImportance
	u.transport = []*widget.Button{back, u.pauseBtn, fwd, restart, stop}

	nowPlaying := container.NewVBox(
		container.NewStack(black, u.preview, container.NewCenter(u.previewMsg)),
		u.sourceLbl,
		u.titleLbl,
		u.progress,
		container.NewHBox(back, u.pauseBtn, fwd, restart, layout.NewSpacer(), stop),
	)

	u.headsetGrid = container.NewGridWrap(fyne.NewSize(212, 196))
	u.headsetNone = widget.NewLabelWithStyle("No headsets connected yet.\nOpen the Connect tab and scan the QR code with each iPhone.",
		fyne.TextAlignCenter, fyne.TextStyle{Italic: true})
	u.headsetNone.Wrapping = fyne.TextWrapWord
	u.headsetTab = container.NewTabItemWithIcon("Headsets (0)", theme.ComputerIcon(),
		container.NewStack(container.NewVScroll(u.headsetGrid), container.NewCenter(u.headsetNone)))
	u.tabs = container.NewAppTabs(u.headsetTab, container.NewTabItemWithIcon("Connect", theme.ContentAddIcon(), connect))
	if u.hub.Count() == 0 {
		u.tabs.SelectIndex(1)
	}
	return container.NewBorder(nowPlaying, nil, nil, nil, u.tabs)
}

// ---- actions ---------------------------------------------------------------

func (u *ui) play(v library.Video) {
	u.current = &v
	u.hub.Play(v.ID, v.Name, "/video/"+v.ID)
	u.grid.Refresh()
	u.tick()
}

func (u *ui) stop() {
	u.hub.Stop()
	u.current = nil
	u.grid.Refresh()
	u.tick()
}

func (u *ui) togglePause() {
	u.hub.TogglePause()
	u.grid.Refresh()
	u.tick()
}

func (u *ui) seek(delta float64) {
	if u.current == nil {
		return
	}
	pos := u.hub.State().Position(time.Now())
	if d := u.duration(u.hub.State(), u.hub.Headsets()); d > 0 {
		pos = math.Mod(pos, d)
	}
	// Seek relative to the wrapped position so skipping back near the start
	// of a loop doesn't jump into the previous loop.
	u.hub.Seek(delta - (u.hub.State().Position(time.Now()) - pos))
	u.tick()
}

// ---- periodic refresh ------------------------------------------------------

func (u *ui) tick() {
	if u.progress == nil {
		return
	}
	st := u.hub.State()
	hs := u.hub.Headsets()
	if st.Stopped {
		u.current = nil
	}

	// Title and transport.
	switch {
	case st.Stopped:
		u.titleLbl.SetText("Stopped — headsets show a black screen")
	case st.Paused:
		u.titleLbl.SetText("Paused:  " + st.Name)
	default:
		u.titleLbl.SetText("Now playing:  " + st.Name)
	}
	if st.Paused {
		u.pauseBtn.SetText("Resume")
		u.pauseBtn.SetIcon(theme.MediaPlayIcon())
	} else {
		u.pauseBtn.SetText("Pause")
		u.pauseBtn.SetIcon(theme.MediaPauseIcon())
	}
	for _, b := range u.transport {
		if st.Stopped {
			b.Disable()
		} else {
			b.Enable()
		}
	}

	// Progress.
	dur := u.duration(st, hs)
	pos := st.Position(time.Now())
	if dur > 0 {
		pos = math.Mod(pos, dur)
	}
	switch {
	case st.Stopped:
		u.progress.TextFormatter = func() string { return "" }
		u.progress.SetValue(0)
	case dur > 0:
		txt := clock(pos) + " / " + clock(dur)
		u.progress.TextFormatter = func() string { return txt }
		u.progress.SetValue(pos / dur)
	default:
		txt := clock(pos)
		u.progress.TextFormatter = func() string { return txt }
		u.progress.SetValue(0)
	}

	u.updatePreview(st, hs, dur)
	u.updateHeadsets(st, hs)

	if u.thumbsChanged.Swap(false) {
		u.grid.Refresh()
	}
}

// duration of the current video: from ffmpeg if we have it, else from a headset.
func (u *ui) duration(st server.State, hs []server.Headset) float64 {
	if st.Stopped {
		return 0
	}
	if u.current != nil && u.current.ID == st.VideoID {
		if _, d := u.thumbFor(*u.current); d > 0 {
			return d
		}
	}
	for _, h := range hs {
		if h.VideoID == st.VideoID && h.Dur > 0 {
			return h.Dur
		}
	}
	return 0
}

func (u *ui) updatePreview(st server.State, hs []server.Headset, dur float64) {
	// Pick the pinned headset, else the one with the freshest frame of the
	// current video.
	var src *server.Headset
	for i := range hs {
		h := &hs[i]
		if h.ID == u.pinned {
			src = h
			break
		}
		if h.Snap != nil && h.SnapVid == st.VideoID && (src == nil || h.SnapAt.After(src.SnapAt)) {
			src = h
		}
	}
	if u.pinned != "" && (src == nil || src.ID != u.pinned) {
		u.pinned = "" // pinned headset left
	}

	msg, source := "", ""
	var res fyne.Resource
	key := ""
	switch {
	case st.Stopped:
		msg = "Stopped"
	case len(hs) == 0:
		msg = "No headsets connected"
	case src == nil || src.Snap == nil || src.SnapVid != st.VideoID:
		msg = "Waiting for a picture from the headsets…"
	default:
		key = fmt.Sprintf("snap-%s-%d.jpg", src.ID, src.SnapAt.UnixNano())
		if key == u.previewKey {
			res = u.preview.Resource
		} else {
			res = fyne.NewStaticResource(key, src.Snap)
		}
		mode := "auto"
		if u.pinned != "" {
			mode = "pinned"
		}
		source = fmt.Sprintf("Live view from %s (left eye, %s) · %s",
			u.headsetName(src.ID), mode, ago(time.Since(src.SnapAt)))
	}
	if st.Stopped && u.current == nil {
		source = ""
	}
	if key != u.previewKey {
		u.previewKey = key
		u.preview.Resource = res
		u.preview.Refresh()
	}
	if res == nil && u.preview.Resource != nil {
		u.preview.Resource = nil
		u.preview.Refresh()
	}
	u.previewMsg.Text = msg
	u.previewMsg.Refresh()
	u.sourceLbl.SetText(source)

	// No ffmpeg: keep a frame from a headset as this video's thumbnail.
	if src != nil && src.Snap != nil && src.SnapVid == st.VideoID && src.SnapPos > 2 && u.current != nil {
		if r, _ := u.thumbFor(*u.current); r == nil {
			u.thumbs.Put(u.thumbKey(*u.current), src.Snap, dur)
		}
	}
}

func (u *ui) updateHeadsets(st server.State, hs []server.Headset) {
	// Only refresh the tabs when the label changes: refreshing rebuilds the
	// tab buttons and would swallow clicks.
	if label := fmt.Sprintf("Headsets (%d)", len(hs)); label != u.headsetTab.Text {
		u.headsetTab.Text = label
		u.tabs.Refresh()
	}
	u.countLbl.SetText(fmt.Sprintf("Headsets connected: %d", len(hs)))
	if len(hs) == 0 {
		u.headsetNone.Show()
	} else {
		u.headsetNone.Hide()
	}

	changed := len(hs) != len(u.headsetGrid.Objects)
	seen := map[string]bool{}
	for i, h := range hs {
		seen[h.ID] = true
		t := u.hsTiles[h.ID]
		if t == nil {
			t = u.newHeadsetTile(h.ID)
			u.hsTiles[h.ID] = t
			changed = true
		} else if !changed && u.headsetGrid.Objects[i] != t.root {
			changed = true
		}
		u.updateHeadsetTile(t, h, st)
	}
	for id := range u.hsTiles {
		if !seen[id] {
			delete(u.hsTiles, id)
			changed = true
		}
	}
	if changed {
		objs := make([]fyne.CanvasObject, 0, len(hs))
		for _, h := range hs {
			objs = append(objs, u.hsTiles[h.ID].root)
		}
		u.headsetGrid.Objects = objs
		u.headsetGrid.Refresh()
	}
}

func (u *ui) newHeadsetTile(id string) *hsTile {
	t := &hsTile{}
	black := canvas.NewRectangle(color.Black)
	black.CornerRadius = 4
	t.img = canvas.NewImageFromResource(nil)
	t.img.FillMode = canvas.ImageFillContain
	t.img.ScaleMode = canvas.ImageScaleFastest
	t.img.SetMinSize(fyne.NewSize(200, 112))
	t.msg = canvas.NewText("", color.NRGBA{0x88, 0x88, 0x88, 0xff})
	t.msg.TextSize = 12
	t.name = widget.NewLabelWithStyle("", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	t.name.Truncation = fyne.TextTruncateEllipsis
	t.status = widget.NewLabel("")
	t.status.SizeName = theme.SizeNameCaptionText
	t.status.Truncation = fyne.TextTruncateEllipsis

	ident := widget.NewButtonWithIcon("", theme.VisibilityIcon(), func() {
		u.hub.Identify(id, u.headsetName(id))
	})
	rename := widget.NewButtonWithIcon("", theme.DocumentCreateIcon(), func() { u.renameHeadset(id) })
	t.pinBtn = widget.NewButtonWithIcon("", theme.ViewFullScreenIcon(), func() {
		if u.pinned == id {
			u.pinned = ""
		} else {
			u.pinned = id
		}
		u.tick()
	})
	for _, b := range []*widget.Button{ident, rename, t.pinBtn} {
		b.Importance = widget.LowImportance
	}
	nameRow := container.NewBorder(nil, nil, nil, container.NewHBox(t.pinBtn, ident, rename), t.name)
	labels := container.New(layout.NewCustomPaddedVBoxLayout(-12), nameRow, t.status)
	card := canvas.NewRectangle(theme.Color(theme.ColorNameInputBackground))
	card.CornerRadius = 6
	t.root = container.NewStack(card, container.NewPadded(container.NewBorder(nil, labels, nil, nil,
		container.NewStack(black, t.img, container.NewCenter(t.msg)))))
	return t
}

func (u *ui) updateHeadsetTile(t *hsTile, h server.Headset, st server.State) {
	t.name.SetText(u.headsetName(h.ID))
	if u.pinned == h.ID {
		t.pinBtn.Importance = widget.HighImportance
	} else {
		t.pinBtn.Importance = widget.LowImportance
	}
	t.pinBtn.Refresh()

	text, imp := headsetStatus(h, st)
	t.status.SetText(text)
	t.status.Importance = imp
	t.status.Refresh()

	showSnap := h.Snap != nil && !st.Stopped && h.SnapVid == st.VideoID
	key := ""
	if showSnap {
		key = fmt.Sprintf("hs-%s-%d.jpg", h.ID, h.SnapAt.UnixNano())
	}
	if key != t.snapKey {
		t.snapKey = key
		if showSnap {
			t.img.Resource = fyne.NewStaticResource(key, h.Snap)
		} else {
			t.img.Resource = nil
		}
		t.img.Refresh()
	}
	msg := ""
	if !showSnap {
		if st.Stopped {
			msg = "Black screen"
		} else {
			msg = "No picture yet"
		}
	}
	if t.msg.Text != msg {
		t.msg.Text = msg
		t.msg.Refresh()
	}
}

// headsetStatus describes a headset for the operator, flagging problems.
func headsetStatus(h server.Headset, st server.State) (string, widget.Importance) {
	if time.Since(h.Seen) > staleAfter {
		return "Not responding (screen locked?)", widget.WarningImportance
	}
	if !st.Stopped && h.State != "idle" && h.State != "error" && h.VideoID != "" && h.VideoID != st.VideoID {
		return "Switching video…", widget.MediumImportance
	}
	switch h.State {
	case "playing":
		d := math.Abs(h.DriftMS)
		if d >= 150 {
			return "Playing · " + strconv.Itoa(int(d)) + " ms off, catching up", widget.WarningImportance
		}
		return "Playing · in sync", widget.SuccessImportance
	case "buffering":
		return "Buffering…", widget.WarningImportance
	case "waiting":
		return "Ready, starting…", widget.MediumImportance
	case "loading":
		return "Loading video…", widget.MediumImportance
	case "paused":
		return "Paused", widget.MediumImportance
	case "tap":
		return "Needs a tap on the screen!", widget.DangerImportance
	case "error":
		return "Can't play this video", widget.DangerImportance
	default:
		return "Black screen (stopped)", widget.LowImportance
	}
}

func (u *ui) renameHeadset(id string) {
	e := widget.NewEntry()
	e.SetText(u.headsetName(id))
	dialog.ShowForm("Rename headset", "Save", "Cancel",
		[]*widget.FormItem{widget.NewFormItem("Name", e)},
		func(ok bool) {
			if !ok || e.Text == "" {
				return
			}
			u.names[id] = e.Text
			u.saveNames()
			u.hub.Identify(id, e.Text)
			u.tick()
		}, u.win)
}

func ago(d time.Duration) string {
	if d < 2*time.Second {
		return "updated just now"
	}
	return fmt.Sprintf("updated %ds ago", int(d.Seconds()))
}
