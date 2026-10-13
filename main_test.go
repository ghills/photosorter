package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildSources(t *testing.T) {
	dir := t.TempDir()
	writeZip(t, filepath.Join(dir, "takeout-001.zip"), map[string]string{"a.jpg": "a"})
	writeZip(t, filepath.Join(dir, "takeout-002.zip"), map[string]string{"b.jpg": "b"})
	writeZip(t, filepath.Join(dir, "odd[1].zip"), map[string]string{"c.jpg": "c"})
	media := filepath.Join(dir, "media")
	os.MkdirAll(media, 0755)
	out := filepath.Join(dir, "out")

	closeAll := func(srcs []InputSource) {
		for _, s := range srcs {
			s.Close()
		}
	}

	t.Run("glob expands to every match", func(t *testing.T) {
		srcs, err := buildSources("", filepath.Join(dir, "takeout-*.zip"), out)
		if err != nil || len(srcs) != 2 {
			t.Fatalf("got %d sources, %v; want 2", len(srcs), err)
		}
		closeAll(srcs)
	})

	t.Run("existing path with glob characters is taken literally", func(t *testing.T) {
		srcs, err := buildSources("", filepath.Join(dir, "odd[1].zip"), out)
		if err != nil || len(srcs) != 1 {
			t.Fatalf("got %d sources, %v; want 1", len(srcs), err)
		}
		closeAll(srcs)
	})

	t.Run("bracket pattern is expanded when no such literal file exists", func(t *testing.T) {
		srcs, err := buildSources("", filepath.Join(dir, "takeout-00[12].zip"), out)
		if err != nil || len(srcs) != 2 {
			t.Fatalf("got %d sources, %v; want 2", len(srcs), err)
		}
		closeAll(srcs)
	})

	t.Run("unmatched pattern", func(t *testing.T) {
		if _, err := buildSources("", filepath.Join(dir, "nothing-*.zip"), out); err == nil || !strings.Contains(err.Error(), "No zip files matched") {
			t.Errorf("err = %v", err)
		}
	})

	t.Run("missing zip", func(t *testing.T) {
		if _, err := buildSources("", filepath.Join(dir, "missing.zip"), out); err == nil {
			t.Error("expected error")
		}
	})

	t.Run("folder and zip together", func(t *testing.T) {
		srcs, err := buildSources(media, filepath.Join(dir, "takeout-001.zip"), out)
		if err != nil || len(srcs) != 2 {
			t.Fatalf("got %d sources, %v; want 2", len(srcs), err)
		}
		closeAll(srcs)
	})

	t.Run("missing input folder", func(t *testing.T) {
		if _, err := buildSources(filepath.Join(dir, "nope"), "", out); err == nil {
			t.Error("expected error")
		}
	})

	t.Run("overlapping input and output", func(t *testing.T) {
		if _, err := buildSources(media, "", filepath.Join(media, "lib")); err == nil || !strings.Contains(err.Error(), "must not contain") {
			t.Errorf("err = %v", err)
		}
	})

	t.Run("a failure leaves no sources open", func(t *testing.T) {
		srcs, err := buildSources(media, filepath.Join(dir, "missing.zip"), out)
		if err == nil || srcs != nil {
			t.Errorf("got %v, %v; want nil sources and an error", srcs, err)
		}
	})
}

func TestOpenLogger(t *testing.T) {
	out := t.TempDir()
	logger, closeLog := openLogger(out)
	logger.Printf("hello")
	closeLog()
	logs, _ := filepath.Glob(filepath.Join(out, "photosorter_*.log"))
	if len(logs) != 1 {
		t.Fatalf("found %d log files, want 1", len(logs))
	}
	if data, _ := os.ReadFile(logs[0]); !strings.Contains(string(data), "hello") {
		t.Errorf("log content = %q", data)
	}

	// An unwritable location degrades to a discard logger instead of failing.
	logger, closeLog = openLogger(filepath.Join(out, "does", "not", "exist"))
	defer closeLog()
	logger.Printf("must not panic")
}
