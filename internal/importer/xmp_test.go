package importer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"photosorter/internal/media/mediatest"
)

// googleSidecar is a Takeout sidecar with every field an XMP sidecar carries over.
const googleSidecar = `{
	"title": "IMG_1234.jpg",
	"description": "Top of the tower",
	"photoTakenTime": {"timestamp": "1684089000"},
	"creationTime": {"timestamp": "1684175400"},
	"geoData": {"latitude": 48.8584, "longitude": 2.2945, "altitude": 300.0},
	"people": [{"name": "Alice"}],
	"favorited": true
}` // taken 2023-05-14 18:30 UTC

func TestRun_WritesXMPWhenDatedFromSidecar(t *testing.T) {
	f := newMemFile("IMG_1234.jpg", "stripped by Google")
	f.sidecar = googleSidecar
	paris, _ := time.LoadLocation("Europe/Paris")
	im, _ := newTestImporter(t, Options{Location: paris})
	report := run(t, im, sourceOf(f))
	if report.Imported[DateFromJSONTaken] != 1 || report.XMPWritten != 1 {
		t.Fatalf("report = %+v", report)
	}
	doc := readFile(t, im.opts.Library, "2023", "2023-05-14", "IMG_1234.jpg.xmp")
	for _, want := range []string{
		"<exif:DateTimeOriginal>2023-05-14T20:30:00+02:00</exif:DateTimeOriginal>", // in -tz
		"<exif:GPSLatitude>48,51.504000N</exif:GPSLatitude>",
		"<exif:GPSLongitude>2,17.670000E</exif:GPSLongitude>",
		"<exif:GPSAltitude>30000/100</exif:GPSAltitude>",
		"Top of the tower",
		"<rdf:li>Alice</rdf:li>",
		"<xmp:Rating>5</xmp:Rating>",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("missing %s in\n%s", want, doc)
		}
	}
}

// An upload time is good enough for a folder but isn't when the photo was taken, so
// it isn't written as the capture time.
func TestRun_XMPFromCreationTimeHasNoCaptureTime(t *testing.T) {
	f := newMemFile("upload.jpg", "x")
	f.sidecar = `{"creationTime": {"timestamp": "1684175400"}, "description": "uploaded"}`
	im, _ := newTestImporter(t, Options{Location: time.UTC})
	if report := run(t, im, sourceOf(f)); report.Imported[DateFromJSONCreation] != 1 || report.XMPWritten != 1 {
		t.Fatalf("report = %+v", report)
	}
	doc := readFile(t, im.opts.Library, "2023", "2023-05-15", "upload.jpg.xmp")
	if strings.Contains(doc, "DateTimeOriginal") || !strings.Contains(doc, "uploaded") {
		t.Errorf("sidecar:\n%s", doc)
	}

	// With nothing but the upload time, there is nothing to write.
	bare := newMemFile("bare.jpg", "y")
	bare.sidecar = `{"creationTime": {"timestamp": "1684175400"}}`
	if report := run(t, im, sourceOf(bare)); report.XMPWritten != 0 {
		t.Errorf("report = %+v", report)
	}
	if _, err := os.Stat(filepath.Join(im.opts.Library, "2023", "2023-05-15", "bare.jpg.xmp")); err == nil {
		t.Error("an empty XMP sidecar was written")
	}
}

// Files that didn't need the sidecar for their date keep their own metadata.
func TestRun_NoXMPWhenSidecarNotUsed(t *testing.T) {
	taken := time.Date(2023, 5, 14, 12, 0, 0, 0, time.UTC)
	exif := &memFile{name: "camera.jpg", content: mediatest.JPEG(taken), sidecar: googleSidecar}
	exif.size = int64(len(exif.content))
	forced := newMemFile("forced.jpg", "forced")
	forced.sidecar = googleSidecar
	named := newMemFile("IMG_20230514_120000.jpg", "named")

	im, _ := newTestImporter(t, Options{})
	run(t, im, sourceOf(exif, named))
	given, _ := ParseGivenDate("2020-01-01")
	im2, _ := newTestImporter(t, Options{ForcedDate: &given})
	run(t, im2, sourceOf(forced))

	for _, lib := range []string{im.opts.Library, im2.opts.Library} {
		filepath.WalkDir(lib, func(p string, d os.DirEntry, err error) error {
			if err == nil && strings.HasSuffix(p, ".xmp") {
				t.Errorf("unexpected XMP sidecar %s", p)
			}
			return err
		})
	}
}

