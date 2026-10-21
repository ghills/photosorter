package importer

import (
	"time"

	"photosorter/internal/media"
	"photosorter/internal/source"
	"photosorter/internal/takeout"
)

// DateSource records how a file's capture date was determined.
type DateSource string

// Date sources, in the order they are tried.
const (
	DateFromEXIF         DateSource = "exif"
	DateFromJSONTaken    DateSource = "json_taken"
	DateFromVideo        DateSource = "video"
	DateFromJSONCreation DateSource = "json_creation"
	DateFromFilename     DateSource = "filename"
	DateUnknown          DateSource = "unknown"

	// Dates given for a manual import: DateForced replaces every file's own date,
	// DateFallback is used only where captureDate finds none.
	DateForced   DateSource = "forced"
	DateFallback DateSource = "fallback"
)

// dateSources lists every DateSource in priority order, for reporting.
var dateSources = []DateSource{DateForced, DateFromEXIF, DateFromJSONTaken, DateFromVideo, DateFromJSONCreation, DateFromFilename, DateFallback, DateUnknown}

// readSidecar returns f's Takeout sidecar. A missing or broken one gives the zero
// Metadata, which has no dates or other values.
func readSidecar(f source.File) takeout.Metadata {
	var meta takeout.Metadata
	if rc, ok := f.OpenSidecar(); ok {
		meta, _ = takeout.ReadMetadata(rc) // a broken sidecar counts as none
		rc.Close()
	}
	return meta
}

// fromSidecar reports whether the date came from the Takeout sidecar.
func (d dated) fromSidecar() bool {
	return d.src == DateFromJSONTaken || d.src == DateFromJSONCreation
}

// dated is a file's date as used to choose its folder.
type dated struct {
	t    time.Time
	prec Precision
	src  DateSource

	// own and ownSrc are set when a forced date replaced a date the file has itself
	// that falls outside the forced date. They are logged and counted.
	own    time.Time
	ownSrc DateSource
}

// conflicted reports whether a forced date overrode a different date of the file's own.
func (d dated) conflicted() bool { return d.ownSrc != "" }

// dateFile works out the date to file f under: the forced date if there is one,
// otherwise the file's own date (see captureDate), otherwise the fallback date. movie
// is f's movie header if it is a video with one, and meta its Takeout sidecar.
func (im *Importer) dateFile(f source.File, movie *media.MovieHeader, meta takeout.Metadata) dated {
	if g := im.opts.ForcedDate; g != nil {
		d := dated{t: g.Time, prec: g.Precision, src: DateForced}
		// The file's own date is still read so that an unexpected one gets reported.
		if own, src := captureDate(f, movie, meta, im.opts.Location); src != DateUnknown && !g.Contains(own) {
			d.own, d.ownSrc = own, src
		}
		return d
	}
	t, src := captureDate(f, movie, meta, im.opts.Location)
	if src == DateUnknown {
		if g := im.opts.FallbackDate; g != nil {
			return dated{t: g.Time, prec: g.Precision, src: DateFallback}
		}
	}
	return dated{t: t, src: src}
}

// captureDate works out when f was taken. It tries, in order:
//
//  1. the EXIF data (formats that carry it),
//  2. the Takeout sidecar's photoTakenTime,
//  3. a video's recording time from its movie header,
//  4. the sidecar's creationTime (upload time: a last resort before guessing),
//  5. a date embedded in the file name.
//
// The sidecar comes before the movie header because some apps rewrite that header
// when they re-encode a video, while Google's photoTakenTime is the original's.
//
// The returned time is in the zone its date should be read in: EXIF wall-clock times
// and Apple video creation dates as recorded, everything else in loc.
func captureDate(f source.File, movie *media.MovieHeader, meta takeout.Metadata, loc *time.Location) (time.Time, DateSource) {
	if media.HasEXIF(f.Name()) {
		if rc, err := f.Open(); err == nil {
			t, ok := media.EXIFDate(rc)
			rc.Close()
			if ok {
				return t, DateFromEXIF
			}
		}
	}

	if t, ok := meta.PhotoTakenTime.Time(); ok {
		return t.In(loc), DateFromJSONTaken
	}
	if movie != nil {
		if t, ok := movie.Created(loc); ok {
			return t, DateFromVideo
		}
	}
	if t, ok := meta.CreationTime.Time(); ok {
		return t.In(loc), DateFromJSONCreation
	}

	if t, ok := media.DateFromFilename(f.Name(), loc); ok {
		return t, DateFromFilename
	}
	return time.Time{}, DateUnknown
}
