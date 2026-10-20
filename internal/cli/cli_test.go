package cli

import (
	"archive/zip"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func writeZip(t *testing.T, path string, files map[string]string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for name, content := range files {
		w, _ := zw.Create(name)
		w.Write([]byte(content))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	f.Close()
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

// runCLI runs the command and returns its exit code and combined output.
func runCLI(t *testing.T, args ...string) (int, string) {
	t.Helper()
	var out bytes.Buffer
	code := Run(context.Background(), args, &out, &out)
	return code, out.String()
}

// mediaIn lists library files as slash paths, ignoring PhotoSorter's own files.
func mediaIn(t *testing.T, lib string) []string {
	t.Helper()
	var files []string
	filepath.WalkDir(lib, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		name := d.Name()
		if strings.HasPrefix(name, ".photosorter") || strings.HasPrefix(name, "photosorter") {
			return nil
		}
		rel, _ := filepath.Rel(lib, p)
		files = append(files, filepath.ToSlash(rel))
		return nil
	})
	sort.Strings(files)
	return files
}

const (
	photos = "Takeout/Google Photos/"
	year   = photos + "Photos from 2023/"
)

// sidecar returns Takeout JSON whose photoTakenTime is the given Unix time.
func sidecar(unix string) string {
	return `{"title":"x","photoTakenTime":{"timestamp":"` + unix + `","formatted":"..."}}`
}

// takeoutFixture writes a Takeout zip that exercises sidecar matching, albums,
// undated files, edited copies, and content already in the library.
func takeoutFixture(t *testing.T, dir string) string {
	// Long enough that Takeout truncates its sidecar name to 46 characters + ".json".
	const long = "a_very_long_file_name_without_any_date_here.jpg"
	truncated := (long + ".supplemental-metadata")[:46] + ".json"

	path := filepath.Join(dir, "takeout-001.zip")
	writeZip(t, path, map[string]string{
		year + "IMG_0001.jpg":                            "photo one",
		year + "IMG_0001.jpg.supplemental-metadata.json": sidecar("1677931200"), // 2023-03-04 12:00 UTC
		year + long:                   "long name",
		year + truncated:              sidecar("1683806400"), // 2023-05-11 12:00 UTC
		year + "IMG_0002.jpg":         "original",
		year + "IMG_0002.jpg.json":    sidecar("1657281600"), // 2022-07-08 12:00 UTC
		year + "IMG_0002-edited.jpg":  "edited",
		year + "mystery.png":          "no date anywhere",
		year + "copy_of_existing.jpg": "already have",
		photos + "Hawaii/beach.jpg":   "beach",
		photos + "Hawaii/beach.jpg.supplemental-metadata.json": sidecar("1677931200"),
		"Takeout/archive_browser.html":                         "<html>",
	})
	return path
}

func TestRun_EndToEnd(t *testing.T) {
	dir := t.TempDir()
	zipPath := takeoutFixture(t, dir)
	lib := filepath.Join(dir, "Library")
	writeFile(t, filepath.Join(lib, "old", "existing.jpg"), "already have")

	code, out := runCLI(t, "-out", lib, "-tz", "UTC", zipPath)
	if code != ExitOK {
		t.Fatalf("exit code %d\n%s", code, out)
	}
	if !strings.Contains(out, "syncing the library first") {
		t.Errorf("expected an automatic sync of the existing library:\n%s", out)
	}

	want := []string{
		"2022/2022-07-08/IMG_0002-edited.jpg", // edited copy dated by the original's sidecar
		"2022/2022-07-08/IMG_0002.jpg",
		"2023/2023-03-04/IMG_0001.jpg",                                    // supplemental-metadata sidecar
		"2023/2023-05-11/a_very_long_file_name_without_any_date_here.jpg", // truncated sidecar
		"Albums/Hawaii/beach.jpg",
		"Unknown_Date/mystery.png",
		"old/existing.jpg", // pre-existing; its copy in the zip was skipped
	}
	if got := mediaIn(t, lib); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("library contents:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}

	// Importing the same archive again adds nothing.
	code, out = runCLI(t, "-out", lib, "-tz", "UTC", zipPath)
	if code != ExitOK || !strings.Contains(out, "Duplicates skipped          : 7") {
		t.Errorf("second run: exit %d\n%s", code, out)
	}
	if got := mediaIn(t, lib); len(got) != len(want) {
		t.Errorf("second run changed the library: %v", got)
	}
}

func TestRun_ConfigFileLayoutAndFilter(t *testing.T) {
	dir := t.TempDir()
	zipPath := takeoutFixture(t, dir)
	lib := filepath.Join(dir, "Library")
	writeFile(t, filepath.Join(lib, "photosorter.toml"), `
tz = "UTC"
[layout]
date_format = "2006/01"
albums = "ignore"
unknown_dir = "Undated"
[media]
exclude_ext = ["png"]
`)

	// The -date-format flag overrides the file; everything else comes from it.
	code, out := runCLI(t, "-out", lib, "-date-format", "2006/2006-01", zipPath)
	if code != ExitOK {
		t.Fatalf("exit code %d\n%s", code, out)
	}
	want := []string{
		"2022/2022-07/IMG_0002-edited.jpg",
		"2022/2022-07/IMG_0002.jpg",
		"2023/2023-03/IMG_0001.jpg",
		"2023/2023-03/beach.jpg", // albums ignored
		"2023/2023-05/a_very_long_file_name_without_any_date_here.jpg",
		"Undated/copy_of_existing.jpg",
		// mystery.png excluded by the extension filter
	}
	if got := mediaIn(t, lib); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("library contents:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestRun_DryRunWritesNoMedia(t *testing.T) {
	dir := t.TempDir()
	lib := filepath.Join(dir, "Library")
	code, out := runCLI(t, "-out", lib, "-dry-run", takeoutFixture(t, dir))
	if code != ExitOK || !strings.Contains(out, "DRY RUN") {
		t.Fatalf("exit %d\n%s", code, out)
	}
	if _, err := os.Stat(lib); !os.IsNotExist(err) {
		t.Errorf("dry run created the library folder (stat err %v)", err)
	}
}

func TestRun_DryRunSyncRecordsNothing(t *testing.T) {
	lib := t.TempDir()
	writeFile(t, filepath.Join(lib, "2020-01-01", "a.jpg"), "a")
	if code, out := runCLI(t, "-out", lib, "-no-sync", "-sync"); code != ExitOK {
		t.Fatalf("exit %d\n%s", code, out)
	}
	writeFile(t, filepath.Join(lib, "2020-01-01", "b.jpg"), "b")
	if code, out := runCLI(t, "-out", lib, "-dry-run", "-sync"); code != ExitOK || !strings.Contains(out, "2 files hashed") {
		t.Fatalf("exit %d\n%s", code, out)
	}
	if _, out := runCLI(t, "-out", lib, "-sync"); !strings.Contains(out, "1 files tracked") {
		t.Errorf("dry-run sync changed the database:\n%s", out)
	}
}

// Duplicate content inside the library shares one hash; that alone must not trigger
// the automatic sync for files added by other means.
func TestRun_NoSyncWarningForDuplicateContent(t *testing.T) {
	dir := t.TempDir()
	lib := filepath.Join(dir, "Library")
	writeFile(t, filepath.Join(lib, "a", "x.jpg"), "same")
	writeFile(t, filepath.Join(lib, "b", "x.jpg"), "same")
	if code, out := runCLI(t, "-out", lib, "-sync"); code != ExitOK {
		t.Fatalf("exit %d\n%s", code, out)
	}
	code, out := runCLI(t, "-out", lib, takeoutFixture(t, dir))
	if code != ExitOK || strings.Contains(out, "(added by hand?)") {
		t.Errorf("import: exit %d\n%s", code, out)
	}
	if _, out := runCLI(t, "-out", lib, takeoutFixture(t, dir)); strings.Contains(out, "(added by hand?)") {
		t.Errorf("needless sync after import:\n%s", out)
	}
	writeFile(t, filepath.Join(lib, "c", "new.jpg"), "added by hand")
	if _, out := runCLI(t, "-out", lib, takeoutFixture(t, dir)); !strings.Contains(out, "(added by hand?)") {
		t.Errorf("no sync for a file added by other means:\n%s", out)
	}
}

func TestRun_SyncOnly(t *testing.T) {
	lib := t.TempDir()
	writeFile(t, filepath.Join(lib, "2020-01-01", "a.jpg"), "a")
	code, out := runCLI(t, "-out", lib, "-sync")
	if code != ExitOK || !strings.Contains(out, "Sync complete: 1 files hashed") {
		t.Errorf("exit %d\n%s", code, out)
	}
}

// A file deleted from the library stays in the database, so it is skipped on the
// next import until -prune forgets it.
func TestRun_PruneAllowsReimportOfDeletedFiles(t *testing.T) {
	dir := t.TempDir()
	lib := filepath.Join(dir, "Library")
	zip := takeoutFixture(t, dir)
	if code, out := runCLI(t, "-out", lib, zip); code != ExitOK {
		t.Fatalf("import: exit %d\n%s", code, out)
	}
	before := mediaIn(t, lib)
	victim := filepath.Join(lib, filepath.FromSlash(before[0]))
	if err := os.Remove(victim); err != nil {
		t.Fatal(err)
	}

	if code, out := runCLI(t, "-out", lib, "-dry-run", "-prune"); code != ExitOK || !strings.Contains(out, "Would prune 1 cached hash") {
		t.Fatalf("dry-run prune: exit %d\n%s", code, out)
	}
	if code, out := runCLI(t, "-out", lib, "-prune"); code != ExitOK || !strings.Contains(out, "Pruned 1 cached hash") || !strings.Contains(out, filepath.Base(victim)) {
		t.Fatalf("prune: exit %d\n%s", code, out)
	}
	if code, out := runCLI(t, "-out", lib, zip); code != ExitOK {
		t.Fatalf("re-import: exit %d\n%s", code, out)
	}
	if after := mediaIn(t, lib); strings.Join(after, "\n") != strings.Join(before, "\n") {
		t.Errorf("deleted file was not re-imported:\nbefore %v\nafter  %v", before, after)
	}
}

func TestRun_ExitCodes(t *testing.T) {
	dir := t.TempDir()
	if code, _ := runCLI(t, "-h"); code != ExitOK {
		t.Errorf("-h exit = %d", code)
	}
	if code, out := runCLI(t, "-bogus"); code != ExitFailure || !strings.Contains(out, "photosorter -h") {
		t.Errorf("bad flag exit = %d\n%s", code, out)
	}
	os.MkdirAll(filepath.Join(dir, "in"), 0755)
	if code, out := runCLI(t, "-out", filepath.Join(dir, "in", "lib"), "-in", filepath.Join(dir, "in")); code != ExitFailure || !strings.Contains(out, "must not contain") {
		t.Errorf("overlap exit = %d\n%s", code, out)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var buf bytes.Buffer
	zipPath := takeoutFixture(t, dir)
	if code := Run(ctx, []string{"-out", filepath.Join(dir, "lib"), zipPath}, &buf, &buf); code != ExitInterrupted {
		t.Errorf("cancelled exit = %d, want %d\n%s", code, ExitInterrupted, buf.String())
	}
}

func TestRun_WritesLog(t *testing.T) {
	dir := t.TempDir()
	lib := filepath.Join(dir, "Library")
	if code, out := runCLI(t, "-out", lib, takeoutFixture(t, dir)); code != ExitOK {
		t.Fatalf("exit %d\n%s", code, out)
	}
	logs, _ := filepath.Glob(filepath.Join(lib, "photosorter_*.log"))
	if len(logs) != 1 {
		t.Fatalf("found %d log files, want 1", len(logs))
	}
	if data, _ := os.ReadFile(logs[0]); !strings.Contains(string(data), "Imported: "+year+"IMG_0001.jpg") {
		t.Errorf("log content:\n%s", data)
	}
}

// A library imported with the old flat layout is moved into year folders, a second
// -reorganize does nothing, and the database still recognises every file.
func TestRun_Reorganize(t *testing.T) {
	dir := t.TempDir()
	lib := filepath.Join(dir, "Library")
	zip := takeoutFixture(t, dir)
	if code, out := runCLI(t, "-out", lib, "-date-format", "2006-01-02", zip); code != ExitOK {
		t.Fatalf("flat import: exit %d\n%s", code, out)
	}
	writeFile(t, filepath.Join(lib, "Road trip", "IMG_20210601_101010.jpg"), "trip")
	writeFile(t, filepath.Join(lib, "2023-05-11 renamed by hand", "x.jpg"), "renamed")
	writeFile(t, filepath.Join(lib, "Misc", "photo.jpg"), "undated")
	if code, out := runCLI(t, "-out", lib, "-sync"); code != ExitOK {
		t.Fatalf("sync: exit %d\n%s", code, out)
	}
	flat := mediaIn(t, lib)

	code, out := runCLI(t, "-out", lib, "-reorganize", "-dry-run")
	if code != ExitOK || !strings.Contains(out, "Would move 5 folder(s)") {
		t.Fatalf("dry run: exit %d\n%s", code, out)
	}
	if got := mediaIn(t, lib); strings.Join(got, "\n") != strings.Join(flat, "\n") {
		t.Fatalf("dry run moved files:\n%v", got)
	}

	code, out = runCLI(t, "-out", lib, "-reorganize")
	if code != ExitOK || !strings.Contains(out, "Moved 5 folder(s)") || !strings.Contains(out, "Misc (1 of 1 media files have no date)") {
		t.Fatalf("reorganize: exit %d\n%s", code, out)
	}
	want := []string{
		"2021/Road trip/IMG_20210601_101010.jpg",
		"2022/2022-07-08/IMG_0002-edited.jpg",
		"2022/2022-07-08/IMG_0002.jpg",
		"2023/2023-03-04/IMG_0001.jpg",
		"2023/2023-05-11 renamed by hand/x.jpg",
		"2023/2023-05-11/a_very_long_file_name_without_any_date_here.jpg",
		"Albums/Hawaii/beach.jpg",
		"Misc/photo.jpg", // undated, so left where it is
		"Unknown_Date/copy_of_existing.jpg",
		"Unknown_Date/mystery.png",
	}
	if got := mediaIn(t, lib); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("library contents:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	logs, _ := filepath.Glob(filepath.Join(lib, "photosorter_*.log"))
	var logged bool
	for _, l := range logs {
		data, _ := os.ReadFile(l)
		logged = logged || strings.Contains(string(data), "REORGANIZE moved: Road trip -> "+filepath.Join("2021", "Road trip"))
	}
	if !logged {
		t.Error("the move was not logged")
	}

	if code, out := runCLI(t, "-out", lib, "-reorganize"); code != ExitOK || !strings.Contains(out, "Moved 0 folder(s)") {
		t.Errorf("second reorganize: exit %d\n%s", code, out)
	}
	code, out = runCLI(t, "-out", lib, zip)
	if code != ExitOK || strings.Contains(out, "(added by hand?)") {
		t.Errorf("re-import: exit %d\n%s", code, out)
	}
	if got := mediaIn(t, lib); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("re-import after reorganizing added files:\n%s", strings.Join(got, "\n"))
	}
}
