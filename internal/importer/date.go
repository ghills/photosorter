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
	DateFromJSONCreation DateSource = "json_creation"
	DateFromFilename     DateSource = "filename"
	DateUnknown          DateSource = "unknown"

	// Dates given for a manual import: DateForced replaces every file's own date,
	// DateFallback is used only where captureDate finds none.
	DateForced   DateSource = "forced"
	DateFallback DateSource = "fallback"
)

// dateSources lists every DateSource in priority order, for reporting.
var dateSources = []DateSource{DateForced, DateFromEXIF, DateFromJSONTaken, DateFromJSONCreation, DateFromFilename, DateFallback, DateUnknown}

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
// otherwise the file's own date (see captureDate), otherwise the fallback date.
func (im *Importer) dateFile(f source.File) dated {
	if g := im.opts.ForcedDate; g != nil {
		d := dated{t: g.Time, prec: g.Precision, src: DateForced}
		// The file's own date is still read so that an unexpected one gets reported.
		if own, src := captureDate(f, im.opts.Location); src != DateUnknown && !g.Contains(own) {
			d.own, d.ownSrc = own, src
		}
		return d
	}
	t, src := captureDate(f, im.opts.Location)
	if src == DateUnknown {
		if g := im.opts.FallbackDate; g != nil {
			return dated{t: g.Time, prec: g.Precision, src: DateFallback}
		}
	}
	return dated{t: t, src: src}
}

// captureDate works out when f was taken. It tries, in order:
//
//  1. the EXIF header (formats that carry one),
//  2. the Takeout sidecar's photoTakenTime,
//  3. the sidecar's creationTime (upload time: a last resort before guessing),
//  4. a date embedded in the file name.
//
// The returned time is in the zone its date should be read in: EXIF wall-clock times
// as recorded, everything else in loc.
func captureDate(f source.File, loc *time.Location) (time.Time, DateSource) {
	if media.HasEXIF(f.Name()) {
		if rc, err := f.Open(); err == nil {
			t, ok := media.EXIFDate(rc)
			rc.Close()
			if ok {
				return t, DateFromEXIF
			}
		}
	}

	if rc, ok := f.OpenSidecar(); ok {
		meta, err := takeout.ReadMetadata(rc)
		rc.Close()
		if err == nil {
			if t, ok := meta.PhotoTakenTime.Time(); ok {
				return t.In(loc), DateFromJSONTaken
			}
			if t, ok := meta.CreationTime.Time(); ok {
				return t.In(loc), DateFromJSONCreation
			}
		}
	}

	if t, ok := media.DateFromFilename(f.Name(), loc); ok {
		return t, DateFromFilename
	}
	return time.Time{}, DateUnknown
}
