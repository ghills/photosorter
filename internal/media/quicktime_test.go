package media

import (
	"bytes"
	"io"
	"testing"
	"time"

	"photosorter/internal/media/mediatest"
)

func TestMovieHeader_AppleCreationDate(t *testing.T) {
	utc := time.Date(2023, 5, 15, 6, 30, 0, 0, time.UTC) // header time: the 15th in UTC
	honolulu, _ := time.LoadLocation("Pacific/Honolulu")

	tests := []struct {
		name    string
		content []byte
		want    string // wall clock and offset, "" for no date
	}{
		{"local time wins over header", mediatest.AppleMOV(utc, "2023-05-14T23:30:00-0700"), "2023-05-14 23:30:00 -0700"},
		{"RFC 3339 offset", mediatest.AppleMOV(utc, "2023-05-14T23:30:00-07:00"), "2023-05-14 23:30:00 -0700"},
		{"fractional seconds", mediatest.AppleMOV(utc, "2023-05-14T23:30:00.123+0200"), "2023-05-14 23:30:00 +0200"},
		{"NUL padded", mediatest.AppleMOV(utc, "2023-05-14T23:30:00Z\x00"), "2023-05-14 23:30:00 +0000"},
		// Without a usable Apple date, the header's instant is read in loc.
		{"unparseable falls back to header", mediatest.AppleMOV(utc, "yesterday"), "2023-05-14 20:30:00 -1000"},
		{"implausible falls back to header", mediatest.AppleMOV(utc, "1904-01-01T00:00:00Z"), "2023-05-14 20:30:00 -1000"},
		{"Apple date alone", mediatest.AppleMOV(time.Time{}, "2020-02-29T08:00:00+0100"), "2020-02-29 08:00:00 +0100"},
		{"neither", mediatest.AppleMOV(time.Time{}, ""), ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Not seekable, as for a zip entry, so mdat and trak are read and discarded.
			h, err := ReadMovieHeader(struct{ io.Reader }{bytes.NewReader(tc.content)})
			if err != nil {
				t.Fatal(err)
			}
			got, ok := h.Created(honolulu)
			if ok != (tc.want != "") {
				t.Fatalf("Created() = %v, %v; want %q", got, ok, tc.want)
			}
			if ok && got.Format("2006-01-02 15:04:05 -0700") != tc.want {
				t.Errorf("Created() = %v, want %s", got, tc.want)
			}
			if d, err := h.Duration(); err != nil || d != 10*time.Second {
				t.Errorf("Duration() = %v, %v; want 10s", d, err)
			}
		})
	}
}

func TestReadMovieHeader_MetaPlacement(t *testing.T) {
	recorded := time.Date(2023, 5, 14, 12, 0, 0, 0, time.UTC)
	mp4 := mediatest.MP4(recorded, time.Second, 0)
	mvhd := mp4[bytes.Index(mp4, []byte("mvhd"))-4:]
	item := map[string]string{"com.apple.quicktime.creationdate": "2023-05-14T05:00:00-0700"}
	plain := mediatest.AppleMeta(item, false)
	iso := mediatest.AppleMeta(item, true)
	huge := mediatest.Box("meta", make([]byte, maxMetaSize+1))
	ext := append([]byte{0, 0, 0, 1}, []byte("trak\x00\x00\x00\x00\x00\x00\x00\x18")...)
	ext = append(ext, make([]byte, 8)...) // a trak with a 64-bit size header

	tests := []struct {
		name string
		moov []byte
		want string
	}{
		{"meta before mvhd", append(append([]byte{}, plain...), mvhd...), "05:00 -0700"},
		{"ISO full-box meta", append(append([]byte{}, mvhd...), iso...), "05:00 -0700"},
		{"meta too large is skipped", append(append([]byte{}, mvhd...), huge...), "12:00 +0000"},
		{"after a 64-bit trak", append(append(append([]byte{}, mvhd...), ext...), plain...), "05:00 -0700"},
		{"meta without the item", append(append([]byte{}, mvhd...), mediatest.AppleMeta(map[string]string{"com.apple.quicktime.make": "Apple"}, false)...), "12:00 +0000"},
		// A broken box after mvhd ends the walk but keeps the header.
		{"truncated after mvhd", append(append([]byte{}, mvhd...), 0, 0, 0, 0x40, 'm', 'e', 't', 'a'), "12:00 +0000"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h, err := ReadMovieHeader(bytes.NewReader(mediatest.Box("moov", tc.moov)))
			if err != nil {
				t.Fatal(err)
			}
			got, ok := h.Created(time.UTC)
			if !ok || got.Format("15:04 -0700") != tc.want {
				t.Errorf("Created() = %v, %v; want %s", got, ok, tc.want)
			}
		})
	}
}

func TestReadMovieHeader_NoMvhd(t *testing.T) {
	plain := mediatest.AppleMeta(map[string]string{"com.apple.quicktime.creationdate": "2023-05-14T05:00:00-0700"}, false)
	for name, content := range map[string][]byte{
		"empty moov":                        mediatest.Box("moov", nil),
		"meta only":                         mediatest.Box("moov", plain),
		"child overruns moov":               mediatest.Box("moov", []byte{0, 0, 0, 0x40, 't', 'r', 'a', 'k'}),
		"header overruns moov":              mediatest.Box("moov", []byte{0, 0, 0, 0x40}),
		"unbounded moov, meta only":         append([]byte{0, 0, 0, 0, 'm', 'o', 'o', 'v'}, plain...),
		"unbounded child in unbounded moov": append([]byte{0, 0, 0, 0, 'm', 'o', 'o', 'v'}, 0, 0, 0, 0, 't', 'r', 'a', 'k'),
	} {
		if _, err := ReadMovieHeader(bytes.NewReader(content)); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestKeyIndex(t *testing.T) {
	keys := func(sizes ...uint32) []byte {
		b := []byte{0, 0, 0, 0, 0, 0, 0, byte(len(sizes))}
		for _, s := range sizes {
			b = append(b, byte(s>>24), byte(s>>16), byte(s>>8), byte(s))
			b = append(b, "mdtaabcd"[:min(int(s), 8)]...)
		}
		return b
	}
	if got := keyIndex(keys(12), "abcd"); got != 1 {
		t.Errorf("keyIndex = %d, want 1", got)
	}
	for name, b := range map[string][]byte{
		"key longer than box": keys(1 << 30),
		"size below header":   keys(4),
		"count too high":      keys(8)[:12],
	} {
		if got := keyIndex(b, "abcd"); got != 0 {
			t.Errorf("%s: keyIndex = %d, want 0", name, got)
		}
	}
}
