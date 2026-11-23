package library

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func touch(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestScanListsAndSorts(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"b.MP4", "a.mov", "c.mkv", "notes.txt", ".hidden.mp4"} {
		touch(t, dir, n)
	}
	if err := os.Mkdir(filepath.Join(dir, "sub.mp4"), 0o755); err != nil {
		t.Fatal(err)
	}
	vids, err := New().Scan(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	var playable []bool
	for _, v := range vids {
		names = append(names, v.Name)
		playable = append(playable, v.Playable)
	}
	if !reflect.DeepEqual(names, []string{"a.mov", "b.MP4", "c.mkv"}) {
		t.Fatalf("names = %v", names)
	}
	if !reflect.DeepEqual(playable, []bool{true, true, false}) {
		t.Fatalf("playable = %v", playable)
	}
	if vids[0].MIME != "video/quicktime" || vids[1].MIME != "video/mp4" {
		t.Fatalf("mimes = %q %q", vids[0].MIME, vids[1].MIME)
	}
}

func TestIDStableAcrossRescan(t *testing.T) {
	dir := t.TempDir()
	touch(t, dir, "one.mp4")
	l := New()
	v1, _ := l.Scan(dir)
	touch(t, dir, "aaa.mp4")
	v2, _ := l.Scan(dir)
	var id2 string
	for _, v := range v2 {
		if v.Name == "one.mp4" {
			id2 = v.ID
		}
	}
	if v1[0].ID == "" || v1[0].ID != id2 {
		t.Fatalf("id changed: %q -> %q", v1[0].ID, id2)
	}
	if len(id2) != 12 {
		t.Fatalf("id length %d", len(id2))
	}
	p, mime, ok := l.Path(id2)
	if !ok || p != filepath.Join(dir, "one.mp4") || mime != "video/mp4" {
		t.Fatalf("Path = %q %q %v", p, mime, ok)
	}
}

func TestPathRejectsUnknownAndUnplayable(t *testing.T) {
	dir := t.TempDir()
	touch(t, dir, "c.mkv")
	l := New()
	vids, _ := l.Scan(dir)
	for _, id := range []string{"deadbeef0000", "../x", "", vids[0].ID} {
		if _, _, ok := l.Path(id); ok {
			t.Fatalf("Path(%q) should fail", id)
		}
	}
}

func TestScanMissingDir(t *testing.T) {
	if _, err := New().Scan(filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Fatal("expected error")
	}
}

// A video's ID must not carry over to a different folder holding a file with
// the same name, or a playing phone would get bytes from a different file.
func TestIDDiffersAcrossFolders(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	touch(t, a, "same.mp4")
	touch(t, b, "same.mp4")
	l := New()
	va, _ := l.Scan(a)
	vb, _ := l.Scan(b)
	if va[0].ID == vb[0].ID {
		t.Fatal("same ID in different folders")
	}
	if _, _, ok := l.Path(va[0].ID); ok {
		t.Fatal("old folder's ID still resolves")
	}
}

func TestScanSubfolders(t *testing.T) {
	dir := t.TempDir()
	for _, d := range []string{"Beach/Day", "Beach/Night", ".secret", "empty"} {
		if err := os.MkdirAll(filepath.Join(dir, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	touch(t, dir, "root.mp4")
	touch(t, dir, filepath.Join("Beach", "waves.mp4"))
	touch(t, dir, filepath.Join("Beach", "Day", "sun.mov"))
	touch(t, dir, filepath.Join("Beach", "Night", "moon.mp4"))
	touch(t, dir, filepath.Join(".secret", "hidden.mp4"))
	l := New()
	vids, err := l.Scan(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(vids) != 4 {
		t.Fatalf("got %d videos: %+v", len(vids), vids)
	}
	var sun Video
	for _, v := range vids {
		if v.Name == "sun.mov" {
			sun = v
		}
	}
	if sun.Folder != "Beach/Day" {
		t.Fatalf("folder = %q", sun.Folder)
	}
	if p, _, ok := l.Path(sun.ID); !ok || p != filepath.Join(dir, "Beach", "Day", "sun.mov") {
		t.Fatalf("Path = %q %v", p, ok)
	}

	folders, here := Browse(vids, "")
	if len(here) != 1 || here[0].Name != "root.mp4" {
		t.Fatalf("root videos = %+v", here)
	}
	if !reflect.DeepEqual(folders, []Folder{{Path: "Beach", Name: "Beach", Count: 3}}) {
		t.Fatalf("root folders = %+v", folders)
	}
	folders, here = Browse(vids, "Beach")
	if len(here) != 1 || here[0].Name != "waves.mp4" {
		t.Fatalf("Beach videos = %+v", here)
	}
	want := []Folder{{Path: "Beach/Day", Name: "Day", Count: 1}, {Path: "Beach/Night", Name: "Night", Count: 1}}
	if !reflect.DeepEqual(folders, want) {
		t.Fatalf("Beach folders = %+v", folders)
	}

	got := Search(vids, "beach MOON")
	if len(got) != 1 || got[0].Name != "moon.mp4" {
		t.Fatalf("search = %+v", got)
	}
	if n := len(Search(vids, "")); n != 4 {
		t.Fatalf("empty search = %d", n)
	}
}
