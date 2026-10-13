package importer

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestReservePath(t *testing.T) {
	dir := t.TempDir()
	// Each call must reserve a distinct name, since the reservation itself creates the file.
	for _, want := range []string{"test.jpg", "test_1.jpg", "test_2.jpg"} {
		got, err := reservePath(filepath.Join(dir, "test.jpg"))
		if err != nil {
			t.Fatal(err)
		}
		if got != filepath.Join(dir, want) {
			t.Errorf("reservePath = %q, want %q", got, want)
		}
	}
}

func TestWriteFile_FailureLeavesNothingBehind(t *testing.T) {
	dir := t.TempDir()
	if _, err := writeFile(context.Background(), failingFile{"a.jpg"}, filepath.Join(dir, "a.jpg")); err == nil {
		t.Fatal("expected an error")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := writeFile(ctx, newMemFile("b.jpg", "data"), filepath.Join(dir, "b.jpg")); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("left behind %d entries", len(entries))
	}
}
