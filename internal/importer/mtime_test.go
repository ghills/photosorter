package importer

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"photosorter/internal/library"
	"photosorter/internal/media/mediatest"
)

// modTimeOf returns the modification time of the library file at rel.
func modTimeOf(t *testing.T, lib, rel string) time.Time {
	t.Helper()
	info, err := os.Stat(filepath.Join(lib, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return info.ModTime()
}

// onlyFile returns the path, relative to lib, of the one media file imported into it.
func onlyFile(t *testing.T, lib string) string {
	t.Helper()
	var found []string
	filepath.WalkDir(lib, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && filepath.Ext(p) == ".jpg" {
			rel, _ := filepath.Rel(lib, p)
			found = append(found, rel)
		}
		return err
	})
	if len(found) != 1 {
		t.Fatalf("library holds %v, want one file", found)
	}
	return found[0]
}

func TestRun_SetsModTimeToDate(t *testing.T) {
	auckland, _ := time.LoadLocation("Pacific/Auckland")
	exif := &memFile{name: "camera.jpg", content: mediatest.JPEG(time.Date(2023, 5, 14, 23, 30, 0, 0, time.UTC))}
	exif.size = int64(len(exif.content))
	sidecar := newMemFile("google.jpg", "stripped")
	sidecar.sidecar = `{"photoTakenTime":{"timestamp":"1672531200"}}` // 2023-01-01T00:00:00Z
	named := newMemFile("IMG_20220304_101010.jpg", "named")

	im, _ := newTestImporter(t, Options{Location: auckland})
	run(t, im, sourceOf(exif, sidecar, named))
	lib := im.opts.Library
	for _, tc := range []struct {
		rel  string
		want time.Time
	}{
		// The EXIF wall clock, read in -tz: the same day as its folder.
		{"2023/2023-05-14/camera.jpg", time.Date(2023, 5, 14, 23, 30, 0, 0, auckland)},
		{"2023/2023-01-01/google.jpg", time.Unix(1672531200, 0)},
		{"2022/2022-03-04/IMG_20220304_101010.jpg", time.Date(2022, 3, 4, 0, 0, 0, 0, auckland)},
	} {
		if got := modTimeOf(t, lib, tc.rel); !got.Equal(tc.want) {
			t.Errorf("%s: modification time %v, want %v", tc.rel, got, tc.want)
		}
	}
}

func TestRun_SetsModTimeToGivenDates(t *testing.T) {
	ny, _ := time.LoadLocation("America/New_York")
	forced, _ := ParseGivenDate("2019-07")
	im, _ := newTestImporter(t, Options{Location: ny, ForcedDate: &forced})
	run(t, im, sourceOf(newMemFile("IMG_20230514.jpg", "forced")))
	// Midnight on the 1st in -tz, not midnight UTC (the evening of June 30th in New York).
	if got, want := modTimeOf(t, im.opts.Library, onlyFile(t, im.opts.Library)), time.Date(2019, 7, 1, 0, 0, 0, 0, ny); !got.Equal(want) {
		t.Errorf("forced: modification time %v, want %v", got, want)
	}

	fallback, _ := ParseGivenDate("2018")
	im2, _ := newTestImporter(t, Options{Location: ny, FallbackDate: &fallback})
	run(t, im2, sourceOf(newMemFile("random.jpg", "fallback")))
	if got, want := modTimeOf(t, im2.opts.Library, onlyFile(t, im2.opts.Library)), time.Date(2018, 1, 1, 0, 0, 0, 0, ny); !got.Equal(want) {
		t.Errorf("fallback: modification time %v, want %v", got, want)
	}
}

func TestRun_UndatedFileKeepsCopyTime(t *testing.T) {
	before := time.Now().Add(-time.Minute)
	im, _ := newTestImporter(t, Options{})
	run(t, im, sourceOf(newMemFile("random.jpg", "no date")))
	if got := modTimeOf(t, im.opts.Library, "Unknown_Date/random.jpg"); got.Before(before) {
		t.Errorf("modification time %v, want the time of the copy", got)
	}
}

// The database records the modification time the file keeps, so the next sync sees
// it as unchanged instead of hashing it again.
func TestRun_RecordsDatedModTime(t *testing.T) {
	f := newMemFile("google.jpg", "stripped")
	f.sidecar = `{"photoTakenTime":{"timestamp":"1672531200"}}`
	im, db := newTestImporter(t, Options{Location: time.UTC})
	run(t, im, sourceOf(f))
	db.Close() // flush the background writer
	db2, err := library.OpenReadOnly(im.opts.Library)
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()
	files, err := db2.Files()
	if err != nil {
		t.Fatal(err)
	}
	rec, ok := files["2023/2023-01-01/google.jpg"]
	if !ok || rec.ModTime != time.Unix(1672531200, 0).UnixNano() {
		t.Errorf("recorded %+v, %v; want the dated modification time", rec, ok)
	}
}
