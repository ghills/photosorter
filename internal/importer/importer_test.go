package importer

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"photosorter/internal/library"
	"photosorter/internal/source"
)

func TestRun_ImportsAndSkipsDuplicates(t *testing.T) {
	im, _ := newTestImporter(t, Options{})
	lib := im.opts.Library

	report := run(t, im, sourceOf(
		newMemFile("IMG_20230101.jpg", "image content A"),
		newMemFile("IMG_20230102.jpg", "image content A"), // same bytes, different name
	))

	if got := readFile(t, lib, "2023-01-01", "IMG_20230101.jpg"); got != "image content A" {
		t.Errorf("imported content = %q", got)
	}
	if names := listDir(t, filepath.Join(lib, "2023-01-02")); len(names) != 0 {
		t.Errorf("duplicate was written: %v", names)
	}
	if report.TotalImported() != 1 || report.Imported[DateFromFilename] != 1 || report.Duplicates != 1 {
		t.Errorf("report = %+v", report)
	}

	// A second run recognises everything as already imported.
	report = run(t, im, sourceOf(newMemFile("IMG_20230101.jpg", "image content A")))
	if report.TotalImported() != 0 || report.Duplicates != 1 {
		t.Errorf("second run report = %+v", report)
	}
}

// Takeout puts album media in a year folder too. Whichever copy comes first, the album
// copy decides where the file goes.
func TestRun_AlbumCopyWinsOverYearFolder(t *testing.T) {
	im, _ := newTestImporter(t, Options{Workers: 1})
	report := run(t, im, sourceOf(
		newMemFile("Takeout/Google Photos/Photos from 2023/IMG_20230101.jpg", "same"),
		newMemFile("Takeout/Google Photos/Trip/IMG_20230101.jpg", "same"),
	))
	if got := readFile(t, im.opts.Library, "Albums", "Trip", "IMG_20230101.jpg"); got != "same" {
		t.Errorf("album copy = %q", got)
	}
	if names := listDir(t, filepath.Join(im.opts.Library, "2023-01-01")); len(names) != 0 {
		t.Errorf("year-folder copy imported: %v", names)
	}
	if report.TotalImported() != 1 || report.Duplicates != 1 {
		t.Errorf("report = %+v", report)
	}
}

// Many workers importing different content under the same name into the same folder
// must each keep their own file, and identical content must be imported once.
func TestRun_ConcurrentSameNameAndDuplicates(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{}
	const unique = 40
	for i := range unique {
		files[fmt.Sprintf("dir%d/IMG_20230101.jpg", i)] = fmt.Sprintf("unique content %d", i)
		files[fmt.Sprintf("same/IMG_20230102_%d.jpg", i)] = "shared content"
	}
	writeZip(t, filepath.Join(dir, "test.zip"), files)

	im, _ := newTestImporter(t, Options{Workers: 16})
	run(t, im, openZip(t, filepath.Join(dir, "test.zip")))

	lib := im.opts.Library
	if got := listDir(t, filepath.Join(lib, "2023-01-01")); len(got) != unique {
		t.Errorf("2023-01-01 has %d files, want %d (same-name files overwritten?)", len(got), unique)
	}
	if got := listDir(t, filepath.Join(lib, "2023-01-02")); len(got) != 1 {
		t.Errorf("2023-01-02 has %v, want 1 file (identical content imported more than once?)", got)
	}
	for _, d := range []string{"2023-01-01", "2023-01-02"} {
		for _, name := range listDir(t, filepath.Join(lib, d)) {
			if strings.HasSuffix(name, ".tmp") {
				t.Errorf("leftover temp file %s/%s", d, name)
			}
		}
	}
}

func TestRun_StopsOnCancelledContext(t *testing.T) {
	var files []source.File
	for i := range 20 {
		files = append(files, newMemFile(fmt.Sprintf("IMG_20230101_%d.jpg", i), fmt.Sprintf("content %d", i)))
	}
	im, _ := newTestImporter(t, Options{Workers: 4})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := im.Run(ctx, sourceOf(files...)); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run error = %v, want context.Canceled", err)
	}
	if names := listDir(t, filepath.Join(im.opts.Library, "2023-01-01")); len(names) != 0 {
		t.Errorf("%d files written after cancellation", len(names))
	}
}

func TestRun_FailsWhenDatabaseUnavailable(t *testing.T) {
	im, db := newTestImporter(t, Options{})
	db.Close()

	report, err := im.Run(context.Background(), sourceOf(newMemFile("IMG_20230101.jpg", "content")))
	if err == nil || report.TotalImported() != 0 {
		t.Fatalf("Run = %+v, %v; want an error and nothing imported", report, err)
	}
	if names := listDir(t, filepath.Join(im.opts.Library, "2023-01-01")); len(names) != 0 {
		t.Errorf("files written without being recorded: %v", names)
	}
}

func TestRun_ReturnsFilesFailedError(t *testing.T) {
	im, _ := newTestImporter(t, Options{Workers: 2})
	report, err := im.Run(context.Background(), sourceOf(failingFile{"a.jpg"}, failingFile{"b.jpg"}))
	var failed *FilesFailedError
	if !errors.As(err, &failed) || failed.Count != 2 || report.Failed != 2 {
		t.Errorf("Run = %+v, %v; want FilesFailedError{Count: 2}", report, err)
	}
}

func TestRun_CountsSourceWarnings(t *testing.T) {
	im, _ := newTestImporter(t, Options{})
	report := run(t, im, []source.Source{listSource{warnings: []string{"x: denied", "y: denied"}}})
	if report.Unreadable != 2 {
		t.Errorf("Unreadable = %d, want 2", report.Unreadable)
	}
}

