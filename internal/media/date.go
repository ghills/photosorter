package media

import (
	"bytes"
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
// r is a whole JPEG, TIFF or HEIF (HEIC) file; the format is detected from its content.
//
// EXIF stores a wall-clock time with no zone. The returned time carries that wall
// clock as-is, so formatting it gives the date the camera showed, whatever zone the
// importer runs in.
func EXIFDate(r io.Reader) (time.Time, bool) {
	head := make([]byte, 8)
	n, _ := io.ReadFull(r, head)
	head = head[:n]

	var tiff io.Reader
	if isHEIF(head) {
		// The meta box sits near the start; read up to the limit so it can be parsed whole.
		rest, err := io.ReadAll(io.LimitReader(r, exifReadLimit-int64(n)))
		if err != nil {
			return time.Time{}, false
		}
		data, err := heifEXIF(append(head, rest...), r)
		if err != nil {
			return time.Time{}, false
		}
		tiff = bytes.NewReader(data)
	} else {
		tiff = io.MultiReader(bytes.NewReader(head), io.LimitReader(r, exifReadLimit-int64(n)))
	}

	x, err := exif.Decode(tiff)
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
