package importer

import (
	"path/filepath"
	"testing"
	"time"
)

func TestParseGivenDate(t *testing.T) {
	tests := []struct {
		in   string
		want string
		prec Precision
	}{
		{"2019", "2019", YearPrecision},
		{"2019-07", "2019-07", MonthPrecision},
		{"2019-07-04", "2019-07-04", DayPrecision},
		{"2020-02-29", "2020-02-29", DayPrecision},
	}
	for _, tc := range tests {
		g, err := ParseGivenDate(tc.in)
		if err != nil || g.String() != tc.want || g.Precision != tc.prec {
			t.Errorf("ParseGivenDate(%q) = %v (precision %d), %v; want %s (precision %d)", tc.in, g, g.Precision, err, tc.want, tc.prec)
		}
	}
	for _, bad := range []string{"", "19", "2019-7", "2019/07", "2019-13", "2019-02-30", "2019-07-04-01", "July 2019", "-019"} {
		if g, err := ParseGivenDate(bad); err == nil {
			t.Errorf("ParseGivenDate(%q) = %v, want an error", bad, g)
		}
	}
}

func TestGivenDate_Contains(t *testing.T) {
	when := time.Date(2019, 7, 4, 23, 0, 0, 0, time.UTC)
	for in, want := range map[string]bool{
		"2019": true, "2018": false,
		"2019-07": true, "2019-08": false,
		"2019-07-04": true, "2019-07-05": false,
	} {
		g, _ := ParseGivenDate(in)
		if got := g.Contains(when); got != want {
			t.Errorf("%s.Contains(2019-07-04) = %v, want %v", in, got, want)
		}
	}
}

func TestLayout_DirPartialDatesAndEvents(t *testing.T) {
	plain := "photos/IMG_1.jpg"
	takeoutAlbum := "Takeout/Google Photos/Hawaii/IMG_1.jpg"
	withEvent := func(l Layout, event string) Layout { l.Event = event; return l }
	withAlbum := func(l Layout, album string) Layout { l.Album = album; return l }
	format := func(f string) Layout { l := DefaultLayout(); l.DateFormat = f; return l }
	def := DefaultLayout()

	tests := []struct {
		name   string
		layout Layout
		path   string
		date   string
		src    DateSource
		want   string
	}{
		{"day", def, plain, "2019-07-04", DateForced, "2019/2019-07-04"},
		{"month", def, plain, "2019-07", DateForced, "2019/2019-07"},
		{"year", def, plain, "2019", DateForced, "2019"},
		{"day event", withEvent(def, "Fireworks"), plain, "2019-07-04", DateForced, "2019/2019-07-04 Fireworks"},
		{"month event", withEvent(def, "Road trip"), plain, "2019-07", DateForced, "2019/2019-07 Road trip"},
		{"year event", withEvent(def, "Scans"), plain, "2019", DateForced, "2019/2019 Scans"},
		{"event on own date", withEvent(def, "Hawaii"), plain, "2023-05-14", DateFromEXIF, "2023/2023-05-14 Hawaii"},
		{"event leaves unknown alone", withEvent(def, "Hawaii"), plain, "2023-05-14", DateUnknown, "Unknown_Date"},
		{"album", withAlbum(def, "Grandma's box"), plain, "2019-07-04", DateForced, "Albums/Grandma's box"},
		{"album beats takeout album", withAlbum(def, "Mine"), takeoutAlbum, "2019", DateUnknown, "Albums/Mine"},
		{"flat format, year", format("2006-01-02"), plain, "2019", DateFallback, "2019"},
		{"flat format, month", format("2006-01-02"), plain, "2019-07", DateFallback, "2019-07"},
		{"year/month format, month", format("2006/01"), plain, "2019-07", DateForced, "2019/07"},
		{"year/month format, year", format("2006/01"), plain, "2019", DateForced, "2019"},
		{"month names, month", format("2006/Jan"), plain, "2019-07", DateForced, "2019/Jul"},
		{"weekday needs a day", format("2006/2006-01 Mon"), plain, "2019-07", DateForced, "2019/2019-07"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g, err := ParseGivenDate(tc.date)
			if err != nil {
				t.Fatal(err)
			}
			if got := tc.layout.Dir(tc.path, g.Time, g.Precision, tc.src); got != filepath.FromSlash(tc.want) {
				t.Errorf("Dir = %q, want %q", got, tc.want)
			}
		})
	}
}
