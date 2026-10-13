package importer

import (
	"path/filepath"
	"testing"
	"time"
)

func TestLayout_Dir(t *testing.T) {
	when := time.Date(2023, 5, 14, 22, 0, 0, 0, time.UTC)
	album := "Takeout/Google Photos/Hawaii/IMG_1.jpg"
	plain := "Takeout/Google Photos/Photos from 2023/IMG_1.jpg"

	nested := Layout{DateFormat: "2006/01", UnknownDir: "misc/undated", AlbumsDir: "Albums", KeepAlbums: true}
	noAlbums := DefaultLayout()
	noAlbums.KeepAlbums = false

	tests := []struct {
		name   string
		layout Layout
		path   string
		src    DateSource
		want   string
	}{
		{"default dated", DefaultLayout(), plain, DateFromEXIF, "2023-05-14"},
		{"default unknown", DefaultLayout(), plain, DateUnknown, "Unknown_Date"},
		{"default album", DefaultLayout(), album, DateFromEXIF, "Albums/Hawaii"},
		{"album wins over unknown date", DefaultLayout(), album, DateUnknown, "Albums/Hawaii"},
		{"nested date format", nested, plain, DateFromFilename, "2023/05"},
		{"nested unknown dir", nested, plain, DateUnknown, "misc/undated"},
		{"albums ignored", noAlbums, album, DateFromEXIF, "2023-05-14"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.layout.Dir(tc.path, when, tc.src); got != filepath.FromSlash(tc.want) {
				t.Errorf("Dir = %q, want %q", got, tc.want)
			}
		})
	}
}
