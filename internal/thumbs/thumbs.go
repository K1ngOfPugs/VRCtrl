// Package thumbs makes and caches video thumbnails and durations.
//
// Thumbnails come from ffmpeg when it is installed. Without ffmpeg, the app
// stores a frame a headset sent while playing the video (Put), so thumbnails
// fill in as videos get played.
package thumbs

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"sync"
	"time"
)

// Key identifies one version of a file: a change of size or modification
// time makes a new thumbnail.
func Key(absPath string, size int64, mod time.Time) string {
	sum := sha1.Sum([]byte(fmt.Sprintf("%s|%d|%d", absPath, size, mod.UnixNano())))
	return hex.EncodeToString(sum[:])[:20]
}

type entry struct {
	jpeg []byte
	dur  float64
}

// Store caches thumbnails in memory and on disk.
type Store struct {
	dir     string
	ffmpeg  string
	onReady func(key string)

	mu       sync.Mutex
	mem      map[string]entry
	inflight map[string]bool
	failed   map[string]bool
	queue    chan job
}

type job struct{ key, path string }

// New makes a store that caches in dir. onReady (may be nil) is called from a
// background goroutine whenever a new thumbnail becomes available.
func New(dir string, onReady func(key string)) *Store {
	os.MkdirAll(dir, 0o755)
	s := &Store{dir: dir, ffmpeg: FindFFmpeg(), onReady: onReady,
		mem: map[string]entry{}, inflight: map[string]bool{}, failed: map[string]bool{},
		queue: make(chan job, 4096)}
	if s.ffmpeg != "" {
		for i := 0; i < 2; i++ {
			go s.worker()
		}
	}
	return s
}

// HasFFmpeg reports whether thumbnails can be generated automatically.
func (s *Store) HasFFmpeg() bool { return s.ffmpeg != "" }

// FindFFmpeg looks for ffmpeg next to the program, on PATH, and in common
// install locations (desktop apps on macOS don't get the shell's PATH).
func FindFFmpeg() string {
	name := "ffmpeg"
	if runtime.GOOS == "windows" {
		name = "ffmpeg.exe"
	}
	var cands []string
	if exe, err := os.Executable(); err == nil {
		cands = append(cands, filepath.Join(filepath.Dir(exe), name))
	}
	if p, err := exec.LookPath(name); err == nil {
		cands = append(cands, p)
	}
	cands = append(cands, "/opt/homebrew/bin/ffmpeg", "/usr/local/bin/ffmpeg", "/usr/bin/ffmpeg")
	for _, c := range cands {
		if fi, err := os.Stat(c); err == nil && fi.Mode().IsRegular() {
			return c
		}
	}
	return ""
}

// Get returns the cached thumbnail (JPEG) and duration (seconds, 0 if
// unknown) for key. When nothing is cached and ffmpeg is available it starts
// generating one from absPath in the background and returns ok=false.
func (s *Store) Get(key, absPath string) (jpeg []byte, dur float64, ok bool) {
	s.mu.Lock()
	if e, hit := s.mem[key]; hit {
		s.mu.Unlock()
		return e.jpeg, e.dur, e.jpeg != nil
	}
	s.mu.Unlock()

	jpeg, _ = os.ReadFile(filepath.Join(s.dir, key+".jpg"))
	if b, err := os.ReadFile(filepath.Join(s.dir, key+".dur")); err == nil {
		dur, _ = strconv.ParseFloat(string(b), 64)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if jpeg != nil {
		s.mem[key] = entry{jpeg, dur}
		return jpeg, dur, true
	}
	if _, hit := s.mem[key]; hit { // filled while we were reading the disk
		e := s.mem[key]
		return e.jpeg, e.dur, e.jpeg != nil
	}
	if s.ffmpeg != "" && absPath != "" && !s.inflight[key] && !s.failed[key] {
		select {
		case s.queue <- job{key, absPath}:
			s.inflight[key] = true
		default:
			return nil, dur, false // queue full: try again next time
		}
	}
	s.mem[key] = entry{nil, dur} // remember the miss; Put fills it in
	return nil, dur, false
}

// Put stores a thumbnail (e.g. a frame from a headset) unless one exists.
// dur may be 0 if unknown.
func (s *Store) Put(key string, jpeg []byte, dur float64) {
	s.mu.Lock()
	if e, hit := s.mem[key]; hit && e.jpeg != nil {
		s.mu.Unlock()
		return
	}
	s.mem[key] = entry{jpeg, dur}
	s.mu.Unlock()
	os.WriteFile(filepath.Join(s.dir, key+".jpg"), jpeg, 0o644)
	if dur > 0 {
		os.WriteFile(filepath.Join(s.dir, key+".dur"), []byte(strconv.FormatFloat(dur, 'f', 3, 64)), 0o644)
	}
	if s.onReady != nil {
		s.onReady(key)
	}
}

func (s *Store) worker() {
	for j := range s.queue {
		jpeg, dur, err := s.generate(j.path)
		s.mu.Lock()
		delete(s.inflight, j.key)
		if err != nil || len(jpeg) == 0 {
			s.failed[j.key] = true
			if dur > 0 {
				s.mem[j.key] = entry{nil, dur}
			}
			s.mu.Unlock()
			continue
		}
		s.mu.Unlock()
		s.Put(j.key, jpeg, dur)
	}
}

var durRe = regexp.MustCompile(`Duration: (\d+):(\d+):(\d+(?:\.\d+)?)`)

// ParseDuration reads the "Duration: HH:MM:SS.ss" line ffmpeg prints.
func ParseDuration(ffmpegLog string) float64 {
	m := durRe.FindStringSubmatch(ffmpegLog)
	if m == nil {
		return 0
	}
	h, _ := strconv.Atoi(m[1])
	mi, _ := strconv.Atoi(m[2])
	sec, _ := strconv.ParseFloat(m[3], 64)
	return float64(h*3600+mi*60) + sec
}

// generate grabs one frame of the left eye, a few seconds in (or the first
// frame of a very short clip).
func (s *Store) generate(path string) ([]byte, float64, error) {
	var dur float64
	for _, at := range []string{"5", "0"} {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		cmd := exec.CommandContext(ctx, s.ffmpeg, "-hide_banner", "-nostdin", "-ss", at, "-i", path,
			"-frames:v", "1", "-vf", "crop=iw/2:ih:0:0,scale=320:-2", "-q:v", "5",
			"-f", "image2", "-c:v", "mjpeg", "pipe:1")
		hideWindow(cmd)
		var out, log bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &log
		err := cmd.Run()
		cancel()
		if d := ParseDuration(log.String()); d > 0 {
			dur = d
		}
		if err == nil && out.Len() > 0 {
			return out.Bytes(), dur, nil
		}
	}
	return nil, dur, fmt.Errorf("ffmpeg could not read %s", filepath.Base(path))
}
