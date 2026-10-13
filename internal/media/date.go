package media

import (
	"io"
	"path/filepath"
	"regexp"
	"strconv"
	"time"

	"github.com/rwcarlsen/goexif/exif"
)

// exifReadLimit bounds how much of a file EXIF decoding may read. The header sits at
// the start of the file, so this is plenty and keeps large files cheap.
const exifReadLimit = 256 << 10

// EXIFDate returns the DateTimeOriginal (or DateTime) recorded in r's EXIF header.
//
// EXIF stores a wall-clock time with no zone. The returned time carries that wall
// clock as-is, so formatting it gives the date the camera showed, whatever zone the
// importer runs in.
func EXIFDate(r io.Reader) (time.Time, bool) {
	x, err := exif.Decode(io.LimitReader(r, exifReadLimit))
	if err != nil {
		return time.Time{}, false
	}
	t, err := x.DateTime()
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

// filenameDate matches YYYYMMDD, YYYY-MM-DD or YYYY_MM_DD, optionally after a common
// camera or app prefix such as IMG_, VID-, Screenshot_ or "WhatsApp Image ".
var filenameDate = regexp.MustCompile(`(?i)(?:^|IMG[-_]?|VID[-_]?|Screenshot[-_]?|WhatsApp Image )?((?:19|20)\d{2})[-_]?(\d{2})[-_]?(\d{2})`)

// DateFromFilename extracts a date from common camera file names such as
// IMG_20230514_120000.jpg. The result is midnight of that day in loc.
func DateFromFilename(name string, loc *time.Location) (time.Time, bool) {
	m := filenameDate.FindStringSubmatch(filepath.Base(name))
	if m == nil {
		return time.Time{}, false
	}
	year, _ := strconv.Atoi(m[1])
	month, _ := strconv.Atoi(m[2])
	day, _ := strconv.Atoi(m[3])
	// time.Date normalizes out-of-range values (Feb 30 becomes Mar 2), so a date that
	// doesn't survive the round trip isn't a real one.
	t := time.Date(year, time.Month(month), day, 0, 0, 0, 0, loc)
	if t.Year() != year || t.Month() != time.Month(month) || t.Day() != day {
		return time.Time{}, false
	}
	return t, true
}