func TestRun_NoXMPOption(t *testing.T) {
	f := newMemFile("IMG_1234.jpg", "x")
	f.sidecar = googleSidecar
	im, _ := newTestImporter(t, Options{NoXMP: true, Location: time.UTC})
	if report := run(t, im, sourceOf(f)); report.XMPWritten != 0 {
		t.Errorf("report = %+v", report)
	}
	if names := listDir(t, filepath.Join(im.opts.Library, "2023", "2023-05-14")); len(names) != 1 {
		t.Errorf("library has %v, want only the photo", names)
	}
}

// The sidecar follows the name the file was given. A name whose sidecar was left
// behind counts as taken, so that sidecar is never replaced or attached to a
// different photo.
func TestRun_XMPFollowsRenamedFileAndSkipsLeftoverSidecar(t *testing.T) {
	im, _ := newTestImporter(t, Options{Location: time.UTC})
	dir := filepath.Join(im.opts.Library, "2023", "2023-05-14")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "IMG_1234.jpg"), []byte("someone else"), 0644)
	os.WriteFile(filepath.Join(dir, "IMG_1234_1.jpg.xmp"), []byte("mine"), 0644)

	a := newMemFile("IMG_1234.jpg", "first")
	a.sidecar = googleSidecar
	if report := run(t, im, sourceOf(a)); report.TotalImported() != 1 || report.XMPWritten != 1 {
		t.Fatalf("report = %+v", report)
	}
	if _, err := os.Stat(filepath.Join(dir, "IMG_1234_1.jpg")); err == nil {
		t.Error("file placed next to a leftover sidecar")
	}
	if got := readFile(t, dir, "IMG_1234_1.jpg.xmp"); got != "mine" {
		t.Errorf("leftover sidecar replaced:\n%s", got)
	}
	readFile(t, dir, "IMG_1234_2.jpg")
	readFile(t, dir, "IMG_1234_2.jpg.xmp")
}

func TestRun_DryRunWritesNoXMP(t *testing.T) {
	f := newMemFile("IMG_1234.jpg", "x")
	f.sidecar = googleSidecar
	im, _ := newTestImporter(t, Options{DryRun: true, Location: time.UTC})
	if report := run(t, im, sourceOf(f)); report.XMPWritten != 1 {
		t.Errorf("report = %+v, want the sidecar counted as would-be-written", report)
	}
	if _, err := os.Stat(filepath.Join(im.opts.Library, "2023")); err == nil {
		t.Error("dry run wrote files")
	}
}

// A photo dated from EXIF needs nothing from its Takeout sidecar, so it isn't read.
func TestRun_EXIFDatedPhotoSkipsSidecar(t *testing.T) {
	f := &memFile{name: "camera.jpg", content: mediatest.JPEG(time.Date(2023, 5, 14, 12, 0, 0, 0, time.UTC)), sidecar: googleSidecar}
	im, _ := newTestImporter(t, Options{Location: time.UTC})
	if report := run(t, im, sourceOf(f)); report.Imported[DateFromEXIF] != 1 {
		t.Fatalf("report = %+v", report)
	}
	if n := f.sidecarOpens.Load(); n != 0 {
		t.Errorf("sidecar opened %d times, want 0", n)
	}
}

func TestReport_DryRunSaysXMPToWrite(t *testing.T) {
	var b strings.Builder
	Report{DryRun: true, XMPWritten: 2}.Print(&b)
	if !strings.Contains(b.String(), "XMP sidecars to write") || strings.Contains(b.String(), "XMP sidecars written") {
		t.Errorf("report:\n%s", b.String())
	}
}
