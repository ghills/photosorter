package media

import (
	"bytes"
	"encoding/binary"
	"io"
	"testing"
	"time"

	"photosorter/internal/media/mediatest"
)

var taken = time.Date(2023, 5, 14, 12, 34, 56, 0, time.UTC)

func TestEXIFDate(t *testing.T) {
	tests := []struct {
		name     string
		content  []byte
		seekable bool // whether EXIFDate gets an io.Seeker
	}{
		{"jpeg", mediatest.JPEG(taken), false},
		{"tiff", mediatest.TIFF(taken), false},
		{"heic, Exif item in mdat", mediatest.HEIC(taken, mediatest.HEICOptions{}), false},
		{"heic, Exif item in idat", mediatest.HEIC(taken, mediatest.HEICOptions{InIdat: true}), false},
		{"heic, Exif item past the read limit", mediatest.HEIC(taken, mediatest.HEICOptions{Gap: exifReadLimit}), true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var r io.Reader = bytes.NewReader(tc.content)
			if !tc.seekable {
				r = struct{ io.Reader }{r}
			}
			got, ok := EXIFDate(r)
			if !ok {
				t.Fatal("no date found")
			}
			// The wall clock must come through as recorded, whatever the zone.
			if got.Format(time.DateTime) != taken.Format(time.DateTime) {
				t.Errorf("got %v, want wall clock %v", got, taken.Format(time.DateTime))
			}
			// The test files record no UTC offset.
			if EXIFHasZone(got) {
				t.Errorf("got zone %v, want none recorded", got.Location())
			}
		})
	}
}

func TestEXIFDate_HEICUnreadable(t *testing.T) {
	far := mediatest.HEIC(taken, mediatest.HEICOptions{Gap: exifReadLimit})
	whole := mediatest.HEIC(taken, mediatest.HEICOptions{})
	ftyp := mediatest.Box("ftyp", []byte("heic\x00\x00\x00\x00"))

	tests := []struct {
		name    string
		content []byte
	}{
		// Without a seek, an Exif item beyond the first exifReadLimit bytes is out of reach.
		{"item past read limit, not seekable", far},
		{"truncated meta", whole[:40]},
		{"truncated item", whole[:len(whole)-10]},
		{"no meta", append(ftyp, mediatest.Box("mdat", nil)...)},
		{"meta without Exif item", append(ftyp, mediatest.Box("meta", []byte{0, 0, 0, 0})...)},
		{"bare ftyp header", ftyp[:8]},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got, ok := EXIFDate(struct{ io.Reader }{bytes.NewReader(tc.content)}); ok {
				t.Errorf("got %v, want no date", got)
			}
		})
	}
}

func TestHEIFEXIF_BadTIFFOffset(t *testing.T) {
	b := mediatest.HEIC(taken, mediatest.HEICOptions{})
	// The item is the mdat body at the end of the file; its first 4 bytes are the offset.
	item := bytes.LastIndex(b, []byte("Exif\x00\x00")) - 4
	binary.BigEndian.PutUint32(b[item:], 1<<30)
	if _, err := heifEXIF(b, nil); err == nil {
		t.Error("expected an error for a TIFF header offset past the item")
	}
}

func TestParser(t *testing.T) {
	p := parser{b: []byte{0x01, 0x02, 0x03}}
	if v := p.uint(2); v != 0x0102 || p.err != nil {
		t.Fatalf("uint(2) = %#x, %v", v, p.err)
	}
	if v := p.uint(0); v != 0 || p.err != nil {
		t.Fatalf("uint(0) = %#x, %v", v, p.err)
	}
	if p.uint(2); p.err == nil {
		t.Fatal("reading past the end should fail")
	}
	if v := p.uint(1); v != 0 {
		t.Errorf("reads after a failure should return 0, got %#x", v)
	}
}
