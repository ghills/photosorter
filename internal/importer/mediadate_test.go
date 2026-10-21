package importer

import (
	"path/filepath"
	"testing"
	"time"

	"photosorter/internal/media/mediatest"
	"photosorter/internal/source"
)

// iPhone photos are HEIC with no date in the file name, so they depend on EXIF.
func TestRun_DatesHEICFromEXIF(t *testing.T) {
	taken := time.Date(2023, 5, 14, 23, 30, 0, 0, time.UTC)
	f := &memFile{name: "IMG_1234.HEIC", content: mediatest.HEIC(taken, mediatest.HEICOptions{})}
	f.size = int64(len(f.content))

	// The EXIF wall clock is used as recorded, so the zone doesn't move it to the 15th.
	loc, _ := time.LoadLocation("Pacific/Auckland")
	im, _ := newTestImporter(t, Options{Location: loc})
	report := run(t, im, sourceOf(f))
	if report.Imported[DateFromEXIF] != 1 {
		t.Fatalf("report = %+v", report)
	}
	readFile(t, im.opts.Library, "2023", "2023-05-14", "IMG_1234.HEIC")
}

func TestRun_DatesVideoFromMovieHeader(t *testing.T) {
	recorded := time.Date(2023, 1, 1, 0, 30, 0, 0, time.UTC)
	for _, tc := range []struct {
		name    string
		zone    string
		sidecar string
		wantSrc DateSource
		want    string
	}{
		{"recording time in UTC", "UTC", "", DateFromVideo, "2023/2023-01-01"},
		{"recording time is an instant read in -tz", "Pacific/Honolulu", "", DateFromVideo, "2022/2022-12-31"},
		{"sidecar taken time wins", "UTC", `{"photoTakenTime":{"timestamp":"1688169600"}}`, DateFromJSONTaken, "2023/2023-07-01"},
		{"recording time beats sidecar upload time", "UTC", `{"creationTime":{"timestamp":"1688169600"}}`, DateFromVideo, "2023/2023-01-01"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			loc, err := time.LoadLocation(tc.zone)
			if err != nil {
				t.Fatal(err)
			}
			f := &memFile{name: "clip.mov", content: mediatest.MP4(recorded, 10*time.Second, 64), sidecar: tc.sidecar}
			f.size = int64(len(f.content))
			im, _ := newTestImporter(t, Options{Location: loc, SkipVideosUpTo: 3 * time.Second})
			report := run(t, im, sourceOf(f))
			if report.Imported[tc.wantSrc] != 1 {
				t.Fatalf("report = %+v", report)
			}
			readFile(t, im.opts.Library, filepath.FromSlash(tc.want), "clip.mov")
		})
	}
}

// A video too big to buffer is read through its zip stream, once for both the
// short-video check and the date.
func TestRun_DatesUnbufferedZippedVideo(t *testing.T) {
	recorded := time.Date(2021, 8, 9, 12, 0, 0, 0, time.UTC)
	short := mediatest.MP4(recorded, time.Second, 2048)
	long := mediatest.MP4(recorded, time.Minute, 4096)
	zipPath := filepath.Join(t.TempDir(), "takeout.zip")
	writeZip(t, zipPath, map[string]string{"short.mp4": string(short), "long.mp4": string(long)})

	im, _ := newTestImporter(t, Options{Location: time.UTC, SkipVideosUpTo: 3 * time.Second, BufferLimit: 1})
	report := run(t, im, openZip(t, zipPath))
	if report.Imported[DateFromVideo] != 1 || report.ShortVideos != 1 {
		t.Fatalf("report = %+v", report)
	}
	readFile(t, im.opts.Library, "2021", "2021-08-09", "long.mp4")
}

// An iPhone video's creation date is local time with its offset, so it gives the day
// where it was recorded, whatever -tz says. The UTC header time alone would give the 15th.
func TestRun_DatesAppleVideoFromLocalCreationDate(t *testing.T) {
	header := time.Date(2023, 5, 15, 6, 30, 0, 0, time.UTC)
	f := &memFile{name: "IMG_0042.MOV", content: mediatest.AppleMOV(header, "2023-05-14T23:30:00-0700")}
	f.size = int64(len(f.content))

	loc, _ := time.LoadLocation("Pacific/Auckland")
	im, _ := newTestImporter(t, Options{Location: loc, SkipVideosUpTo: 3 * time.Second})
	report := run(t, im, sourceOf(f))
	if report.Imported[DateFromVideo] != 1 {
		t.Fatalf("report = %+v", report)
	}
	readFile(t, im.opts.Library, "2023", "2023-05-14", "IMG_0042.MOV")
}

func TestRun_DatesM4VAnd3GP(t *testing.T) {
	recorded := time.Date(2012, 6, 1, 12, 0, 0, 0, time.UTC)
	var files []source.File
	for _, name := range []string{"clip.m4v", "old-phone.3gp", "motion.3gp"} {
		d := 10 * time.Second
		if name == "motion.3gp" {
			d = time.Second
		}
		f := &memFile{name: name, content: mediatest.MP4(recorded, d, len(name))}
		f.size = int64(len(f.content))
		files = append(files, f)
	}
	im, _ := newTestImporter(t, Options{Location: time.UTC, SkipVideosUpTo: 3 * time.Second})
	report := run(t, im, sourceOf(files...))
	if report.Imported[DateFromVideo] != 2 || report.ShortVideos != 1 {
		t.Fatalf("report = %+v", report)
	}
	readFile(t, im.opts.Library, "2012", "2012-06-01", "clip.m4v")
	readFile(t, im.opts.Library, "2012", "2012-06-01", "old-phone.3gp")
}
