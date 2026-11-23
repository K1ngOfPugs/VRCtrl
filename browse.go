package main

import (
	"fmt"
	"image/color"
	"path"
	"path/filepath"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"vrctrl/internal/library"
	"vrctrl/internal/thumbs"
)

// item is one tile in the library grid: a folder or a video.
type item struct {
	folder *library.Folder
	video  *library.Video
}

type browser struct {
	all      []library.Video
	folder   string // slash-separated, "" = root
	items    []item
	thumbRes map[string]fyne.Resource // by thumbnail key
	tiles    map[fyne.CanvasObject]*tileParts

	folderLbl *widget.Label
	grid      *widget.GridWrap
	crumbs    *fyne.Container
	upBtn     *widget.Button
	search    *widget.Entry
	infoLbl   *widget.Label
	emptyLbl  *widget.Label
}

func themeRefresh() fyne.Resource { return theme.ViewRefreshIcon() }

const thumbW, thumbH = 208, 117

// tileParts are the pieces of one grid tile that updateTile changes.
type tileParts struct {
	bg       *canvas.Rectangle
	img      *canvas.Image
	icon     *widget.Icon
	dim      *canvas.Rectangle
	badge    *canvas.Text
	badgeBox fyne.CanvasObject
	name     *widget.Label
	sub      *widget.Label
}

func (u *ui) buildBrowser() fyne.CanvasObject {
	u.thumbRes = map[string]fyne.Resource{}
	u.tiles = map[fyne.CanvasObject]*tileParts{}
	u.folderLbl = widget.NewLabel("No folder selected")
	u.folderLbl.Truncation = fyne.TextTruncateEllipsis
	chooseBtn := widget.NewButtonWithIcon("Choose folder…", theme.FolderOpenIcon(), u.chooseFolder)
	rescanBtn := widget.NewButtonWithIcon("Rescan", theme.ViewRefreshIcon(), func() {
		if d := u.lib.Dir(); d != "" {
			u.openFolder(d)
		}
	})

	u.upBtn = widget.NewButtonWithIcon("", theme.MoveUpIcon(), func() {
		if u.folder != "" {
			u.navigate(parentFolder(u.folder))
		}
	})
	u.crumbs = container.NewHBox()
	u.search = widget.NewEntry()
	u.search.SetPlaceHolder("Search all folders…")
	u.search.ActionItem = widget.NewIcon(theme.SearchIcon())
	u.search.OnChanged = func(string) { u.refreshItems(true) }

	u.grid = widget.NewGridWrap(
		func() int { return len(u.items) },
		u.makeTile,
		func(i widget.GridWrapItemID, o fyne.CanvasObject) {
			if i >= 0 && i < len(u.items) {
				u.updateTile(u.items[i], o)
			}
		},
	)
	u.grid.OnSelected = func(i widget.GridWrapItemID) {
		u.grid.UnselectAll() // so clicking the same video again restarts it
		if i < 0 || i >= len(u.items) {
			return
		}
		it := u.items[i]
		if it.folder != nil {
			u.navigate(it.folder.Path)
			return
		}
		if it.video.Playable {
			u.play(*it.video)
		}
	}
	u.emptyLbl = widget.NewLabelWithStyle("", fyne.TextAlignCenter, fyne.TextStyle{Italic: true})
	u.emptyLbl.Wrapping = fyne.TextWrapWord
	u.infoLbl = widget.NewLabel("")
	u.infoLbl.Importance = widget.LowImportance
	u.infoLbl.Wrapping = fyne.TextWrapWord

	top := container.NewVBox(
		container.NewBorder(nil, nil, chooseBtn, rescanBtn, u.folderLbl),
		container.NewBorder(nil, nil, u.upBtn, nil, container.NewHScroll(u.crumbs)),
		u.search,
	)
	u.refreshItems(false)
	return container.NewBorder(top, u.infoLbl, nil, nil,
		container.NewStack(u.grid, container.NewCenter(u.emptyLbl)))
}

