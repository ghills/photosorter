package importer

import (
	"os"
	"time"
)

// modTime returns the modification time an imported file should get for date, or
// false if its date is unknown. Many viewers sort by modification time, and a fresh
// copy would otherwise carry the time of the import.
//
// Wall-clock dates are read in loc, the zone sidecar timestamps are dated in, so the
// file's modification time falls on the day of the folder it is in. These are EXIF
// times, which goexif returns in the machine's zone unless the camera recorded one,
// and -date and -fallback-date, which are midnight UTC. Every other date is already
// an instant.
func modTime(date dated, loc *time.Location) (time.Time, bool) {
	t := date.t
	switch date.src {
	case DateUnknown:
		return time.Time{}, false
	case DateForced, DateFallback:
		t = wallClockIn(t, loc)
	case DateFromEXIF:
		if t.Location() == time.Local {
			t = wallClockIn(t, loc)
		}
	}
	return t, !t.IsZero()
}

// wallClockIn returns the time in loc that shows the same date and clock time as t.
func wallClockIn(t time.Time, loc *time.Location) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), loc)
}

// setModTime gives the file at path the modification time for date, leaving its
// access time alone. It does nothing for an undated file.
func setModTime(path string, date dated, loc *time.Location) error {
	t, ok := modTime(date, loc)
	if !ok {
		return nil
	}
	return os.Chtimes(path, time.Time{}, t)
}
