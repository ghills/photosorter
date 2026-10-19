package importer

import (
	"path/filepath"
	"time"

	"photosorter/internal/takeout"
)

// Layout decides where in the library each file goes.
type Layout struct {
	// DateFormat names dated folders, as a Go time layout. Slashes create nested
	// folders: "2006/2006-01-02" gives 2023/2023-05-14/, "2006/01" gives 2023/05/.
	DateFormat string

	// UnknownDir holds files whose date could not be determined.
	UnknownDir string

	// AlbumsDir is the parent of album folders when KeepAlbums is set.
	AlbumsDir string

	// KeepAlbums files media from Google Photos albums under AlbumsDir/<album name>
	// instead of by date.
	KeepAlbums bool
}

// DefaultLayout returns PhotoSorter's default layout: YYYY/YYYY-MM-DD/,
// Unknown_Date/ and Albums/<name>/.
func DefaultLayout() Layout {
	return Layout{
		DateFormat: "2006/2006-01-02",
		UnknownDir: "Unknown_Date",
		AlbumsDir:  "Albums",
		KeepAlbums: true,
	}
}

// Dir returns the folder, relative to the library root, for the file at path taken
// at t (as found by src).
//
// The layout's own folder names are trusted (the config package validates them).
// Album names come from the input, so one that would escape the library (possible
// only with hostile input) falls back to the date folder.
func (l Layout) Dir(path string, t time.Time, src DateSource) string {
	if l.KeepAlbums {
		if album := takeout.AlbumName(path); album != "" {
			if dir := filepath.Join(filepath.FromSlash(l.AlbumsDir), album); filepath.IsLocal(dir) {
				return dir
			}
		}
	}
	if src == DateUnknown {
		return filepath.FromSlash(l.UnknownDir)
	}
	return filepath.FromSlash(t.Format(l.DateFormat))
}