func (u *ui) makeTile() fyne.CanvasObject {
	t := &tileParts{}
	t.bg = canvas.NewRectangle(color.Transparent)
	t.bg.CornerRadius = 6
	black := canvas.NewRectangle(color.NRGBA{0x10, 0x10, 0x12, 0xff})
	black.CornerRadius = 4
	t.img = canvas.NewImageFromResource(nil)
	t.img.FillMode = canvas.ImageFillContain
	t.img.ScaleMode = canvas.ImageScaleFastest
	t.img.SetMinSize(fyne.NewSize(thumbW, thumbH))
	t.icon = widget.NewIcon(theme.MediaVideoIcon())
	t.dim = canvas.NewRectangle(color.NRGBA{0, 0, 0, 0xa0})
	t.badge = canvas.NewText("", color.White)
	t.badge.TextStyle = fyne.TextStyle{Bold: true}
	t.badge.TextSize = 12
	t.badgeBox = container.NewStack(canvas.NewRectangle(color.NRGBA{0, 0, 0, 0xb0}), container.NewPadded(t.badge))
	t.name = widget.NewLabelWithStyle("", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	t.name.Truncation = fyne.TextTruncateEllipsis
	t.sub = widget.NewLabel("")
	t.sub.Truncation = fyne.TextTruncateEllipsis
	t.sub.SizeName = theme.SizeNameCaptionText
	t.sub.Importance = widget.LowImportance

	picture := container.NewStack(black, t.img,
		container.NewCenter(container.NewGridWrap(fyne.NewSize(48, 48), t.icon)), t.dim,
		container.NewVBox(layout.NewSpacer(), container.NewHBox(layout.NewSpacer(), t.badgeBox)))
	labels := container.New(layout.NewCustomPaddedVBoxLayout(-12), t.name, t.sub)
	root := container.NewStack(t.bg, container.NewPadded(container.NewBorder(nil, labels, nil, nil, picture)))
	u.tiles[root] = t
	return root
}

func (u *ui) updateTile(it item, o fyne.CanvasObject) {
	t := u.tiles[o]
	if t == nil {
		return
	}
	setBadge := func(s string) {
		t.badge.Text = s
		t.badge.Refresh()
		if s == "" {
			t.badgeBox.Hide()
		} else {
			t.badgeBox.Show()
		}
	}
	t.bg.FillColor = color.Transparent
	t.dim.Hide()
	if it.folder != nil {
		f := it.folder
		t.img.Resource = nil
		t.img.Refresh()
		t.icon.SetResource(theme.FolderIcon())
		t.icon.Show()
		t.name.SetText(f.Name)
		t.sub.SetText(plural(f.Count, "video"))
		setBadge("")
		t.bg.Refresh()
		return
	}
	v := it.video
	res, dur := u.thumbFor(*v)
	t.img.Resource = res
	t.img.Refresh()
	if res == nil {
		t.icon.SetResource(theme.MediaVideoIcon())
		t.icon.Show()
	} else {
		t.icon.Hide()
	}
	t.name.SetText(v.Name)
	sub := ""
	if u.search.Text != "" && v.Folder != "" {
		sub = v.Folder + "  ·  "
	}
	st := u.hub.State()
	switch {
	case !v.Playable:
		sub += "Can't play on iPhone (" + strings.TrimPrefix(strings.ToLower(filepath.Ext(v.Name)), ".") + ")"
		t.dim.Show()
	case !st.Stopped && st.VideoID == v.ID:
		if st.Paused {
			sub += "Paused"
		} else {
			sub += "Now playing"
		}
		t.bg.FillColor = theme.Color(theme.ColorNamePrimary)
	default:
		sub += humanSize(v.Size)
	}
	t.sub.SetText(sub)
	if dur > 0 {
		setBadge(clock(dur))
	} else {
		setBadge("")
	}
	t.bg.Refresh()
}

// thumbFor returns the thumbnail resource (nil if none yet) and duration.
func (u *ui) thumbFor(v library.Video) (fyne.Resource, float64) {
	key := u.thumbKey(v)
	jpg, dur, ok := u.thumbs.Get(key, u.lib.AbsPath(v))
	if !ok {
		return nil, dur
	}
	res, hit := u.thumbRes[key]
	if !hit {
		res = fyne.NewStaticResource("thumb-"+key+".jpg", jpg)
		u.thumbRes[key] = res
	}
	return res, dur
}

func (u *ui) thumbKey(v library.Video) string {
	return thumbs.Key(u.lib.AbsPath(v), v.Size, v.ModTime)
}

// refreshItems rebuilds the grid for the current folder or search.
func (u *ui) refreshItems(scrollTop bool) {
	u.items = u.items[:0]
	q := strings.TrimSpace(u.search.Text)
	if q != "" {
		for _, v := range library.Search(u.all, q) {
			v := v
			u.items = append(u.items, item{video: &v})
		}
	} else {
		folders, vids := library.Browse(u.all, u.folder)
		for _, f := range folders {
			f := f
			u.items = append(u.items, item{folder: &f})
		}
		for _, v := range vids {
			v := v
			u.items = append(u.items, item{video: &v})
		}
	}
	u.rebuildCrumbs(q != "")
	switch {
	case u.lib.Dir() == "":
		u.emptyLbl.SetText("Click “Choose folder…” to pick the folder with your videos.")
	case len(u.items) == 0 && q != "":
		u.emptyLbl.SetText("No videos match “" + q + "”.")
	case len(u.items) == 0:
		u.emptyLbl.SetText("No videos here. Playable formats: .mp4, .m4v, .mov")
	default:
		u.emptyLbl.SetText("")
	}
	if u.emptyLbl.Text == "" {
		u.emptyLbl.Hide()
	} else {
		u.emptyLbl.Show()
	}

	folders := map[string]bool{}
	for _, v := range u.all {
		folders[v.Folder] = true
	}
	info := plural(len(u.all), "video")
	if n := len(folders); n > 1 || (n == 1 && !folders[""]) {
		info += " in " + plural(n, "folder")
	}
	if len(u.all) > 0 && !u.thumbs.HasFFmpeg() {
		info += ". Thumbnails appear after a video has played on a headset (install ffmpeg to get them right away)."
	}
	u.infoLbl.SetText(info)
	if u.folder == "" || q != "" {
		u.upBtn.Disable()
	} else {
		u.upBtn.Enable()
	}
	if scrollTop {
		u.grid.ScrollToTop()
	}
	u.grid.Refresh()
}

func (u *ui) rebuildCrumbs(searching bool) {
	u.crumbs.RemoveAll()
	if searching {
		l := widget.NewLabel("Search results")
		l.TextStyle.Bold = true
		u.crumbs.Add(l)
		u.crumbs.Refresh()
		return
	}
	root := filepath.Base(u.lib.Dir())
	if u.lib.Dir() == "" {
		root = "Library"
	}
	add := func(label, target string, current bool) {
		b := widget.NewButton(label, func() { u.navigate(target) })
		b.Importance = widget.LowImportance
		if current {
			b.Importance = widget.HighImportance
		}
		u.crumbs.Add(b)
	}
	add(root, "", u.folder == "")
	if u.folder != "" {
		parts := strings.Split(u.folder, "/")
		for i, p := range parts {
			u.crumbs.Add(widget.NewLabel("›"))
			add(p, strings.Join(parts[:i+1], "/"), i == len(parts)-1)
		}
	}
	u.crumbs.Refresh()
}

func (u *ui) navigate(folder string) {
	u.folder = folder
	if u.search.Text != "" {
		u.search.SetText("") // triggers refreshItems
	}
	u.refreshItems(true)
}

func (u *ui) chooseFolder() {
	d := dialog.NewFolderOpen(func(uri fyne.ListableURI, err error) {
		if err != nil {
			dialog.ShowError(err, u.win)
			return
		}
		if uri == nil {
			return
		}
		u.openFolder(uri.Path())
	}, u.win)
	d.Resize(fyne.NewSize(800, 520))
	d.Show()
}

func (u *ui) openFolder(dir string) {
	sameDir := dir == u.lib.Dir()
	vids, err := u.lib.Scan(dir)
	if err != nil {
		dialog.ShowError(fmt.Errorf("could not open folder: %w", err), u.win)
		return
	}
	u.app.Preferences().SetString(prefFolder, dir)
	u.all = vids
	// If the playing video is gone (new folder, or file deleted), blank the
	// headsets rather than leave them pointing at a file that no longer exists.
	if st := u.hub.State(); !st.Stopped {
		if _, _, ok := u.lib.Path(st.VideoID); !ok {
			u.hub.Stop()
			u.current = nil
		}
	}
	if !sameDir {
		u.folder = ""
	} else if folders, here := library.Browse(vids, u.folder); len(folders) == 0 && len(here) == 0 {
		u.folder = "" // the folder we were in is gone
	}
	u.folderLbl.SetText(dir)
	u.refreshItems(!sameDir)
	u.tick()
}

func parentFolder(f string) string {
	p := path.Dir(f)
	if p == "." || p == "/" {
		return ""
	}
	return p
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}

func humanSize(b int64) string {
	switch {
	case b >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(b)/(1<<30))
	case b >= 1<<20:
		return fmt.Sprintf("%.0f MB", float64(b)/(1<<20))
	default:
		return fmt.Sprintf("%d KB", b>>10)
	}
}

// clock formats seconds as m:ss or h:mm:ss.
func clock(s float64) string {
	if s < 0 {
		s = 0
	}
	t := int(s)
	if t >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", t/3600, t/60%60, t%60)
	}
	return fmt.Sprintf("%d:%02d", t/60, t%60)
}
