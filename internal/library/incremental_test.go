package library

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"
)

// syncLib syncs the library at dir and returns the result.
func syncLib(t *testing.T, dir string, db *DB, opts SyncOptions) SyncResult {
	t.Helper()
	if opts.Workers == 0 {
		opts.Workers = 4
	}
	res, err := Sync(context.Background(), scan(t, dir), db, opts)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func storedPaths(t *testing.T, db *DB) []string {
	t.Helper()
	files, err := db.Files()
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	return paths
}

// reopen closes db, so its queued records are written, and opens the library again.
func reopen(t *testing.T, db *DB, dir string) *DB {
	t.Helper()
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestSync_OnlyHashesNewOrChangedFiles(t *testing.T) {
	lib := t.TempDir()
	writeFiles(t, lib, map[string]string{"2023/a.jpg": "a", "2023/b.jpg": "b", "2023/c.jpg": "c", "2023/d.jpg": "d"})
	db, _ := Open(lib)

	if res := syncLib(t, lib, db, SyncOptions{}); res.Hashed != 4 || res.Unchanged != 0 {
		t.Fatalf("first sync: %+v, want 4 hashed", res)
	}
	db = reopen(t, db, lib)
	if res := syncLib(t, lib, db, SyncOptions{}); res.Hashed != 0 || res.Unchanged != 4 {
		t.Fatalf("second sync: %+v, want 4 unchanged", res)
	}

	writeFiles(t, lib, map[string]string{"2023/a.jpg": "a, edited", "2023/new.jpg": "new"})
	later := time.Now().Add(time.Hour)
	os.Chtimes(filepath.Join(lib, "2023", "b.jpg"), later, later) // same size, new mtime
	os.Remove(filepath.Join(lib, "2023", "c.jpg"))
	db = reopen(t, db, lib)

	res := syncLib(t, lib, db, SyncOptions{})
	if res.Hashed != 3 || res.Unchanged != 1 {
		t.Errorf("after changes: %+v, want 3 hashed (a, b, new) and 1 unchanged (d)", res)
	}
	db = reopen(t, db, lib)
	want := []string{"2023/a.jpg", "2023/b.jpg", "2023/d.jpg", "2023/new.jpg"}
	if got := storedPaths(t, db); !equalStrings(got, want) {
		t.Errorf("stored paths = %v, want %v", got, want)
	}
	// The removed file's content is still remembered, so it isn't imported again.
	if claimed, _ := db.Claim(hashOf("c")); claimed {
		t.Error("hash of a removed file was forgotten without -prune")
	}

	if res := syncLib(t, lib, db, SyncOptions{Rehash: true}); res.Hashed != 4 || res.Unchanged != 0 {
		t.Errorf("rehash: %+v, want all 4 hashed", res)
	}
}

func TestSync_IncrementalPrune(t *testing.T) {
	lib := t.TempDir()
	writeFiles(t, lib, map[string]string{"keep.jpg": "keep", "gone.jpg": "gone"})
	db, _ := Open(lib)
	syncLib(t, lib, db, SyncOptions{})
	db = reopen(t, db, lib)

	os.Remove(filepath.Join(lib, "gone.jpg"))
	res := syncLib(t, lib, db, SyncOptions{Prune: true})
	if res.Unchanged != 1 || len(res.Pruned) != 1 || res.Pruned[0] != "gone.jpg" {
		t.Errorf("prune: %+v, want keep.jpg unchanged and gone.jpg pruned", res)
	}
	if claimed, _ := db.Claim(hashOf("keep")); claimed {
		t.Error("prune forgot the hash of an unchanged file")
	}
}

func TestDB_MovePath(t *testing.T) {
	lib := t.TempDir()
	db, _ := Open(lib)
	for _, p := range []string{"2023-05-14/a.jpg", "2023-05-14/sub/b.jpg", "2023-05-14x/c.jpg", "loose.jpg"} {
		db.RecordFile(hashOf(p), FileState{Path: p, Size: 1, ModTime: 1})
	}
	db = reopen(t, db, lib)

	if err := db.MovePath(filepath.FromSlash("2023-05-14"), filepath.FromSlash("2023/2023-05-14")); err != nil {
		t.Fatal(err)
	}
	if err := db.MovePath("loose.jpg", "misc/loose.jpg"); err != nil {
		t.Fatal(err)
	}
	want := []string{"2023-05-14x/c.jpg", "2023/2023-05-14/a.jpg", "2023/2023-05-14/sub/b.jpg", "misc/loose.jpg"}
	if got := storedPaths(t, db); !equalStrings(got, want) {
		t.Errorf("paths = %v, want %v", got, want)
	}
}

// A read-only database from a version without the library_files table has no files,
// so a dry-run sync hashes everything instead of failing.
func TestDB_FilesOnOldReadOnlyDatabase(t *testing.T) {
	lib := t.TempDir()
	raw, err := sql.Open("sqlite", filepath.Join(lib, DBFileName))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`CREATE TABLE processed_hashes (hash TEXT PRIMARY KEY, filename TEXT, processed_at DATETIME)`); err != nil {
		t.Fatal(err)
	}
	raw.Close()

	db, err := OpenReadOnly(lib)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	files, err := db.Files()
	if err != nil || len(files) != 0 {
		t.Errorf("Files() = %v, %v; want empty", files, err)
	}
	writeFiles(t, lib, map[string]string{"a.jpg": "a"})
	if res := syncLib(t, lib, db, SyncOptions{}); res.Hashed != 1 {
		t.Errorf("dry-run sync: %+v, want 1 hashed", res)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
