package importer

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"photosorter/internal/library"
	"photosorter/internal/media"
	"photosorter/internal/source"
	"photosorter/internal/takeout"
)

// memFile is an in-memory source.File. It counts opens and can misreport its size.
type memFile struct {
	name    string
	content []byte
	size    int64  // reported by Size(); may deliberately disagree with len(content)
	sidecar string // sidecar JSON; "" for none
	opens   atomic.Int32
}

func (m *memFile) Name() string { return m.name }
func (m *memFile) Size() int64  { return m.size }
func (m *memFile) Open() (io.ReadCloser, error) {
	m.opens.Add(1)
	return io.NopCloser(bytes.NewReader(m.content)), nil
}
func (m *memFile) OpenSidecar() (io.ReadCloser, bool) {
	if m.sidecar == "" {
		return nil, false
	}
	return io.NopCloser(bytes.NewReader([]byte(m.sidecar))), true
}

func newMemFile(name, content string) *memFile {
	return &memFile{name: name, content: []byte(content), size: int64(len(content))}
}

// failingFile always fails to open.
type failingFile struct{ name string }

func (f failingFile) Name() string                       { return f.name }
func (f failingFile) Size() int64                        { return -1 }
func (f failingFile) Open() (io.ReadCloser, error)       { return nil, errors.New("boom") }
func (f failingFile) OpenSidecar() (io.ReadCloser, bool) { return nil, false }

// listSource is a source.Source over a fixed list of files.
type listSource struct {
	files    []source.File
	warnings []string
}

func (s listSource) Name() string         { return "test" }
func (s listSource) Files() []source.File { return s.files }
func (s listSource) Warnings() []string   { return s.warnings }
func (s listSource) Close() error         { return nil }

// sourceOf wraps files in a single source.
func sourceOf(files ...source.File) []source.Source {
	return []source.Source{listSource{files: files}}
}

// newTestImporter opens a library in a temp dir and returns an importer for it.
// opts.Library is filled in; other fields are used as given.
func newTestImporter(t *testing.T, opts Options) (*Importer, *library.DB) {
	t.Helper()
	if opts.Library == "" {
		opts.Library = t.TempDir()
	}
	db, err := library.Open(opts.Library)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if opts.Workers == 0 {
		opts.Workers = 1
	}
	return New(db, opts), db
}

func run(t *testing.T, im *Importer, sources []source.Source) Report {
	t.Helper()
	report, err := im.Run(context.Background(), sources)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return report
}

func writeZip(t *testing.T, path string, files map[string]string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for name, content := range files {
		w, _ := zw.Create(name)
		w.Write([]byte(content))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	f.Close()
}

func openZip(t *testing.T, path string) []source.Source {
	t.Helper()
	src, err := source.OpenZip(path, media.DefaultFilter(), takeout.NewSidecarIndex())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { src.Close() })
	return []source.Source{src}
}

// listDir returns the names in dir, ignoring a missing dir.
func listDir(t *testing.T, dir string) []string {
	t.Helper()
	entries, _ := os.ReadDir(dir)
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func readFile(t *testing.T, path ...string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(path...))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
