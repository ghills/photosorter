package main

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"
)

func TestGetUniqueDestPath(t *testing.T) {
	tempDir := t.TempDir()

	basePath := filepath.Join(tempDir, "test.jpg")

	// Should return the base path since it doesn't exist yet
	got := getUniqueDestPath(basePath)
	if got != basePath {
		t.Errorf("getUniqueDestPath() = %q, want %q", got, basePath)
	}

	// Create the file to trigger a collision
	os.WriteFile(basePath, []byte("dummy"), 0644)

	// Now it should return test_1.jpg
	want1 := filepath.Join(tempDir, "test_1.jpg")
	got1 := getUniqueDestPath(basePath)
	if got1 != want1 {
		t.Errorf("getUniqueDestPath() = %q, want %q", got1, want1)
	}

	// Create test_1.jpg to trigger a second collision
	os.WriteFile(want1, []byte("dummy"), 0644)

	// Now it should return test_2.jpg
	want2 := filepath.Join(tempDir, "test_2.jpg")
	got2 := getUniqueDestPath(basePath)
	if got2 != want2 {
		t.Errorf("getUniqueDestPath() = %q, want %q", got2, want2)
	}
}

func TestProcessor_ProcessDuplicateAndNew(t *testing.T) {
	tempDir := t.TempDir()

	// 1. Initialize DB inside tempDir
	dbDir := filepath.Join(tempDir, "db")
	db, _, err := InitDB(dbDir)
	if err != nil {
		t.Fatalf("InitDB error: %v", err)
	}
	defer db.Close()

	// 2. Create a test zip file
	zipPath := filepath.Join(tempDir, "test.zip")
	zipFile, err := os.Create(zipPath)
	if err != nil {
		t.Fatalf("Error creating zip file: %v", err)
	}

	zw := zip.NewWriter(zipFile)

	// File 1: IMG_20230101.jpg
	f1, err := zw.Create("IMG_20230101.jpg")
	if err != nil {
		t.Fatalf("Error creating zip entry: %v", err)
	}
	f1.Write([]byte("image content A"))

	// File 2: duplicate content of file 1 but different filename/date
	f2, err := zw.Create("IMG_20230102.jpg")
	if err != nil {
		t.Fatalf("Error creating zip entry: %v", err)
	}
	f2.Write([]byte("image content A"))

	zw.Close()
	zipFile.Close()

	// 3. Run processor
	outDir := filepath.Join(tempDir, "out")
	src, err := NewZipSource(zipPath)
	if err != nil {
		t.Fatalf("Error creating ZipSource: %v", err)
	}
	processor := NewProcessor([]InputSource{src}, outDir, db, 1, false, nil)
	err = processor.Run()
	if err != nil {
		t.Fatalf("Processor Run error: %v", err)
	}

	// 4. Verify results
	// First file should be placed in 2023-01-01 folder (Fallback logic parses IMG_20230101)
	expectedDest := filepath.Join(outDir, "2023-01-01", "IMG_20230101.jpg")
	if _, err := os.Stat(expectedDest); os.IsNotExist(err) {
		t.Errorf("Expected output file not found: %s", expectedDest)
	}

	// Second file should be skipped because it is an identical duplicate (same bytes -> same hash)
	// It parses to 2023-01-02, but should NOT exist!
	expectedDuplicateDest := filepath.Join(outDir, "2023-01-02", "IMG_20230102.jpg")
	if _, err := os.Stat(expectedDuplicateDest); !os.IsNotExist(err) {
		t.Errorf("Duplicate file was erroneously extracted: %s", expectedDuplicateDest)
	}
}
