package source

import (
	"archive/zip"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"photosorter/internal/media"
	"photosorter/internal/takeout"
)

func writeFiles(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		p := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
}

func writeZip(t *testing.T, path string, files map[string]string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for name, content := range files {
		w, _ := zw.Create(name)
		w.Write([]byte(content))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	f.Close()
}

func sidecarContent(t *testing.T, f File) string {
	t.Helper()
	rc, ok := f.OpenSidecar()
	if !ok {
		t.Fatalf("no sidecar found for %s", f.Name())
	}
	defer rc.Close()
	b, _ := io.ReadAll(rc)
	return string(b)
}

func TestOpen(t *testing.T) {
	dir := t.TempDir()
	writeZip(t, filepath.Join(dir, "takeout-001.zip"), map[string]string{"a.jpg": "a"})
	writeZip(t, filepath.Join(dir, "takeout-002.zip"), map[string]string{"b.jpg": "b"})
	writeZip(t, filepath.Join(dir, "odd[1].zip"), map[string]string{"c.jpg": "c"})
	folder := filepath.Join(dir, "media")
	os.MkdirAll(folder, 0755)
	lib := filepath.Join(dir, "out")

	open := func(folders []string, zips ...string) ([]Source, error) {
		return Open(Inputs{Folders: folders, Zips: zips, Library: lib, Filter: media.DefaultFilter()})
	}
	wantSources := func(t *testing.T, srcs []Source, err error, n int) {
		t.Helper()
		defer CloseAll(srcs)
		if err != nil || len(srcs) != n {
			t.Fatalf("got %d sources, %v; want %d", len(srcs), err, n)
		}
	}

	t.Run("glob expands to every match", func(t *testing.T) {
		srcs, err := open(nil, filepath.Join(dir, "takeout-*.zip"))
		wantSources(t, srcs, err, 2)
	})
	t.Run("existing path with glob characters is taken literally", func(t *testing.T) {
		srcs, err := open(nil, filepath.Join(dir, "odd[1].zip"))
		wantSources(t, srcs, err, 1)
	})
	t.Run("bracket pattern is expanded when no such literal file exists", func(t *testing.T) {
		srcs, err := open(nil, filepath.Join(dir, "takeout-00[12].zip"))
		wantSources(t, srcs, err, 2)
	})
	t.Run("several zip arguments", func(t *testing.T) {
		srcs, err := open(nil, filepath.Join(dir, "takeout-001.zip"), filepath.Join(dir, "odd[1].zip"))
		wantSources(t, srcs, err, 2)
	})
	t.Run("folder and zip together", func(t *testing.T) {
		srcs, err := open([]string{folder}, filepath.Join(dir, "takeout-001.zip"))
		wantSources(t, srcs, err, 2)
	})
	t.Run("unmatched pattern", func(t *testing.T) {
		if _, err := open(nil, filepath.Join(dir, "nothing-*.zip")); err == nil || !strings.Contains(err.Error(), "no zip files match") {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("missing zip", func(t *testing.T) {
		if _, err := open(nil, filepath.Join(dir, "missing.zip")); err == nil {
			t.Error("expected error")
		}
	})
	t.Run("missing input folder", func(t *testing.T) {
		if _, err := open([]string{filepath.Join(dir, "nope")}); err == nil {
			t.Error("expected error")
		}
	})
	t.Run("library inside input folder", func(t *testing.T) {
		_, err := Open(Inputs{Folders: []string{folder}, Library: filepath.Join(folder, "lib"), Filter: media.DefaultFilter()})
		if err == nil || !strings.Contains(err.Error(), "must not contain") {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("a failure leaves no sources open", func(t *testing.T) {
		srcs, err := open([]string{folder}, filepath.Join(dir, "missing.zip"))
		if err == nil || srcs != nil {
			t.Errorf("got %v, %v; want nil sources and an error", srcs, err)
		}
	})
}

func TestOpenZip_ListsMediaOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.zip")
	writeZip(t, path, map[string]string{
		"Takeout/Google Photos/Trip/a.jpg":      "a",
		"Takeout/Google Photos/Trip/a.jpg.json": "{}",
		"Takeout/archive_browser.html":          "html",
		"__MACOSX/Takeout/._a.jpg":              "fork",
	})
	src, err := OpenZip(path, media.DefaultFilter(), takeout.NewSidecarIndex())
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	if files := src.Files(); len(files) != 1 || files[0].Size() != 1 {
		t.Fatalf("Files() = %v", files)
	}
	if src.Name() != "t.zip" {
		t.Errorf("Name() = %q", src.Name())
	}
}

func TestOpenFolder_IgnoresMacosxDirs(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{"a.jpg": "a", "__MACOSX/b.jpg": "b"})
	src, err := OpenFolder(dir, media.DefaultFilter(), takeout.NewSidecarIndex())
	if err != nil || len(src.Files()) != 1 {
		t.Fatalf("got %v, %v; want 1 file", src, err)
	}
}

func TestOpenFolder_ReportsUnreadablePaths(t *testing.T) {
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

	src, err := OpenFolder(dir, media.DefaultFilter(), takeout.NewSidecarIndex())
	if err != nil {
		t.Fatal(err)
	}
	if len(src.Files()) != 1 {
		t.Errorf("got %d files, want 1", len(src.Files()))
	}
	if w := src.Warnings(); len(w) != 1 || !strings.Contains(w[0], "locked") {
		t.Errorf("Warnings() = %v, want one entry naming the locked folder", w)
	}
}

func TestOpenFolder_RespectsFilter(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{"a.jpg": "a", "b.gif": "b", "c.dng": "c"})
	filter, err := media.NewFilter([]string{"dng"}, []string{"gif"})
	if err != nil {
		t.Fatal(err)
	}
	src, err := OpenFolder(dir, filter, takeout.NewSidecarIndex())
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, f := range src.Files() {
		names = append(names, filepath.Base(f.Name()))
	}
	if strings.Join(names, ",") != "a.jpg,c.dng" {
		t.Errorf("files = %v, want a.jpg and c.dng", names)
	}
}

// A sidecar that landed in a different Takeout part than its media must still be found.
func TestSidecarsAcrossZipParts(t *testing.T) {
	dir := t.TempDir()
	album := "Takeout/Google Photos/Trip/"
	writeZip(t, filepath.Join(dir, "takeout-001.zip"), map[string]string{album + "DSC0001.jpg": "photo bytes"})
	writeZip(t, filepath.Join(dir, "takeout-002.zip"), map[string]string{album + "DSC0001.jpg.supplemental-metadata.json": "sidecar"})

	srcs, err := Open(Inputs{Zips: []string{filepath.Join(dir, "takeout-*.zip")}, Library: t.TempDir(), Filter: media.DefaultFilter()})
	if err != nil {
		t.Fatal(err)
	}
	defer CloseAll(srcs)
	files := srcs[0].Files()
	if len(files) != 1 {
		t.Fatalf("got %d media files", len(files))
	}
	if got := sidecarContent(t, files[0]); got != "sidecar" {
		t.Errorf("sidecar = %q", got)
	}
}

func TestSidecarsInFolder(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{"IMG_1(1).jpg": "x", "IMG_1.jpg(1).json": "sidecar"})
	src, err := OpenFolder(dir, media.DefaultFilter(), takeout.NewSidecarIndex())
	if err != nil || len(src.Files()) != 1 {
		t.Fatalf("got %v, %v", src, err)
	}
	if got := sidecarContent(t, src.Files()[0]); got != "sidecar" {
		t.Errorf("sidecar = %q", got)
	}
}
