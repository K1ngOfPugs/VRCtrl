// VRCtrl: pick a video on the laptop, every phone headset plays it in sync.
package main

import (
	"fmt"
	"image/color"
	"net/http"
	"strconv"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	qrcode "github.com/skip2/go-qrcode"

	"vrctrl/internal/library"
	"vrctrl/internal/netaddr"
	"vrctrl/internal/server"
)

const (
	prefFolder = "folder"
	prefPort   = "port"
	defPort    = 8080
)

type ui struct {
	app     fyne.App
	win     fyne.Window
	lib     *library.Library
	hub     *server.Hub
	handler http.Handler
	srv     *http.Server

	videos    []library.Video
	currentID string
	port      int
	ip        string

	folderLbl *widget.Label
	list      *widget.List
	nowLbl    *widget.Label
	urlLbl    *widget.Label
	countLbl  *widget.Label
	qr        *canvas.Image
	ipSel     *widget.Select
	portEntry *widget.Entry
}

func main() {
	a := app.NewWithID("io.vrctrl.app")
	u := &ui{app: a, lib: library.New(), hub: server.NewHub(time.Now)}
	u.handler = server.New(u.lib, u.hub)
	u.win = a.NewWindow("VRCtrl")
	u.build()
	u.win.Resize(fyne.NewSize(920, 580))

	u.port = a.Preferences().IntWithFallback(prefPort, defPort)
	u.portEntry.SetText(strconv.Itoa(u.port))
	u.refreshIPs()
	u.startServer(u.port)
	if dir := a.Preferences().String(prefFolder); dir != "" {
		u.openFolder(dir)
	}
	u.win.SetOnClosed(func() {
		if u.srv != nil {
			u.srv.Close()
		}
	})
	u.win.ShowAndRun()
}

