package importer

import (
	"path/filepath"
	"strings"
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

	// Album, if set, files every media file under AlbumsDir/<Album>, whatever its date
	// or Takeout album. It must be a single folder name.
	Album string

	// Event, if set, is appended to the name of every dated folder: 2023/2023-05-14
	// becomes 2023/2023-05-14 <Event>. It must be usable in a folder name.
	Event string
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
// at t (as found by src). prec says how much of t is known; see [Layout.dateDir].
//
// The layout's own folder names are trusted (the config package validates them).
// Album names come from the input, so one that would escape the library (possible
// only with hostile input) falls back to the date folder.
func (l Layout) Dir(path string, t time.Time, prec Precision, src DateSource) string {
	if l.Album != "" {
		return filepath.Join(filepath.FromSlash(l.AlbumsDir), l.Album)
	}
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
	return filepath.FromSlash(l.dateDir(t, prec))
}

// dateDir names the dated folder for t, slash-separated, with Event appended.
//
// For a partial date (only a year, or a year and month), only the leading components
// of DateFormat that need nothing finer are used, followed by the date itself at its
// precision. With the default "2006/2006-01-02", 2019-07 gives 2019/2019-07 and 2019
// gives just 2019 (or 2019/2019 <Event> with an event, so the event keeps its own
// folder inside the year).
func (l Layout) dateDir(t time.Time, prec Precision) string {
	parts := strings.Split(l.DateFormat, "/")
	var kept []string
	for _, p := range parts {
		if !coveredBy(p, t, prec) {
			break
		}
		kept = append(kept, t.Format(p))
	}
	if len(kept) == len(parts) {
		if l.Event != "" {
			kept[len(kept)-1] += " " + l.Event
		}
		return strings.Join(kept, "/")
	}
	label := GivenDate{Time: t, Precision: prec}.String()
	if l.Event != "" {
		label += " " + l.Event
	} else if len(kept) > 0 && kept[len(kept)-1] == label {
		return strings.Join(kept, "/")
	}
	return strings.Join(append(kept, label), "/")
}

// coveredBy reports whether the time layout component p can be filled in from a date
// known to precision prec. It is, if formatting it gives the same result at both ends
// of the period the date covers (so it uses no finer field: day, weekday, hour...).
func coveredBy(p string, t time.Time, prec Precision) bool {
	var first, last time.Time
	switch prec {
	case YearPrecision:
		first = time.Date(t.Year(), 1, 1, 0, 0, 0, 0, time.UTC)
		last = time.Date(t.Year(), 12, 28, 15, 59, 59, 0, time.UTC)
	case MonthPrecision:
		first = time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
		last = time.Date(t.Year(), t.Month(), 28, 15, 59, 59, 0, time.UTC)
	default:
		return true
	}
	return first.Format(p) == last.Format(p)
}
