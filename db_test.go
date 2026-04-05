package main

import (
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

	// Second check, should exist
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