func (u *ui) build() {
	u.folderLbl = widget.NewLabel("No folder selected")
	u.folderLbl.Truncation = fyne.TextTruncateEllipsis
	chooseBtn := widget.NewButtonWithIcon("Choose folder…", theme.FolderOpenIcon(), u.chooseFolder)

	u.list = widget.NewList(
		func() int { return len(u.videos) },
		func() fyne.CanvasObject {
			return container.NewHBox(widget.NewIcon(theme.MediaPlayIcon()), widget.NewLabel("video name placeholder"))
		},
		func(i widget.ListItemID, o fyne.CanvasObject) {
			row := o.(*fyne.Container)
			icon, lbl := row.Objects[0].(*widget.Icon), row.Objects[1].(*widget.Label)
			v := u.videos[i]
			switch {
			case !v.Playable:
				icon.SetResource(theme.NewDisabledResource(theme.CancelIcon()))
				lbl.SetText(v.Name + "  — not playable on iPhone")
				lbl.Importance = widget.LowImportance
			case v.ID == u.currentID:
				icon.SetResource(theme.MediaPlayIcon())
				lbl.SetText(v.Name + "  (now playing)")
				lbl.Importance = widget.HighImportance
			default:
				icon.SetResource(theme.NewDisabledResource(theme.MediaPlayIcon()))
				lbl.SetText(v.Name)
				lbl.Importance = widget.MediumImportance
			}
			lbl.Refresh()
		},
	)
	u.list.OnSelected = func(i widget.ListItemID) {
		u.list.UnselectAll() // so clicking the same video again restarts it
		if i < 0 || i >= len(u.videos) || !u.videos[i].Playable {
			return
		}
		v := u.videos[i]
		u.currentID = v.ID
		u.hub.Play(v.ID, v.Name, "/video/"+v.ID)
		u.nowLbl.SetText("Now playing: " + v.Name)
		u.list.Refresh()
	}

	rescanBtn := widget.NewButtonWithIcon("Rescan", theme.ViewRefreshIcon(), func() {
		if d := u.lib.Dir(); d != "" {
			u.openFolder(d)
		}
	})
	stopBtn := widget.NewButtonWithIcon("Stop (black screen)", theme.MediaStopIcon(), func() {
		u.hub.Stop()
		u.currentID = ""
		u.nowLbl.SetText("Stopped")
		u.list.Refresh()
	})
	stopBtn.Importance = widget.DangerImportance
	u.nowLbl = widget.NewLabel("Stopped")

	left := container.NewBorder(
		container.NewBorder(nil, nil, chooseBtn, nil, u.folderLbl),
		container.NewVBox(u.nowLbl, container.NewHBox(rescanBtn, layout.NewSpacer(), stopBtn)),
		nil, nil,
		u.list,
	)

	// Right: connection info.
	u.qr = canvas.NewImageFromResource(nil)
	u.qr.FillMode = canvas.ImageFillContain
	u.qr.SetMinSize(fyne.NewSize(240, 240))
	u.urlLbl = widget.NewLabelWithStyle("", fyne.TextAlignCenter, fyne.TextStyle{Bold: true})
	u.urlLbl.Selectable = true
	u.countLbl = widget.NewLabelWithStyle("Headsets connected: 0", fyne.TextAlignCenter, fyne.TextStyle{})
	u.hub.OnCount(func(n int) {
		fyne.Do(func() { u.countLbl.SetText(fmt.Sprintf("Headsets connected: %d", n)) })
	})

	u.ipSel = widget.NewSelect(nil, func(ip string) {
		u.ip = ip
		u.refreshURL()
	})
	refreshIPBtn := widget.NewButtonWithIcon("", theme.ViewRefreshIcon(), u.refreshIPs)
	u.portEntry = widget.NewEntry()
	applyPort := widget.NewButton("Apply", func() {
		p, err := strconv.Atoi(u.portEntry.Text)
		if err != nil || p < 1 || p > 65535 {
			dialog.ShowError(fmt.Errorf("port must be a number from 1 to 65535"), u.win)
			return
		}
		u.startServer(p)
	})

	qrBg := canvas.NewRectangle(color.White)
	right := container.NewVBox(
		widget.NewLabelWithStyle("Open on each iPhone", fyne.TextAlignCenter, fyne.TextStyle{Bold: true}),
		container.NewStack(qrBg, container.NewPadded(u.qr)),
		u.urlLbl,
		u.countLbl,
		widget.NewSeparator(),
		widget.NewForm(
			widget.NewFormItem("Address", container.NewBorder(nil, nil, nil, refreshIPBtn, u.ipSel)),
			widget.NewFormItem("Port", container.NewBorder(nil, nil, nil, applyPort, u.portEntry)),
		),
	)

	split := container.NewHSplit(container.NewPadded(left), container.NewPadded(right))
	split.SetOffset(0.6)
	u.win.SetContent(split)
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
	vids, err := u.lib.Scan(dir)
	if err != nil {
		dialog.ShowError(fmt.Errorf("could not open folder: %w", err), u.win)
		return
	}
	u.app.Preferences().SetString(prefFolder, dir)
	u.videos = vids
	// If the playing video is gone (new folder, or file deleted), blank the
	// headsets rather than leave them pointing at a file that no longer exists.
	if u.currentID != "" {
		if _, _, ok := u.lib.Path(u.currentID); !ok {
			u.hub.Stop()
			u.currentID = ""
			u.nowLbl.SetText("Stopped (video no longer in folder)")
		}
	}
	u.folderLbl.SetText(dir)
	u.list.Refresh()
}

func (u *ui) refreshIPs() {
	ips, _ := netaddr.LANIPv4s()
	if len(ips) == 0 {
		ips = []string{"127.0.0.1"}
	}
	u.ipSel.SetOptions(ips)
	keep := false
	for _, ip := range ips {
		if ip == u.ip {
			keep = true
		}
	}
	if keep {
		u.ipSel.SetSelected(u.ip)
	} else {
		u.ipSel.SetSelected(ips[0])
	}
}

func (u *ui) startServer(port int) {
	if u.srv != nil && port == u.port {
		return
	}
	s, err := server.Listen(fmt.Sprintf(":%d", port), u.handler)
	if err != nil {
		dialog.ShowError(fmt.Errorf("could not start on port %d: %w\nTry another port.", port, err), u.win)
		return
	}
	if u.srv != nil {
		u.srv.Close()
	}
	u.srv, u.port = s, port
	u.app.Preferences().SetInt(prefPort, port)
	u.refreshURL()
}

func (u *ui) refreshURL() {
	if u.ip == "" || u.urlLbl == nil {
		return
	}
	url := netaddr.URL(u.ip, u.port)
	u.urlLbl.SetText(url)
	png, err := qrcode.Encode(url, qrcode.Medium, 512)
	if err != nil {
		return
	}
	u.qr.Resource = fyne.NewStaticResource("qr.png", png)
	u.qr.Refresh()
}
