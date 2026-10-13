package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
)

// countingMedia records how many times its content is opened and lets tests misreport its size.
type countingMedia struct {
	name    string
	content []byte
	size    int64 // reported by Size(); may deliberately disagree with len(content)
	opens   atomic.Int32
}

func (c *countingMedia) Name() string { return c.name }
func (c *countingMedia) Size() int64  { return c.size }
func (c *countingMedia) Open() (io.ReadCloser, error) {
	c.opens.Add(1)
	return io.NopCloser(bytes.NewReader(c.content)), nil
}
func (c *countingMedia) OpenSidecar() (io.ReadCloser, bool) { return nil, false }

func newTestProcessor(t *testing.T, limit int64) (*Processor, string) {
	t.Helper()
	out := t.TempDir()
	db, _, err := InitDB(out)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	p := NewProcessor(nil, out, db, 1, 0, false, nil)
	p.BufferLimit = limit
	return p, out
}

func TestBuffering_ReadsSmallFileOnce(t *testing.T) {
	p, out := newTestProcessor(t, 1<<20)
	m := &countingMedia{name: "IMG_20230101.jpg", content: []byte("small photo"), size: 11}

	if _, err := p.processFile(context.Background(), m); err != nil {
		t.Fatal(err)
	}
	if got := m.opens.Load(); got != 1 {
		t.Errorf("source opened %d times, want 1 (hash, EXIF and copy should share one read)", got)
	}
	data, err := os.ReadFile(filepath.Join(out, "2023-01-01", "IMG_20230101.jpg"))
	if err != nil || string(data) != "small photo" {
		t.Errorf("output = %q, %v", data, err)
	}
}

func TestBuffering_DisabledOrTooLargeStreams(t *testing.T) {
	for name, tc := range map[string]struct {
		limit int64
		size  int64
	}{
		"disabled":  {0, 11},
		"too large": {5, 11},
	} {
		t.Run(name, func(t *testing.T) {
			p, out := newTestProcessor(t, tc.limit)
			m := &countingMedia{name: "IMG_20230101.jpg", content: []byte("small photo"), size: tc.size}
			if _, err := p.processFile(context.Background(), m); err != nil {
				t.Fatal(err)
			}
			if got := m.opens.Load(); got < 3 {
				t.Errorf("source opened %d times, want at least 3 when streaming", got)
			}
			if data, _ := os.ReadFile(filepath.Join(out, "2023-01-01", "IMG_20230101.jpg")); string(data) != "small photo" {
				t.Errorf("output = %q", data)
			}
		})
	}
}

// A zip header can understate a file's size. The content must still be imported intact,
// and without holding more than BufferLimit in memory.
func TestBuffering_WrongSizeFallsBackToStreaming(t *testing.T) {
	p, out := newTestProcessor(t, 16)
	content := bytes.Repeat([]byte("0123456789"), 100)
	m := &countingMedia{name: "IMG_20230101.jpg", content: content, size: 8}

	if _, err := p.processFile(context.Background(), m); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(out, "2023-01-01", "IMG_20230101.jpg"))
	if err != nil || !bytes.Equal(data, content) {
		t.Errorf("output differs from source (len %d vs %d), %v", len(data), len(content), err)
	}
}

// The duplicate check must not depend on which read path was taken.
func TestBuffering_HashMatchesStreaming(t *testing.T) {
	content := bytes.Repeat([]byte("duplicate me "), 1000)
	buffered, _ := newTestProcessor(t, 1<<20)
	h1, _, release, err := buffered.hashAndBuffer(context.Background(), &countingMedia{name: "a.jpg", content: content, size: int64(len(content))})
	if err != nil {
		t.Fatal(err)
	}
	release()
	h2, err := streamHash(context.Background(), &countingMedia{name: "a.jpg", content: content})
	if err != nil {
		t.Fatal(err)
	}
	if h1 != h2 {
		t.Errorf("buffered hash %s != streamed hash %s", h1, h2)
	}
}

func TestBuffering_StopsOnCancel(t *testing.T) {
	p, _ := newTestProcessor(t, 1<<20)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	m := &countingMedia{name: "IMG_20230101.jpg", content: []byte("x"), size: 1}
	if _, err := p.processFile(ctx, m); err == nil {
		t.Error("expected an error for a cancelled context")
	}
}
