package media

import (
	"bytes"
	"encoding/binary"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"photosorter/internal/media/mediatest"
)

// box builds an MP4 box with a 32-bit size header.
func box(boxType string, body []byte) []byte {
	b := make([]byte, 8+len(body))
	binary.BigEndian.PutUint32(b[0:4], uint32(len(b)))
	copy(b[4:8], boxType)
	copy(b[8:], body)
	return b
}

// header builds a bare box header claiming the given size.
func header(size uint32, boxType string) []byte {
	b := make([]byte, 8)
	binary.BigEndian.PutUint32(b[0:4], size)
	copy(b[4:8], boxType)
	return b
}

// extHeader builds a box header that uses the 64-bit size form.
func extHeader(size uint64, boxType string) []byte {
	e := make([]byte, 8)
	binary.BigEndian.PutUint64(e, size)
	return append(header(1, boxType), e...)
}

// mvhdV0 returns a version-0 mvhd body with the given timescale and duration.
func mvhdV0(timeScale, duration uint32) []byte {
	b := make([]byte, 20)
	binary.BigEndian.PutUint32(b[12:16], timeScale)
	binary.BigEndian.PutUint32(b[16:20], duration)
	return b
}

// mvhdV1 returns a version-1 mvhd body with the given timescale and duration.
func mvhdV1(timeScale uint32, duration uint64) []byte {
	b := make([]byte, 32)
	b[0] = 1
	binary.BigEndian.PutUint32(b[20:24], timeScale)
	binary.BigEndian.PutUint64(b[24:32], duration)
	return b
}

func TestMP4Duration(t *testing.T) {
	ftyp := box("ftyp", []byte("isom\x00\x00\x02\x00"))
	v1 := mvhdV1(1000, 5500)

	tests := []struct {
		name    string
		content []byte
		want    time.Duration
		wantErr bool
	}{
		{"v0 mvhd after ftyp", append(ftyp, box("moov", box("mvhd", mvhdV0(1000, 2500)))...), 2500 * time.Millisecond, false},
		{"v1 mvhd", box("moov", box("mvhd", v1)), 5500 * time.Millisecond, false},
		{"v1 mvhd too short for duration", box("moov", box("mvhd", v1[:28])), 0, true},
		{"v1 implausible duration", box("moov", box("mvhd", mvhdV1(1, 1<<62))), 0, true},
		{"empty mvhd body", box("moov", box("mvhd", nil)), 0, true},
		{"zero timescale", box("moov", box("mvhd", mvhdV0(0, 10))), 0, true},
		{"box size smaller than header", append(header(4, "free"), make([]byte, 16)...), 0, true},
		{"extended size smaller than header", extHeader(3, "free"), 0, true},
		{"extended size has top bit set", extHeader(1<<63, "free"), 0, true},
		{"mvhd claims huge size", append(header(0xFFFFFFF0, "mvhd"), make([]byte, 40)...), 0, true},
		{"skip past end of data", append(header(0xFFFFFFF0, "free"), make([]byte, 16)...), 0, true},
		{"unbounded non-moov box", header(0, "mdat"), 0, true},
		{"unbounded moov box", append(header(0, "moov"), box("mvhd", v1)...), 5500 * time.Millisecond, false},
		{"truncated", box("moov", nil)[:6], 0, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Wrap so the reader is not an io.Seeker and the read-and-discard path is used.
			got, err := MP4Duration(struct{ io.Reader }{bytes.NewReader(tc.content)})
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if got != tc.want {
				t.Errorf("duration = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestMP4Duration_SeekableSkip(t *testing.T) {
	content := append(box("mdat", make([]byte, 4096)), box("moov", box("mvhd", mvhdV0(1000, 2000)))...)
	path := filepath.Join(t.TempDir(), "v.mp4")
	if err := os.WriteFile(path, content, 0644); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	got, err := MP4Duration(f)
	f.Close()
	if err != nil || got != 2*time.Second {
		t.Errorf("got %v, %v; want 2s", got, err)
	}

	// An mdat claiming to extend past EOF on a real file must error, not hang or succeed.
	if err := os.WriteFile(path, append(header(0xFFFFFFF0, "mdat"), make([]byte, 16)...), 0644); err != nil {
		t.Fatal(err)
	}
	f, err = os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := MP4Duration(f); err == nil {
		t.Error("expected error for box extending past EOF")
	}
}

func TestMovieHeader_Created(t *testing.T) {
	recorded := time.Date(2023, 5, 14, 19, 0, 0, 0, time.UTC)
	v1 := mvhdV1(1000, 5500)
	binary.BigEndian.PutUint64(v1[4:12], uint64(recorded.Unix()-mp4Epoch))

	tests := []struct {
		name    string
		content []byte
		want    time.Time // zero for none
	}{
		{"v0", mediatest.MP4(recorded, 2*time.Second, 64), recorded},
		{"v1", box("moov", box("mvhd", v1)), recorded},
		{"no creation time", mediatest.MP4(time.Time{}, 2*time.Second, 0), time.Time{}},
		{"Unix seconds written as MP4 seconds", mediatest.MP4(time.Unix(recorded.Unix()+mp4Epoch, 0), time.Second, 0), time.Time{}},
		// A zero timescale makes the duration unreadable, but the creation time is still good.
		{"zero timescale", box("moov", box("mvhd", func() []byte {
			b := mvhdV0(0, 10)
			binary.BigEndian.PutUint32(b[4:8], uint32(recorded.Unix()-mp4Epoch))
			return b
		}())), recorded},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h, err := ReadMovieHeader(bytes.NewReader(tc.content))
			if err != nil {
				t.Fatal(err)
			}
			got, ok := h.Created()
			if ok != !tc.want.IsZero() || !got.Equal(tc.want) {
				t.Errorf("Created() = %v, %v; want %v", got, ok, tc.want)
			}
		})
	}

	far := mvhdV1(1000, 1)
	binary.BigEndian.PutUint64(far[4:12], 1<<63)
	if h, err := ReadMovieHeader(bytes.NewReader(box("moov", box("mvhd", far)))); err != nil {
		t.Fatal(err)
	} else if got, ok := h.Created(); ok {
		t.Errorf("implausible creation time accepted: %v", got)
	}
}
