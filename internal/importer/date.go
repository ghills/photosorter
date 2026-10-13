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
)

// dateSources lists every DateSource in priority order, for reporting.
var dateSources = []DateSource{DateFromEXIF, DateFromJSONTaken, DateFromJSONCreation, DateFromFilename, DateUnknown}

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
