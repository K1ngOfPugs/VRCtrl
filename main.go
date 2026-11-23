// VRCtrl: pick a video on the laptop, every phone headset plays it in sync.
package main

import (
	"encoding/json"
	"fmt"
	"image/color"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"
	qrcode "github.com/skip2/go-qrcode"

	"vrctrl/internal/library"
	"vrctrl/internal/netaddr"
	"vrctrl/internal/server"
	"vrctrl/internal/thumbs"
)

const (
	prefFolder = "folder"
	prefPort   = "port"
	prefNames  = "headsetNames"
	defPort    = 8080
)

type ui struct {
	app     fyne.App
	win     fyne.Window
	lib     *library.Library
	hub     *server.Hub
	thumbs  *thumbs.Store
	handler http.Handler
	srv     *http.Server

	port int
	ip   string

	// Connect tab
	urlLbl    *widget.Label
	countLbl  *widget.Label
	qr        *canvas.Image
	ipSel     *widget.Select
	portEntry *widget.Entry

	browser       // library.go
	live          // live.go
	thumbsChanged atomic.Bool
}

func main() {
	a := app.NewWithID("io.vrctrl.app")
	u := &ui{app: a, lib: library.New(), hub: server.NewHub(time.Now)}
	u.thumbs = thumbs.New(thumbCacheDir(), func(string) { u.thumbsChanged.Store(true) })
	u.handler = server.New(u.lib, u.hub)
	u.win = a.NewWindow("VRCtrl")
	u.loadNames()
	u.build()
	u.win.Resize(fyne.NewSize(1240, 800))

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
	go func() {
		for range time.Tick(500 * time.Millisecond) {
			fyne.Do(u.tick)
		}
	}()
	u.win.ShowAndRun()
}

func thumbCacheDir() string {
	base, err := os.UserCacheDir()
	if err != nil {
		base = os.TempDir()
	}
	return filepath.Join(base, "VRCtrl", "thumbs")
}

func (u *ui) build() {
	left := u.buildBrowser()
	right := u.buildLive(u.buildConnect())

	split := container.NewHSplit(container.NewPadded(left), container.NewPadded(right))
	split.SetOffset(0.56)
	u.win.SetContent(split)

	// Keyboard: Space = pause/resume, ←/→ = skip 10 s, Esc = clear search.
	u.win.Canvas().SetOnTypedKey(func(e *fyne.KeyEvent) {
		switch e.Name {
		case fyne.KeySpace:
			u.togglePause()
		case fyne.KeyLeft:
			u.seek(-10)
		case fyne.KeyRight:
			u.seek(10)
		case fyne.KeyEscape:
			u.search.SetText("")
		}
	})
}

// buildConnect makes the QR code / address panel.
func (u *ui) buildConnect() fyne.CanvasObject {
	u.qr = canvas.NewImageFromResource(nil)
	u.qr.FillMode = canvas.ImageFillContain
	u.qr.SetMinSize(fyne.NewSize(220, 220))
	u.urlLbl = widget.NewLabelWithStyle("", fyne.TextAlignCenter, fyne.TextStyle{Bold: true})
	u.urlLbl.Selectable = true
	u.countLbl = widget.NewLabelWithStyle("Headsets connected: 0", fyne.TextAlignCenter, fyne.TextStyle{})

	u.ipSel = widget.NewSelect(nil, func(ip string) {
		u.ip = ip
		u.refreshURL()
	})
	refreshIPBtn := widget.NewButtonWithIcon("", themeRefresh(), u.refreshIPs)
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
	steps := widget.NewLabel("On each iPhone: scan with the Camera app, open in Safari, " +
		"then Share → Add to Home Screen and open VRCtrl from the home screen.")
	steps.Wrapping = fyne.TextWrapWord
	return container.NewVScroll(container.NewVBox(
		container.NewCenter(container.NewStack(qrBg, container.NewPadded(u.qr))),
		u.urlLbl,
		u.countLbl,
		steps,
		widget.NewSeparator(),
		widget.NewForm(
			widget.NewFormItem("Address", container.NewBorder(nil, nil, nil, refreshIPBtn, u.ipSel)),
			widget.NewFormItem("Port", container.NewBorder(nil, nil, nil, applyPort, u.portEntry)),
		),
	))
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
	u.qr.Resource = fyne.NewStaticResource("qr-"+url+".png", png)
	u.qr.Refresh()
}

// ---- headset names (kept in preferences, keyed by the phone's ID) ----------

func (u *ui) loadNames() {
	u.names = map[string]string{}
	json.Unmarshal([]byte(u.app.Preferences().String(prefNames)), &u.names)
}

func (u *ui) saveNames() {
	b, _ := json.Marshal(u.names)
	u.app.Preferences().SetString(prefNames, string(b))
}

// headsetName returns the saved name, giving a new phone "Headset N".
func (u *ui) headsetName(id string) string {
	if n, ok := u.names[id]; ok {
		return n
	}
	used := map[string]bool{}
	for _, n := range u.names {
		used[n] = true
	}
	for i := 1; ; i++ {
		n := fmt.Sprintf("Headset %d", i)
		if !used[n] {
			u.names[id] = n
			u.saveNames()
			return n
		}
	}
}
