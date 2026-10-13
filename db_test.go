package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestInitDB(t *testing.T) {
	tempDir := t.TempDir()

	db, count, err := InitDB(tempDir)
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer db.Close()

	if count != 0 {
		t.Errorf("Expected initial count of 0, got %d", count)
	}

	// Verify DB file was created
	dbPath := filepath.Join(tempDir, ".photosorter_cache.db")
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		t.Errorf("Database file was not created at %s", dbPath)
	}
}

func TestDB_ExistsAndInsert(t *testing.T) {
	tempDir := t.TempDir()
	db, _, err := InitDB(tempDir)
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer db.Close()

	hash := "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855" // dummy hash

	// Initial check, should not exist
	exists, err := db.Exists(hash)
	if err != nil {
		t.Fatalf("Exists check failed: %v", err)
	}
	if exists {
		t.Errorf("Expected hash to not exist")
	}

	// Insert the hash
	err = db.Insert(hash, "dummy_file.jpg")
	if err != nil {
		t.Fatalf("Insert failed: %v", err)
	}

	// Second check, should exist (served from the in-memory cache immediately)
	exists, err = db.Exists(hash)
	if err != nil {
		t.Fatalf("Exists check failed: %v", err)
	}

	if !exists {
		t.Errorf("Expected hash to exist after insert")
	}

	// Insert duplicate should not error (because of INSERT OR IGNORE)
	err = db.Insert(hash, "another_dummy.jpg")
	if err != nil {
		t.Fatalf("Insert of duplicate failed: %v", err)
	}
}

func TestDB_InsertAfterCloseReturnsError(t *testing.T) {
	db, _, err := InitDB(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}
	if err := db.Insert("abc", "a.jpg"); err == nil {
		t.Error("expected error inserting into a closed DB (previously a panic)")
	}
	if err := db.Close(); err != nil {
		t.Errorf("second Close should be a no-op, got %v", err)
	}
}

func TestDB_CloseFlushesAndReportsWriteErrors(t *testing.T) {
	dir := t.TempDir()
	db, _, err := InitDB(dir)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 1200; i++ {
		if err := db.Insert(fmt.Sprintf("hash%d", i), "f.jpg"); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}
	db2, count, err := InitDB(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()
	if count != 1200 {
		t.Errorf("persisted %d records, want 1200", count)
	}

	// Break the table so the background writer fails; the failure must surface.
	if _, err := db2.db.Exec("DROP TABLE processed_hashes"); err != nil {
		t.Fatal(err)
	}
	db2.Insert("x", "x.jpg")
	if err := db2.Close(); err == nil {
		t.Error("Close should report the failed background write")
	}
}

func TestDB_PragmasApplyToEveryConnectionAndOddPaths(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "my photos #1 100%?")
	db, _, err := InitDB(dir)
	if err != nil {
		t.Fatalf("InitDB with unusual path failed: %v", err)
	}
	defer db.Close()

	// Hold several connections at once so the pool opens more than one.
	var conns []*sql.Conn
	for i := 0; i < 3; i++ {
		c, err := db.db.Conn(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		conns = append(conns, c)
		var timeout int
		if err := c.QueryRowContext(context.Background(), "PRAGMA busy_timeout").Scan(&timeout); err != nil || timeout != 5000 {
			t.Errorf("conn %d busy_timeout = %d, %v; want 5000", i, timeout, err)
		}
		var sync int
		if err := c.QueryRowContext(context.Background(), "PRAGMA synchronous").Scan(&sync); err != nil || sync != 1 {
			t.Errorf("conn %d synchronous = %d, %v; want 1 (NORMAL)", i, sync, err)
		}
	}
	for _, c := range conns {
		c.Close()
	}
	if _, err := os.Stat(filepath.Join(dir, ".photosorter_cache.db")); err != nil {
		t.Errorf("db not created at expected path: %v", err)
	}
}
