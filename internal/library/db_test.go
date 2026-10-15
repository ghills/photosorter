package library

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func openDB(t *testing.T, dir string) *DB {
	t.Helper()
	db, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// has reports whether hash is recorded, in memory or on disk.
func has(db *DB, hash string) (bool, error) {
	if v, ok := db.known.Load(hash); ok && v == true {
		return true, nil
	}
	return db.inSQL(hash)
}

func count(t *testing.T, db *DB) int {
	t.Helper()
	n, err := db.Count()
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestOpen_CreatesDatabase(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "new-library")
	db := openDB(t, dir)
	if n := count(t, db); n != 0 {
		t.Errorf("Count = %d, want 0", n)
	}
	if _, err := os.Stat(filepath.Join(dir, DBFileName)); err != nil {
		t.Errorf("database file not created: %v", err)
	}
}

func TestDB_HasAndRecord(t *testing.T) {
	db := openDB(t, t.TempDir())
	const hash = "e3b0c44298fc1c14"

	if ok, err := has(db, hash); err != nil || ok {
		t.Fatalf("Has before Record = %v, %v; want false", ok, err)
	}
	if err := db.Record(hash, "a.jpg"); err != nil {
		t.Fatal(err)
	}
	if ok, err := has(db, hash); err != nil || !ok {
		t.Errorf("Has after Record = %v, %v; want true", ok, err)
	}
	// Recording the same hash again is harmless (INSERT OR IGNORE).
	if err := db.Record(hash, "b.jpg"); err != nil {
		t.Errorf("duplicate Record: %v", err)
	}
}

func TestDB_ClaimIsExclusive(t *testing.T) {
	db := openDB(t, t.TempDir())

	var winners atomic.Int32
	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if ok, err := db.Claim("same"); err == nil && ok {
				winners.Add(1)
				db.Keep("same") // let the others stop waiting
			}
		}()
	}
	wg.Wait()
	if winners.Load() != 1 {
		t.Fatalf("%d goroutines claimed the same hash, want 1", winners.Load())
	}
}

func TestDB_ClaimWaitsForReleasedClaim(t *testing.T) {
	db := openDB(t, t.TempDir())
	if ok, _ := db.Claim("same"); !ok {
		t.Fatal("first claim failed")
	}
	got := make(chan bool)
	go func() {
		ok, _ := db.Claim("same")
		got <- ok
	}()
	select {
	case <-got:
		t.Fatal("second Claim returned while the first was in flight")
	case <-time.After(50 * time.Millisecond):
	}
	// The first importer failed: the waiting one must get to import the content.
	db.Release("same")
	if ok := <-got; !ok {
		t.Error("Claim after Release = false, want true")
	}
}

func TestOpenReadOnly_WritesNothing(t *testing.T) {
	dir := t.TempDir()
	db := openDB(t, dir)
	db.Record("persisted", "a.jpg")
	db.Close()

	ro, err := OpenReadOnly(dir)
	if err != nil {
		t.Fatal(err)
	}
	if ok, _ := ro.Claim("persisted"); ok {
		t.Error("read-only DB does not see persisted hash")
	}
	ro.Record("new", "b.jpg")
	if ok, _ := ro.Claim("new"); ok {
		t.Error("read-only DB forgot an in-memory record")
	}
	ro.SetLibraryFiles(5)
	if err := ro.Close(); err != nil {
		t.Fatal(err)
	}
	if n := count(t, openDB(t, dir)); n != 1 {
		t.Errorf("read-only DB wrote records: count = %d, want 1", n)
	}

	missing := filepath.Join(t.TempDir(), "missing")
	ro, err = OpenReadOnly(missing)
	if err != nil {
		t.Fatal(err)
	}
	ro.Record("x", "x.jpg")
	if n := count(t, ro); n != 0 {
		t.Errorf("empty read-only DB count = %d", n)
	}
	ro.Close()
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Errorf("read-only open created the library folder: %v", err)
	}
}

