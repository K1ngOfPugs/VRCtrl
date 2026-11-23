package server

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"vrctrl/internal/library"
)

func testServer(t *testing.T) (*httptest.Server, string, int) {
	t.Helper()
	dir := t.TempDir()
	data := []byte("0123456789abcdef")
	if err := os.WriteFile(filepath.Join(dir, "clip.mp4"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	lib := library.New()
	vids, err := lib.Scan(dir)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(New(lib, NewHub(nil)))
	t.Cleanup(srv.Close)
	return srv, vids[0].ID, len(data)
}

func TestVideoRange(t *testing.T) {
	srv, id, size := testServer(t)
	req, _ := http.NewRequest("GET", srv.URL+"/video/"+id, nil)
	req.Header.Set("Range", "bytes=0-1")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != 206 {
		t.Fatalf("status %d", res.StatusCode)
	}
	if got, want := res.Header.Get("Content-Range"), fmt.Sprintf("bytes 0-1/%d", size); got != want {
		t.Fatalf("Content-Range %q want %q", got, want)
	}
	if ct := res.Header.Get("Content-Type"); ct != "video/mp4" {
		t.Fatalf("Content-Type %q", ct)
	}
	if string(body) != "01" {
		t.Fatalf("body %q", body)
	}
	// Open-ended range.
	req.Header.Set("Range", "bytes=10-")
	res2, _ := http.DefaultClient.Do(req)
	b2, _ := io.ReadAll(res2.Body)
	res2.Body.Close()
	if res2.StatusCode != 206 || string(b2) != "abcdef" {
		t.Fatalf("open range: %d %q", res2.StatusCode, b2)
	}
}

func TestVideoUnknownID404(t *testing.T) {
	srv, _, _ := testServer(t)
	for _, p := range []string{"/video/deadbeef0000", "/video/..%2F..%2Fetc%2Fpasswd", "/video/"} {
		res, err := http.Get(srv.URL + p)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != 404 {
			t.Fatalf("%s: status %d", p, res.StatusCode)
		}
	}
}

func TestIndexServed(t *testing.T) {
	srv, _, _ := testServer(t)
	res, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	s := string(b)
	if res.StatusCode != 200 || !strings.Contains(s, "playsinline") || !strings.Contains(s, "apple-mobile-web-app-capable") {
		t.Fatalf("index: %d %q", res.StatusCode, s)
	}
}

func TestManifestServed(t *testing.T) {
	srv, _, _ := testServer(t)
	for _, p := range []string{"/manifest.webmanifest", "/sync.js", "/app.js", "/icon-180.png"} {
		res, err := http.Get(srv.URL + p)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != 200 {
			t.Fatalf("%s: %d", p, res.StatusCode)
		}
	}
	res, _ := http.Get(srv.URL + "/sync.test.mjs")
	res.Body.Close()
	if res.StatusCode != 404 {
		t.Fatalf("test file exposed: %d", res.StatusCode)
	}
}

func TestListenPortBusy(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if s, err := Listen(l.Addr().String(), http.NotFoundHandler()); err == nil {
		s.Close()
		t.Fatal("expected bind error")
	}
	s, err := Listen("127.0.0.1:0", http.NotFoundHandler())
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
}
