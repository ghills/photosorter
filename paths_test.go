package main

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPathsOverlap(t *testing.T) {
	root := t.TempDir()
	mk := func(rel string) string {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(p, 0755); err != nil {
			t.Fatal(err)
		}
		return p
	}
	lib := mk("lib")
	inner := mk("lib/2023")
	sibling := mk("libx") // shares a string prefix with "lib" but is a different folder
	other := mk("other")

	link := filepath.Join(root, "link-to-inner")
	if err := os.Symlink(inner, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	tests := []struct {
		name string
		a, b string
		want bool
	}{
		{"same folder", lib, lib, true},
		{"child inside parent", lib, inner, true},
		{"parent inside child (argument order)", inner, lib, true},
		{"not-yet-created child inside parent", lib, filepath.Join(lib, "new", "deeper"), true},
		{"not-yet-created folder elsewhere", lib, filepath.Join(other, "new"), false},
		{"string-prefix sibling", lib, sibling, false},
		{"unrelated folders", lib, other, false},
		{"symlink into the folder", lib, link, true},
		{"relative spelling of same folder", lib, filepath.Join(lib, "2023", ".."), true},
		{"neither exists", filepath.Join(root, "x"), filepath.Join(root, "y"), false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := pathsOverlap(tc.a, tc.b); got != tc.want {
				t.Errorf("pathsOverlap(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
			}
		})
	}
}

func TestIsMedia_UsesPathComponents(t *testing.T) {
	if IsMedia("Takeout/__MACOSX/Trip/a.jpg") {
		t.Error("file inside a __MACOSX folder should be ignored")
	}
	if !IsMedia("Takeout/NotMACOSX/a.jpg") || !IsMedia("a__MACOSX_b.jpg") {
		t.Error("names merely containing the text should not be ignored")
	}
	root := "/photos/__MACOSX_backup"
	if !IsMedia(relMediaPath(root, root+"/a.jpg")) {
		t.Error("the scan root's own name must not affect the result")
	}
}

func TestLocalFolderSource_IgnoresMacosxDirs(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{"a.jpg": "a", "__MACOSX/b.jpg": "b"})
	files, err := NewLocalFolderSource(dir, nil).MediaFiles()
	if err != nil || len(files) != 1 {
		t.Fatalf("got %d files, %v; want 1", len(files), err)
	}
}

func TestLocalFolderSource_ReportsUnreadablePaths(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("permissions are not enforced for root")
	}
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{"ok/a.jpg": "a", "locked/b.jpg": "b"})
	locked := filepath.Join(dir, "locked")
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(locked, 0755)

	src := NewLocalFolderSource(dir, nil)
	files, _ := src.MediaFiles()
	if len(files) != 1 {
		t.Errorf("got %d files, want 1", len(files))
	}
	if w := src.Warnings(); len(w) != 1 || !strings.Contains(w[0], "locked") {
		t.Errorf("Warnings() = %v, want one entry naming the locked folder", w)
	}
}

// failingMedia/failingSource let Run hit a per-file error without relying on filesystem permissions.
type failingMedia struct{ name string }

func (f failingMedia) Name() string                       { return f.name }
func (f failingMedia) Open() (io.ReadCloser, error)       { return nil, errors.New("boom") }
func (f failingMedia) OpenSidecar() (io.ReadCloser, bool) { return nil, false }

type failingSource struct{ files []MediaFile }

func (s failingSource) Name() string                     { return "failing" }
func (s failingSource) MediaFiles() ([]MediaFile, error) { return s.files, nil }
func (s failingSource) Close() error                     { return nil }

func TestProcessor_RunReturnsFilesFailedError(t *testing.T) {
	db, _, err := InitDB(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	src := failingSource{files: []MediaFile{failingMedia{"a.jpg"}, failingMedia{"b.jpg"}}}
	err = NewProcessor([]InputSource{src}, t.TempDir(), db, 2, 0, false, nil).Run(context.Background())
	var failed *FilesFailedError
	if !errors.As(err, &failed) || failed.Count != 2 {
		t.Errorf("Run error = %v, want FilesFailedError{Count: 2}", err)
	}
}

// The -tz flag works by replacing time.Local, so every date source must follow it.
func TestTimezoneOverrideAffectsFolderDates(t *testing.T) {
	orig := time.Local
	defer func() { time.Local = orig }()

	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"a.jpg":      "x",
		"a.jpg.json": `{"photoTakenTime":{"timestamp":"1672531200"}}`, // 2023-01-01T00:00:00Z
	})
	files, _ := NewLocalFolderSource(dir, nil).MediaFiles()

	for _, tc := range []struct{ zone, want string }{
		{"UTC", "2023-01-01"},
		{"Pacific/Auckland", "2023-01-01"}, // 13:00 on the 1st
		{"Pacific/Honolulu", "2022-12-31"}, // 14:00 on the 31st
	} {
		loc, err := time.LoadLocation(tc.zone)
		if err != nil {
			t.Fatal(err)
		}
		time.Local = loc
		tm, origin := FindCaptureDate(files[0])
		if origin != "json_taken" || tm.Format("2006-01-02") != tc.want {
			t.Errorf("%s: got %s via %s, want %s", tc.zone, tm.Format("2006-01-02"), origin, tc.want)
		}
	}
}
