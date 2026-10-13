package main

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReserveDestPath(t *testing.T) {
	tempDir := t.TempDir()
	basePath := filepath.Join(tempDir, "test.jpg")

	// Each call must reserve a distinct name, since the reservation itself creates the file.
	for _, name := range []string{"test.jpg", "test_1.jpg", "test_2.jpg"} {
		got, err := reserveDestPath(basePath)
		if err != nil {
			t.Fatalf("reserveDestPath() error: %v", err)
		}
		if want := filepath.Join(tempDir, name); got != want {
			t.Errorf("reserveDestPath() = %q, want %q", got, want)
		}
	}
}

// Many workers importing different content under the same filename into the same
// folder must each keep their own file, and identical content must be imported once.
func TestProcessor_ConcurrentSameNameAndDuplicates(t *testing.T) {
	tempDir := t.TempDir()
	db, _, err := InitDB(filepath.Join(tempDir, "out"))
	if err != nil {
		t.Fatalf("InitDB error: %v", err)
	}
	defer db.Close()

	zipPath := filepath.Join(tempDir, "test.zip")
	zf, _ := os.Create(zipPath)
	zw := zip.NewWriter(zf)
	const unique = 40
	for i := 0; i < unique; i++ {
		// Distinct content, same base name, same date folder.
		w, _ := zw.Create(fmt.Sprintf("dir%d/IMG_20230101.jpg", i))
		fmt.Fprintf(w, "unique content %d", i)
	}
	for i := 0; i < unique; i++ {
		// Identical content under different names.
		w, _ := zw.Create(fmt.Sprintf("same/IMG_20230102_%d.jpg", i))
		w.Write([]byte("shared content"))
	}
	zw.Close()
	zf.Close()

	src, err := NewZipSource(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	outDir := filepath.Join(tempDir, "out")
	if err := NewProcessor([]InputSource{src}, outDir, db, 16, 0, false, nil).Run(context.Background()); err != nil {
		t.Fatal(err)
	}

	count := func(dir string) (n int) {
		entries, _ := os.ReadDir(filepath.Join(outDir, dir))
		for _, e := range entries {
			if !strings.HasSuffix(e.Name(), ".tmp") {
				n++
			}
		}
		return n
	}
	if got := count("2023-01-01"); got != unique {
		t.Errorf("2023-01-01 has %d files, want %d (same-name files overwritten)", got, unique)
	}
	if got := count("2023-01-02"); got != 1 {
		t.Errorf("2023-01-02 has %d files, want 1 (identical content imported more than once)", got)
	}
	// No leftover temp files.
	for _, dir := range []string{"2023-01-01", "2023-01-02"} {
		entries, _ := os.ReadDir(filepath.Join(outDir, dir))
		for _, e := range entries {
			if strings.HasSuffix(e.Name(), ".tmp") {
				t.Errorf("leftover temp file %s/%s", dir, e.Name())
			}
		}
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
	processor := NewProcessor([]InputSource{src}, outDir, db, 1, 2, false, nil)
	err = processor.Run(context.Background())
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

func TestProcessor_RunStopsOnCancelledContext(t *testing.T) {
	tempDir := t.TempDir()
	outDir := filepath.Join(tempDir, "out")
	db, _, err := InitDB(outDir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	zipPath := filepath.Join(tempDir, "test.zip")
	zf, _ := os.Create(zipPath)
	zw := zip.NewWriter(zf)
	for i := 0; i < 20; i++ {
		w, _ := zw.Create(fmt.Sprintf("IMG_20230101_%d.jpg", i))
		fmt.Fprintf(w, "content %d", i)
	}
	zw.Close()
	zf.Close()
	src, err := NewZipSource(zipPath)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err = NewProcessor([]InputSource{src}, outDir, db, 4, 0, false, nil).Run(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run error = %v, want context.Canceled", err)
	}
	if entries, _ := os.ReadDir(filepath.Join(outDir, "2023-01-01")); len(entries) != 0 {
		t.Errorf("%d files written after cancellation", len(entries))
	}
}

func TestProcessor_RunReportsDatabaseFailure(t *testing.T) {
	tempDir := t.TempDir()
	outDir := filepath.Join(tempDir, "out")
	db, _, err := InitDB(outDir)
	if err != nil {
		t.Fatal(err)
	}
	db.setErr(errors.New("disk full"))

	zipPath := filepath.Join(tempDir, "test.zip")
	zf, _ := os.Create(zipPath)
	zw := zip.NewWriter(zf)
	w, _ := zw.Create("IMG_20230101.jpg")
	w.Write([]byte("content"))
	zw.Close()
	zf.Close()
	src, _ := NewZipSource(zipPath)

	err = NewProcessor([]InputSource{src}, outDir, db, 1, 0, false, nil).Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Errorf("Run error = %v, want database failure", err)
	}
	if err := db.Close(); err == nil {
		t.Error("Close should return the recorded write error")
	}
}
