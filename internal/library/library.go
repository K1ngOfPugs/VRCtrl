// Package library scans a folder for videos and maps them to opaque IDs so
// that clients never handle file paths.
package library

import (
	"crypto/sha1"
	"encoding/hex"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// Video is one file in the scanned folder.
type Video struct {
	ID       string
	Name     string
	RelPath  string
	Playable bool // iPhone Safari can play it natively
	MIME     string
}

var playableExt = map[string]string{
	".mp4": "video/mp4",
	".m4v": "video/mp4",
	".mov": "video/quicktime",
}

var otherVideoExt = map[string]bool{
	".mkv": true, ".avi": true, ".webm": true, ".wmv": true, ".flv": true,
}

// Library holds the result of the most recent scan.
type Library struct {
	mu   sync.RWMutex
	dir  string
	byID map[string]Video
}

func New() *Library { return &Library{byID: map[string]Video{}} }

// ID returns the stable ID for a file in a folder. The folder is part of the
// hash so the same file name in another folder never reuses an ID.
func ID(dir, relPath string) string {
	sum := sha1.Sum([]byte(filepath.Join(dir, relPath)))
	return hex.EncodeToString(sum[:])[:12]
}

// Scan lists videos directly inside dir (non-recursive), sorted by name.
func (l *Library) Scan(dir string) ([]Video, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var vids []Video
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") || e.IsDir() {
			continue
		}
		if !e.Type().IsRegular() {
			// Resolve symlinks; skip anything that isn't a regular file.
			fi, err := os.Stat(filepath.Join(dir, name))
			if err != nil || !fi.Mode().IsRegular() {
				continue
			}
		}
		ext := strings.ToLower(filepath.Ext(name))
		mime, ok := playableExt[ext]
		if !ok && !otherVideoExt[ext] {
			continue
		}
		vids = append(vids, Video{ID: ID(dir, name), Name: name, RelPath: name, Playable: ok, MIME: mime})
	}
	sort.Slice(vids, func(i, j int) bool {
		a, b := strings.ToLower(vids[i].Name), strings.ToLower(vids[j].Name)
		if a == b {
			return vids[i].Name < vids[j].Name
		}
		return a < b
	})
	byID := make(map[string]Video, len(vids))
	for _, v := range vids {
		byID[v.ID] = v
	}
	l.mu.Lock()
	l.dir, l.byID = dir, byID
	l.mu.Unlock()
	return vids, nil
}

// Path resolves a playable video's ID to its absolute path and MIME type.
func (l *Library) Path(id string) (absPath, mime string, ok bool) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	v, found := l.byID[id]
	if !found || !v.Playable {
		return "", "", false
	}
	return filepath.Join(l.dir, v.RelPath), v.MIME, true
}

// Dir returns the folder of the most recent successful scan.
func (l *Library) Dir() string {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.dir
}
