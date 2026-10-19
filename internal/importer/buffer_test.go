package importer

import (
	"bytes"
	"context"
	"testing"

	"photosorter/internal/library"
)

func TestBuffering_ReadsSmallFileOnce(t *testing.T) {
	im, _ := newTestImporter(t, Options{BufferLimit: 1 << 20})
	f := newMemFile("IMG_20230101.jpg", "small photo")

	run(t, im, sourceOf(f))
	if got := f.opens.Load(); got != 1 {
		t.Errorf("source opened %d times, want 1 (hash, EXIF and copy should share one read)", got)
	}
	if got := readFile(t, im.opts.Library, "2023/2023-01-01", "IMG_20230101.jpg"); got != "small photo" {
		t.Errorf("output = %q", got)
	}
}

func TestBuffering_DisabledOrTooLargeStreams(t *testing.T) {
	for name, limit := range map[string]int64{"disabled": 0, "too large": 5} {
		t.Run(name, func(t *testing.T) {
			im, _ := newTestImporter(t, Options{BufferLimit: limit})
			f := newMemFile("IMG_20230101.jpg", "small photo")
			run(t, im, sourceOf(f))
			if got := f.opens.Load(); got < 3 {
				t.Errorf("source opened %d times, want at least 3 when streaming", got)
			}
			if got := readFile(t, im.opts.Library, "2023/2023-01-01", "IMG_20230101.jpg"); got != "small photo" {
				t.Errorf("output = %q", got)
			}
		})
	}
}

// A zip header can understate a file's size. The content must still be imported intact,
// without holding more than BufferLimit in memory.
func TestBuffering_WrongSizeFallsBackToStreaming(t *testing.T) {
	im, _ := newTestImporter(t, Options{BufferLimit: 16})
	content := bytes.Repeat([]byte("0123456789"), 100)
	f := &memFile{name: "IMG_20230101.jpg", content: content, size: 8}

	run(t, im, sourceOf(f))
	if got := readFile(t, im.opts.Library, "2023/2023-01-01", "IMG_20230101.jpg"); got != string(content) {
		t.Errorf("output differs from source (len %d vs %d)", len(got), len(content))
	}
}

// The duplicate check must not depend on which read path was taken.
func TestBuffering_HashMatchesStreaming(t *testing.T) {
	content := bytes.Repeat([]byte("duplicate me "), 1000)
	im, _ := newTestImporter(t, Options{BufferLimit: 1 << 20})
	hash, _, release, err := im.hashAndBuffer(context.Background(), &memFile{name: "a.jpg", content: content, size: int64(len(content))})
	if err != nil {
		t.Fatal(err)
	}
	release()
	streamed, err := library.Hash(bytes.NewReader(content))
	if err != nil || hash != streamed {
		t.Errorf("buffered hash %s != streamed hash %s (%v)", hash, streamed, err)
	}
}

func TestBuffering_StopsOnCancel(t *testing.T) {
	im, _ := newTestImporter(t, Options{BufferLimit: 1 << 20})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := im.importFile(ctx, newMemFile("IMG_20230101.jpg", "x")); err == nil {
		t.Error("expected an error for a cancelled context")
	}
}
