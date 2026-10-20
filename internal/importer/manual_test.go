package importer

import (
	"bytes"
	"log"
	"path/filepath"
	"strings"
	"testing"
)

func givenDate(t *testing.T, s string) *GivenDate {
	t.Helper()
	g, err := ParseGivenDate(s)
	if err != nil {
		t.Fatal(err)
	}
	return &g
}

func TestImport_ForcedDateOverridesOwnDates(t *testing.T) {
	var logBuf bytes.Buffer
	layout := DefaultLayout()
	layout.Event = "Fireworks"
	im, _ := newTestImporter(t, Options{
		Layout:     layout,
		ForcedDate: givenDate(t, "2019-07-04"),
		Log:        log.New(&logBuf, "", 0),
	})
	report := run(t, im, sourceOf(
		newMemFile("IMG_20230514.jpg", "has its own date"),
		newMemFile("IMG_20190704_2100.jpg", "own date agrees"),
		newMemFile("random.jpg", "undated"),
	))

	lib := im.opts.Library
	for _, name := range []string{"IMG_20230514.jpg", "IMG_20190704_2100.jpg", "random.jpg"} {
		readFile(t, lib, "2019", "2019-07-04 Fireworks", name)
	}
	if report.Imported[DateForced] != 3 || report.DateConflicts != 1 {
		t.Errorf("forced = %d, conflicts = %d; want 3 and 1", report.Imported[DateForced], report.DateConflicts)
	}
	if !strings.Contains(logBuf.String(), "own date is 2023-05-14, from filename") {
		t.Errorf("log does not record the overridden date:\n%s", logBuf.String())
	}
}

func TestImport_FallbackDateOnlyFillsIn(t *testing.T) {
	im, _ := newTestImporter(t, Options{FallbackDate: givenDate(t, "1998")})
	report := run(t, im, sourceOf(
		newMemFile("IMG_20230514.jpg", "has its own date"),
		newMemFile("scan_001.jpg", "undated"),
	))

	lib := im.opts.Library
	readFile(t, lib, "2023", "2023-05-14", "IMG_20230514.jpg")
	readFile(t, lib, "1998", "scan_001.jpg")
	if report.Imported[DateFallback] != 1 || report.Imported[DateFromFilename] != 1 {
		t.Errorf("report = %+v", report.Imported)
	}
}

func TestImport_ManualAlbumSkipsDuplicates(t *testing.T) {
	layout := DefaultLayout()
	layout.Album = "Wedding"
	im, _ := newTestImporter(t, Options{Layout: layout})
	run(t, im, sourceOf(newMemFile("a.jpg", "one")))
	report := run(t, im, sourceOf(newMemFile("a.jpg", "one"), newMemFile("b.jpg", "two")))

	if got := listDir(t, filepath.Join(im.opts.Library, "Albums", "Wedding")); len(got) != 2 {
		t.Errorf("album holds %v, want a.jpg and b.jpg", got)
	}
	if report.Duplicates != 1 {
		t.Errorf("duplicates = %d, want 1", report.Duplicates)
	}
}

func TestReport_PrintManualRows(t *testing.T) {
	var buf bytes.Buffer
	Report{Imported: map[DateSource]int{DateFromEXIF: 1}}.Print(&buf)
	if strings.Contains(buf.String(), "-date") {
		t.Errorf("unused manual date rows should be hidden:\n%s", buf.String())
	}
	buf.Reset()
	Report{Imported: map[DateSource]int{DateForced: 3}, DateConflicts: 2}.Print(&buf)
	for _, want := range []string{"dated by -date            : 3", "own date differed (log) : 2"} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("report missing %q:\n%s", want, buf.String())
		}
	}
}
