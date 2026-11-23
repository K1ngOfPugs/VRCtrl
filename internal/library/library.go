// Package library scans a folder tree for videos and maps them to opaque IDs
// so that clients never handle file paths.
package library

import (
	"crypto/sha1"
	"encoding/hex"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// MaxDepth limits how deep Scan descends into subfolders.
const MaxDepth = 8

// Video is one file in the scanned folder tree.
type Video struct {
	ID       string
	Name     string
	RelPath  string // OS-specific path relative to the scanned folder
	Folder   string // slash-separated folder relative to the root; "" = root
	Playable bool   // iPhone Safari can play it natively
	MIME     string
	Size     int64
	ModTime  time.Time
}

// Folder is a subfolder that contains videos somewhere beneath it.
type Folder struct {
	Path  string // slash-separated, relative to the root
	Name  string
	Count int // videos anywhere beneath it
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

// Scan lists videos in dir and its subfolders (hidden entries skipped),
// sorted by folder then name.
func (l *Library) Scan(dir string) ([]Video, error) {
	if _, err := os.ReadDir(dir); err != nil {
		return nil, err
	}
	var vids []Video
	filepath.WalkDir(dir, func(p string, e fs.DirEntry, err error) error {
		if err != nil {
			if e != nil && e.IsDir() && p != dir {
				return fs.SkipDir // unreadable subfolder: skip it, keep going
			}
			return nil
		}
		name := e.Name()
		if p == dir {
			return nil
		}
		rel, _ := filepath.Rel(dir, p)
		if strings.HasPrefix(name, ".") {
			if e.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if e.IsDir() {
			if strings.Count(filepath.ToSlash(rel), "/")+1 >= MaxDepth {
				return fs.SkipDir
			}
			return nil
		}
		ext := strings.ToLower(filepath.Ext(name))
		mime, ok := playableExt[ext]
		if !ok && !otherVideoExt[ext] {
			return nil
		}
		// Resolve symlinks; skip anything that isn't a regular file.
		fi, err := os.Stat(p)
		if err != nil || !fi.Mode().IsRegular() {
			return nil
		}
		folder := path.Dir(filepath.ToSlash(rel))
		if folder == "." {
			folder = ""
		}
		vids = append(vids, Video{
			ID: ID(dir, rel), Name: name, RelPath: rel, Folder: folder,
			Playable: ok, MIME: mime, Size: fi.Size(), ModTime: fi.ModTime(),
		})
		return nil
	})
	sort.Slice(vids, func(i, j int) bool {
		if vids[i].Folder != vids[j].Folder {
			return lessFold(vids[i].Folder, vids[j].Folder)
		}
		return lessFold(vids[i].Name, vids[j].Name)
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

func lessFold(a, b string) bool {
	la, lb := strings.ToLower(a), strings.ToLower(b)
	if la == lb {
		return a < b
	}
	return la < lb
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

// AbsPath returns the absolute path of any scanned video, playable or not.
func (l *Library) AbsPath(v Video) string {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return filepath.Join(l.dir, v.RelPath)
}

// Dir returns the folder of the most recent successful scan.
func (l *Library) Dir() string {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.dir
}

// Browse returns the immediate subfolders of folder (each with the number of
// videos beneath it) and the videos directly inside it. vids must be sorted
// as Scan returns them.
func Browse(vids []Video, folder string) ([]Folder, []Video) {
	prefix := ""
	if folder != "" {
		prefix = folder + "/"
	}
	idx := map[string]int{}
	var folders []Folder
	var here []Video
	for _, v := range vids {
		if v.Folder == folder {
			here = append(here, v)
			continue
		}
		if !strings.HasPrefix(v.Folder, prefix) {
			continue
		}
		child := strings.SplitN(v.Folder[len(prefix):], "/", 2)[0]
		i, ok := idx[child]
		if !ok {
			i = len(folders)
			idx[child] = i
			folders = append(folders, Folder{Path: prefix + child, Name: child})
		}
		folders[i].Count++
	}
	sort.Slice(folders, func(i, j int) bool { return lessFold(folders[i].Name, folders[j].Name) })
	return folders, here
}

// Search returns videos anywhere in the tree whose name or folder contains
// every word of query (case-insensitive).
func Search(vids []Video, query string) []Video {
	words := strings.Fields(strings.ToLower(query))
	var out []Video
	for _, v := range vids {
		hay := strings.ToLower(v.Folder + "/" + v.Name)
		match := true
		for _, w := range words {
			if !strings.Contains(hay, w) {
				match = false
				break
			}
		}
		if match {
			out = append(out, v)
		}
	}
	return out
}
