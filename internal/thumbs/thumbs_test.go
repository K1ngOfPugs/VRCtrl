package thumbs

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestParseDuration(t *testing.T) {
	if d := ParseDuration("  Duration: 01:02:03.50, start: 0.000"); d != 3723.5 {
		t.Fatalf("d = %v", d)
	}
	if d := ParseDuration("nothing"); d != 0 {
		t.Fatalf("d = %v", d)
	}
}

func TestPutAndGetFromDisk(t *testing.T) {
	dir := t.TempDir()
	s := &Store{dir: dir, mem: map[string]entry{}, inflight: map[string]bool{}, failed: map[string]bool{}}
	s.Put("k", []byte("jpg"), 12.5)
	s2 := &Store{dir: dir, mem: map[string]entry{}, inflight: map[string]bool{}, failed: map[string]bool{}}
	j, d, ok := s2.Get("k", "")
	if !ok || string(j) != "jpg" || d != 12.5 {
		t.Fatalf("got %q %v %v", j, d, ok)
	}
	s2.Put("k", []byte("other"), 0) // never overwrites
	if j, _, _ := s2.Get("k", ""); string(j) != "jpg" {
		t.Fatalf("overwritten: %q", j)
	}
}

func TestGenerateWithFFmpeg(t *testing.T) {
	ff := FindFFmpeg()
	if ff == "" {
		t.Skip("ffmpeg not installed")
	}
	dir := t.TempDir()
	vid := filepath.Join(dir, "v.mp4")
	if out, err := exec.Command(ff, "-v", "error", "-f", "lavfi", "-i", "testsrc=size=640x180:rate=10:duration=2",
		"-pix_fmt", "yuv420p", vid).CombinedOutput(); err != nil {
		t.Skipf("cannot make test video: %v %s", err, out)
	}
	ready := make(chan string, 1)
	s := New(filepath.Join(dir, "cache"), func(k string) { ready <- k })
	fi, _ := os.Stat(vid)
	key := Key(vid, fi.Size(), fi.ModTime())
	if _, _, ok := s.Get(key, vid); ok {
		t.Fatal("unexpected cache hit")
	}
	select {
	case <-ready:
	case <-time.After(20 * time.Second):
		t.Fatal("no thumbnail")
	}
	j, d, ok := s.Get(key, vid)
	if !ok || len(j) < 100 || j[0] != 0xFF || j[1] != 0xD8 || d < 1.9 || d > 2.1 {
		t.Fatalf("thumb %d bytes, dur %v, ok %v", len(j), d, ok)
	}
}