func TestDB_LibraryFiles(t *testing.T) {
	dir := t.TempDir()
	db := openDB(t, dir)
	if _, ok, _ := db.LibraryFiles(); ok {
		t.Error("new DB should not know its file count")
	}
	if err := db.SetLibraryFiles(42); err != nil {
		t.Fatal(err)
	}
	if n, ok, err := db.LibraryFiles(); n != 42 || !ok || err != nil {
		t.Errorf("LibraryFiles = %d, %v, %v; want 42", n, ok, err)
	}
}

func TestDB_ClaimSeesPersistedHashes(t *testing.T) {
	dir := t.TempDir()
	db, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	db.Record("persisted", "a.jpg")
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	db2 := openDB(t, dir)
	if ok, err := db2.Claim("persisted"); err != nil || ok {
		t.Errorf("Claim of a stored hash = %v, %v; want false", ok, err)
	}
}

func TestDB_RecordAfterCloseReturnsError(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := db.Record("abc", "a.jpg"); err == nil {
		t.Error("expected error recording into a closed DB")
	}
	if err := db.Close(); err != nil {
		t.Errorf("second Close should be a no-op, got %v", err)
	}
}

func TestDB_CloseFlushesAndReportsWriteErrors(t *testing.T) {
	dir := t.TempDir()
	db, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	for i := range 1200 {
		if err := db.Record(fmt.Sprintf("hash%d", i), "f.jpg"); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	db2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if n := count(t, db2); n != 1200 {
		t.Errorf("persisted %d records, want 1200", n)
	}

	// Break the table so the background writer fails; the failure must surface.
	if _, err := db2.sql.Exec("DROP TABLE processed_hashes"); err != nil {
		t.Fatal(err)
	}
	db2.Record("x", "x.jpg")
	if err := db2.Close(); err == nil {
		t.Error("Close should report the failed background write")
	}
}

func TestDB_PragmasApplyToEveryConnectionAndOddPaths(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "my photos #1 100%?")
	db := openDB(t, dir)

	// Hold several connections at once so the pool opens more than one.
	var conns []*sql.Conn
	for i := range 3 {
		c, err := db.sql.Conn(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		conns = append(conns, c)
		var timeout int
		if err := c.QueryRowContext(context.Background(), "PRAGMA busy_timeout").Scan(&timeout); err != nil || timeout != 5000 {
			t.Errorf("conn %d busy_timeout = %d, %v; want 5000", i, timeout, err)
		}
		var syncMode int
		if err := c.QueryRowContext(context.Background(), "PRAGMA synchronous").Scan(&syncMode); err != nil || syncMode != 1 {
			t.Errorf("conn %d synchronous = %d, %v; want 1 (NORMAL)", i, syncMode, err)
		}
	}
	for _, c := range conns {
		c.Close()
	}
	if _, err := os.Stat(filepath.Join(dir, DBFileName)); err != nil {
		t.Errorf("db not created at expected path: %v", err)
	}
}

func TestDB_PruneReadOnlyWritesNothing(t *testing.T) {
	dir := t.TempDir()
	db := openDB(t, dir)
	db.Record("keep", "a.jpg")
	db.Record("stale", "b.jpg")
	db.Close()

	ro, err := OpenReadOnly(dir)
	if err != nil {
		t.Fatal(err)
	}
	names, err := ro.Prune(map[string]struct{}{"keep": {}})
	if err != nil || len(names) != 1 || names[0] != "b.jpg" {
		t.Fatalf("Prune = %v, %v; want [b.jpg]", names, err)
	}
	if ok, _ := ro.Claim("stale"); !ok {
		t.Error("read-only DB still treats pruned hash as present")
	}
	if ok, _ := ro.Claim("keep"); ok {
		t.Error("read-only DB forgot a kept hash")
	}
	ro.Close()
	if n := count(t, openDB(t, dir)); n != 2 {
		t.Errorf("read-only prune changed the database: count = %d, want 2", n)
	}
}
