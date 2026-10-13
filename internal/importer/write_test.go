package importer

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestWriteFile_PicksFreeNames(t *testing.T) {
	dir, tmp := t.TempDir(), t.TempDir()
	for i, want := range []string{"test.jpg", "test_1.jpg", "test_2.jpg"} {
		got, err := writeFile(context.Background(), newMemFile("x.jpg", string(rune('a'+i))), filepath.Join(dir, "test.jpg"), tmp)
		if err != nil {
			t.Fatal(err)
		}
		if got != filepath.Join(dir, want) {
			t.Errorf("writeFile = %q, want %q", got, want)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "test.jpg")); string(b) != "a" {
		t.Errorf("first file overwritten: %q", b)
	}
	if entries, _ := os.ReadDir(tmp); len(entries) != 0 {
		t.Errorf("left %d temp files", len(entries))
	}
}

func TestWriteFile_FailureLeavesNothingBehind(t *testing.T) {
	dir := t.TempDir()
	if _, err := writeFile(context.Background(), failingFile{"a.jpg"}, filepath.Join(dir, "a.jpg"), dir); err == nil {
		t.Fatal("expected an error")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := writeFile(ctx, newMemFile("b.jpg", "data"), filepath.Join(dir, "b.jpg"), dir); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("left behind %d entries", len(entries))
	}
}
