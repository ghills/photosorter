package main

import (
	"context"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/cespare/xxhash/v2"
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

func hashOf(content string) string {
	h := xxhash.New()
	h.Write([]byte(content))
	return hex.EncodeToString(h.Sum(nil))
}

func TestCountMediaFiles(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"a.jpg":                    "a",
		"2023-01-01/b.PNG":         "b", // extension matching is case-insensitive
		"Albums/Trip/c.mp4":        "c",
		"notes.txt":                "not media",
		"2023-01-01/b.jpg.json":    "{}",
		"2023-01-01/._e.jpg":       "resource fork file",
		".photosorter_cache.db":    "cache",
		"photosorter_2023.log":     "log",
		"2023-01-01/.tmp-x.tmp":    "temp",
		"Albums/Trip/Nested/f.m4v": "f",
	})

	got, err := CountMediaFiles(dir)
	if err != nil {
		t.Fatal(err)
	}
	if want := 4; got != want {
		t.Errorf("CountMediaFiles = %d, want %d", got, want)
	}
}

func TestCountMediaFiles_EmptyAndMissing(t *testing.T) {
	if got, err := CountMediaFiles(t.TempDir()); err != nil || got != 0 {
		t.Errorf("empty dir: got %d, %v; want 0, nil", got, err)
	}
	// Unreadable roots are skipped rather than failing the startup check.
	if got, _ := CountMediaFiles(filepath.Join(t.TempDir(), "missing")); got != 0 {
		t.Errorf("missing dir: got %d, want 0", got)
	}
}

func TestSyncFolderWithDB_RecordsHashes(t *testing.T) {
	lib := t.TempDir()
	writeFiles(t, lib, map[string]string{
		"2023-01-01/a.jpg":  "content a",
		"Albums/Trip/b.mp4": "content b",
		"2023-01-02/c.jpg":  "content a", // same bytes as a.jpg: one hash, not two
		"readme.txt":        "ignored",
	})

	dbDir := t.TempDir()
	db, _, err := InitDB(dbDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := SyncFolderWithDB(context.Background(), lib, db); err != nil {
		t.Fatalf("SyncFolderWithDB: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Reopen so we're checking what actually persisted, not the in-memory cache.
	db2, count, err := InitDB(dbDir)
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()
	if count != 2 {
		t.Errorf("persisted %d hashes, want 2 (duplicate content shares a hash, non-media ignored)", count)
	}
	for _, content := range []string{"content a", "content b"} {
		if ok, err := db2.Exists(hashOf(content)); err != nil || !ok {
			t.Errorf("hash for %q missing (exists=%v, err=%v)", content, ok, err)
		}
	}
	if ok, _ := db2.Exists(hashOf("ignored")); ok {
		t.Error("non-media file was hashed")
	}
}

// Syncing must make the pipeline treat already-organized files as duplicates.
func TestSyncFolderWithDB_PreventsReimport(t *testing.T) {
	root := t.TempDir()
	lib := filepath.Join(root, "lib")
	writeFiles(t, lib, map[string]string{"2020-05-05/existing.jpg": "already here"})
	writeZip(t, filepath.Join(root, "t.zip"), map[string]string{"IMG_20230101.jpg": "already here"})

	db, _, err := InitDB(lib)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := SyncFolderWithDB(context.Background(), lib, db); err != nil {
		t.Fatal(err)
	}

	src, err := NewZipSource(filepath.Join(root, "t.zip"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	if err := NewProcessor([]InputSource{src}, lib, db, 1, 0, false, nil).Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(lib, "2023-01-01")); !os.IsNotExist(err) {
		t.Error("file already in the library was imported again")
	}
}

func TestSyncFolderWithDB_EmptyLibrary(t *testing.T) {
	db, _, err := InitDB(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := SyncFolderWithDB(context.Background(), t.TempDir(), db); err != nil {
		t.Errorf("empty library: %v", err)
	}
}

func TestSyncFolderWithDB_StopsOnCancel(t *testing.T) {
	lib := t.TempDir()
	writeFiles(t, lib, map[string]string{"a.jpg": "a", "b.jpg": "b"})
	db, _, err := InitDB(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err = SyncFolderWithDB(ctx, lib, db)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
	if ok, _ := db.Exists(hashOf("a")); ok {
		t.Error("files were hashed after cancellation")
	}
}

func TestSyncFolderWithDB_StopsOnDBFailure(t *testing.T) {
	lib := t.TempDir()
	writeFiles(t, lib, map[string]string{"a.jpg": "a"})
	db, _, err := InitDB(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.setErr(errors.New("disk full"))

	if err := SyncFolderWithDB(context.Background(), lib, db); err == nil {
		t.Error("expected the database failure to be returned")
	}
}

func TestAppendToDB(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{"x.jpg": "payload"})
	db, _, err := InitDB(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if err := appendToDB(filepath.Join(dir, "x.jpg"), db); err != nil {
		t.Fatal(err)
	}
	if ok, _ := db.Exists(hashOf("payload")); !ok {
		t.Error("hash not recorded")
	}
	if err := appendToDB(filepath.Join(dir, "missing.jpg"), db); err == nil {
		t.Error("expected error for a missing file")
	}
}
