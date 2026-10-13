package library

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"photosorter/internal/media"
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

func hashOf(content string) string { return HashBytes([]byte(content)) }

func syncOpts() SyncOptions { return SyncOptions{Workers: 4, Filter: media.DefaultFilter()} }

func TestHash_MatchesHashBytes(t *testing.T) {
	content := bytes.Repeat([]byte("abc"), 10000)
	got, err := Hash(bytes.NewReader(content))
	if err != nil || got != HashBytes(content) {
		t.Errorf("Hash = %s, %v; HashBytes = %s", got, err, HashBytes(content))
	}
	if len(got) != 16 {
		t.Errorf("hash %q should be 16 hex digits", got)
	}
}

func TestCountMedia(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"a.jpg":                    "a",
		"2023-01-01/b.PNG":         "b", // extension matching is case-insensitive
		"Albums/Trip/c.mp4":        "c",
		"__MACOSX/Trip/d.jpg":      "inside a __MACOSX folder",
		"notes.txt":                "not media",
		"2023-01-01/b.jpg.json":    "{}",
		"2023-01-01/._e.jpg":       "resource fork file",
		DBFileName:                 "cache",
		"photosorter_2023.log":     "log",
		"2023-01-01/.tmp-x.tmp":    "temp",
		"Albums/Trip/Nested/f.m4v": "f",
	})
	if got := CountMedia(dir, media.DefaultFilter()); got != 4 {
		t.Errorf("CountMedia = %d, want 4", got)
	}
	noVideo, _ := media.NewFilter(nil, []string{"mp4", "m4v"})
	if got := CountMedia(dir, noVideo); got != 2 {
		t.Errorf("CountMedia without videos = %d, want 2", got)
	}
}

func TestCountMedia_EmptyAndMissing(t *testing.T) {
	if got := CountMedia(t.TempDir(), media.DefaultFilter()); got != 0 {
		t.Errorf("empty dir: got %d, want 0", got)
	}
	if got := CountMedia(filepath.Join(t.TempDir(), "missing"), media.DefaultFilter()); got != 0 {
		t.Errorf("missing dir: got %d, want 0", got)
	}
}

func TestSync_RecordsHashes(t *testing.T) {
	lib := t.TempDir()
	writeFiles(t, lib, map[string]string{
		"2023-01-01/a.jpg":  "content a",
		"Albums/Trip/b.mp4": "content b",
		"2023-01-02/c.jpg":  "content a", // same bytes as a.jpg: one hash, not two
		"readme.txt":        "ignored",
	})

	dbDir := t.TempDir()
	db, err := Open(dbDir)
	if err != nil {
		t.Fatal(err)
	}
	res, err := Sync(context.Background(), lib, db, syncOpts())
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if res.Hashed != 3 || len(res.Failed) != 0 || len(res.Unreadable) != 0 {
		t.Errorf("result = %+v, want 3 hashed", res)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Reopen so we're checking what actually persisted, not the in-memory cache.
	db2 := openDB(t, dbDir)
	if n := count(t, db2); n != 2 {
		t.Errorf("persisted %d hashes, want 2 (duplicate content shares a hash, non-media ignored)", n)
	}
	for _, content := range []string{"content a", "content b"} {
		if ok, err := db2.Has(hashOf(content)); err != nil || !ok {
			t.Errorf("hash for %q missing (has=%v, err=%v)", content, ok, err)
		}
	}
	if ok, _ := db2.Has(hashOf("ignored")); ok {
		t.Error("non-media file was hashed")
	}
}

func TestSync_EmptyLibrary(t *testing.T) {
	db := openDB(t, t.TempDir())
	if res, err := Sync(context.Background(), t.TempDir(), db, syncOpts()); err != nil || res.Hashed != 0 {
		t.Errorf("empty library: %+v, %v", res, err)
	}
}

func TestSync_StopsOnCancel(t *testing.T) {
	lib := t.TempDir()
	writeFiles(t, lib, map[string]string{"a.jpg": "a", "b.jpg": "b"})
	db := openDB(t, t.TempDir())

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Sync(ctx, lib, db, syncOpts()); !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
	if ok, _ := db.Has(hashOf("a")); ok {
		t.Error("files were hashed after cancellation")
	}
}

func TestSync_StopsOnDBFailure(t *testing.T) {
	lib := t.TempDir()
	writeFiles(t, lib, map[string]string{"a.jpg": "a"})
	db := openDB(t, t.TempDir())
	db.setErr(errors.New("disk full"))

	if _, err := Sync(context.Background(), lib, db, syncOpts()); err == nil {
		t.Error("expected the database failure to be returned")
	}
}

func TestSync_ReportsUnhashableFiles(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("permissions are not enforced for root")
	}
	lib := t.TempDir()
	writeFiles(t, lib, map[string]string{"ok.jpg": "ok", "locked.jpg": "locked"})
	if err := os.Chmod(filepath.Join(lib, "locked.jpg"), 0); err != nil {
		t.Fatal(err)
	}
	db := openDB(t, t.TempDir())
	res, err := Sync(context.Background(), lib, db, syncOpts())
	if err != nil {
		t.Fatal(err)
	}
	if res.Hashed != 1 || len(res.Failed) != 1 || !strings.Contains(res.Failed[0], "locked.jpg") {
		t.Errorf("result = %+v, want one hashed and locked.jpg failed", res)
	}
}

func TestSync_ManyFilesConcurrently(t *testing.T) {
	lib := t.TempDir()
	files := map[string]string{}
	for i := range 300 {
		files[fmt.Sprintf("d%d/img_%d.jpg", i%7, i)] = fmt.Sprintf("content %d", i)
	}
	writeFiles(t, lib, files)

	dbDir := t.TempDir()
	db, err := Open(dbDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Sync(context.Background(), lib, db, SyncOptions{Workers: 8, Filter: media.DefaultFilter()}); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if n := count(t, openDB(t, dbDir)); n != 300 {
		t.Errorf("persisted %d hashes, want 300", n)
	}
}