func TestRun_DryRunWritesNothingButDetectsDuplicates(t *testing.T) {
	im, db := newTestImporter(t, Options{DryRun: true})
	report := run(t, im, sourceOf(
		newMemFile("IMG_20230101.jpg", "same"),
		newMemFile("IMG_20230102.jpg", "same"),
	))
	if report.TotalImported() != 1 || report.Duplicates != 1 || !report.DryRun {
		t.Errorf("report = %+v, want 1 imported and 1 duplicate", report)
	}
	for _, name := range listDir(t, im.opts.Library) {
		if !strings.HasPrefix(name, library.DBFileName) {
			t.Errorf("dry run wrote %s", name)
		}
	}
	if n, _ := db.Count(); n != 0 {
		t.Errorf("dry run recorded %d hashes", n)
	}
}

// mp4 returns a minimal MP4 whose mvhd box declares the given duration.
func mp4(d time.Duration) []byte {
	body := make([]byte, 20)
	binary.BigEndian.PutUint32(body[12:16], 1000)
	binary.BigEndian.PutUint32(body[16:20], uint32(d.Milliseconds()))
	mvhd := append(binary.BigEndian.AppendUint32(nil, uint32(8+len(body))), "mvhd"...)
	mvhd = append(mvhd, body...)
	moov := append(binary.BigEndian.AppendUint32(nil, uint32(8+len(mvhd))), "moov"...)
	return append(moov, mvhd...)
}

func TestRun_SkipsShortVideos(t *testing.T) {
	short := &memFile{name: "VID_20230101_short.mp4", content: mp4(2 * time.Second)}
	long := &memFile{name: "VID_20230101_long.mp4", content: mp4(10 * time.Second)}
	short.size, long.size = int64(len(short.content)), int64(len(long.content))

	im, _ := newTestImporter(t, Options{SkipVideosUpTo: 3 * time.Second, BufferLimit: 16 << 20})
	report := run(t, im, sourceOf(short, long))
	if report.ShortVideos != 1 || report.TotalImported() != 1 {
		t.Errorf("report = %+v, want 1 short video skipped and 1 imported", report)
	}
	if names := listDir(t, filepath.Join(im.opts.Library, "2023-01-01")); len(names) != 1 || names[0] != long.name {
		t.Errorf("imported %v, want only the long video", names)
	}

	// The skipped clip is not recorded, so it is checked again rather than counted as a
	// duplicate.
	if report := run(t, im, sourceOf(short)); report.ShortVideos != 1 || report.Duplicates != 0 {
		t.Errorf("rerun report = %+v, want the short video skipped again", report)
	}

	// With the threshold off, short videos are imported.
	im2, _ := newTestImporter(t, Options{})
	if report := run(t, im2, sourceOf(short)); report.TotalImported() != 1 {
		t.Errorf("threshold off: report = %+v", report)
	}
}

func TestRun_UsesSidecarDateInConfiguredZone(t *testing.T) {
	const sidecar = `{"photoTakenTime":{"timestamp":"1672531200"}}` // 2023-01-01T00:00:00Z
	for _, tc := range []struct{ zone, want string }{
		{"UTC", "2023-01-01"},
		{"Pacific/Auckland", "2023-01-01"}, // 13:00 on the 1st
		{"Pacific/Honolulu", "2022-12-31"}, // 14:00 on the 31st
	} {
		loc, err := time.LoadLocation(tc.zone)
		if err != nil {
			t.Fatal(err)
		}
		f := newMemFile("a.jpg", "x")
		f.sidecar = sidecar
		im, _ := newTestImporter(t, Options{Location: loc})
		report := run(t, im, sourceOf(f))
		if report.Imported[DateFromJSONTaken] != 1 {
			t.Errorf("%s: report = %+v", tc.zone, report)
		}
		if names := listDir(t, filepath.Join(im.opts.Library, tc.want)); len(names) != 1 {
			t.Errorf("%s: expected a.jpg in %s", tc.zone, tc.want)
		}
	}
}

func TestRun_CustomLayout(t *testing.T) {
	layout := Layout{DateFormat: "2006/01", UnknownDir: "Undated", AlbumsDir: "Collections/Albums", KeepAlbums: true}
	im, _ := newTestImporter(t, Options{Layout: layout})
	run(t, im, sourceOf(
		newMemFile("IMG_20230514.jpg", "dated"),
		newMemFile("random.jpg", "undated"),
		newMemFile("Takeout/Google Photos/Trip/IMG_20230601.jpg", "album"),
	))
	lib := im.opts.Library
	for _, p := range []string{"2023/05/IMG_20230514.jpg", "Undated/random.jpg", "Collections/Albums/Trip/IMG_20230601.jpg"} {
		readFile(t, lib, filepath.FromSlash(p))
	}
}

func TestReport_Print(t *testing.T) {
	var buf bytes.Buffer
	Report{DryRun: true, Imported: map[DateSource]int{DateFromEXIF: 2, DateUnknown: 1}, Duplicates: 4, Interrupted: 1}.Print(&buf)
	out := buf.String()
	for _, want := range []string{"DRY RUN", "Imported                    : 3", "dated from EXIF", "Duplicates skipped          : 4", "Interrupted"} {
		if !strings.Contains(out, want) {
			t.Errorf("report missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Unreadable") {
		t.Error("zero-count optional rows should be hidden")
	}
}
