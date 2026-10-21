package library

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"photosorter/internal/media"
	"photosorter/internal/media/mediatest"
)

func reorgOpts() ReorganizeOptions {
	return ReorganizeOptions{
		Filter:   media.DefaultFilter(),
		Skip:     []string{"Unknown_Date", "Albums"},
		Location: time.UTC,
		Workers:  4,
	}
}

// tree lists every file under root as "slash/path=content", ignoring the database.
func tree(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			t.Fatal(err)
		}
		if d.IsDir() || strings.HasPrefix(d.Name(), DBFileName) {
			return nil
		}
		b, _ := os.ReadFile(p)
		rel, _ := filepath.Rel(root, p)
		out = append(out, filepath.ToSlash(rel)+"="+string(b))
		return nil
	})
	sort.Strings(out)
	return out
}

// filenameOf returns the file name hash is recorded under on disk.
func filenameOf(t *testing.T, db *DB, hash string) string {
	t.Helper()
	var name string
	if err := db.sql.QueryRow("SELECT filename FROM processed_hashes WHERE hash = ?", hash).Scan(&name); err != nil {
		t.Fatalf("hash %s: %v", hash, err)
	}
	return name
}

// syncedDB syncs the library at lib into its database and reopens it, so every
// record is on disk.
func syncedDB(t *testing.T, lib string) *DB {
	t.Helper()
	db := openDB(t, lib)
	if _, err := Sync(context.Background(), scan(t, lib), db, syncOpts()); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	return openDB(t, lib)
}

func reorganize(t *testing.T, lib string, db *DB, opts ReorganizeOptions) ReorganizeResult {
	t.Helper()
	res, err := Reorganize(context.Background(), lib, db, opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Failed) > 0 {
		t.Fatalf("failed: %v", res.Failed)
	}
	return res
}

func TestReorganize_MovesDatedFolders(t *testing.T) {
	lib := t.TempDir()
	writeFiles(t, lib, map[string]string{
		"2025-10-02 foo bar/a.jpg":       "a",
		"2025-10-02 foo bar/notes.txt":   "kept with its folder",
		"2023-05-14/b.jpg":               "b",
		"1999 Christmas/sub/c.jpg":       "c",
		"Beach/IMG_20190704_120000.jpg":  "d",
		"Beach/sub/VID-2019-08-01.mp4":   "e",
		"Mixed/IMG_20190101.jpg":         "f",
		"Mixed/IMG_20200101.jpg":         "g",
		"Undated/holiday.jpg":            "h",
		"Undated/IMG_20190101.jpg":       "i",
		"Empty/readme.txt":               "no media",
		"Unknown_Date/x.jpg":             "j",
		"Albums/Hawaii/IMG_20180101.jpg": "k",
		"2024/2024-01-01/l.jpg":          "l",
		".photosorter-tmp/copy-1.tmp":    "temp",
		"20230514/m.jpg":                 "m", // no separator after the year: dated by contents
		"loose.jpg":                      "loose files at the root stay",
	})
	db := syncedDB(t, lib)
	before := count(t, db)

	res := reorganize(t, lib, db, reorgOpts())

	want := []string{
		".photosorter-tmp/copy-1.tmp=temp",
		"1999/1999 Christmas/sub/c.jpg=c",
		"2019/Beach/IMG_20190704_120000.jpg=d",
		"2019/Beach/sub/VID-2019-08-01.mp4=e",
		"2023/2023-05-14/b.jpg=b",
		"20230514/m.jpg=m",
		"2024/2024-01-01/l.jpg=l",
		"2025/2025-10-02 foo bar/a.jpg=a",
		"2025/2025-10-02 foo bar/notes.txt=kept with its folder",
		"Albums/Hawaii/IMG_20180101.jpg=k",
		"Empty/readme.txt=no media",
		"Mixed/IMG_20190101.jpg=f",
		"Mixed/IMG_20200101.jpg=g",
		"Undated/IMG_20190101.jpg=i",
		"Undated/holiday.jpg=h",
		"Unknown_Date/x.jpg=j",
		"loose.jpg=loose files at the root stay",
	}
	if got := tree(t, lib); !reflect.DeepEqual(got, want) {
		t.Errorf("library:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if len(res.Moved) != 4 {
		t.Errorf("moved %v, want 4 folders", res.Moved)
	}
	wantSkips := []Skip{
		{"20230514", "1 of 1 media files have no date"},
		{"Empty", "no media files to date it by"},
		{"Mixed", "media from several years: 2019, 2020"},
		{"Undated", "1 of 2 media files have no date"},
	}
	if !reflect.DeepEqual(res.Skipped, wantSkips) {
		t.Errorf("skipped %v, want %v", res.Skipped, wantSkips)
	}
	if got := count(t, db); got != before {
		t.Errorf("database has %d hashes, want %d unchanged", got, before)
	}
}

func TestReorganize_IsIdempotent(t *testing.T) {
	lib := t.TempDir()
	writeFiles(t, lib, map[string]string{
		"2025-10-02 foo/a.jpg":   "a",
		"Trip/IMG_20190101.jpg":  "b",
		"Mixed/IMG_20190101.jpg": "c",
		"Mixed/IMG_20200101.jpg": "d",
	})
	db := syncedDB(t, lib)
	reorganize(t, lib, db, reorgOpts())
	after := tree(t, lib)
	files, _, _ := db.LibraryFiles()

	res := reorganize(t, lib, db, reorgOpts())
	if len(res.Moved)+len(res.Duplicates)+len(res.Renamed) != 0 {
		t.Errorf("second run changed something: %+v", res)
	}
	if got := tree(t, lib); !reflect.DeepEqual(got, after) {
		t.Errorf("second run changed the library:\n%v\nwant:\n%v", got, after)
	}
	if again, _, _ := db.LibraryFiles(); again != files {
		t.Errorf("library file count %d, want %d", again, files)
	}
}

func TestReorganize_MergesIntoExistingFolder(t *testing.T) {
	lib := t.TempDir()
	writeFiles(t, lib, map[string]string{
		"2023-05-14/new.jpg":          "new",
		"2023-05-14/same.jpg":         "same",
		"2023-05-14/clash.jpg":        "old copy",
		"2023-05-14/sub/deep.jpg":     "deep",
		"2023-05-14/fresh/f.jpg":      "fresh",
		"2023-05-14/.DS_Store":        "finder a",
		"2023/2023-05-14/same.jpg":    "same",
		"2023/2023-05-14/clash.jpg":   "imported copy",
		"2023/2023-05-14/clash_1.jpg": "already taken",
		"2023/2023-05-14/sub/x.jpg":   "x",
		"2023/2023-05-14/.DS_Store":   "finder b",
	})
	db := syncedDB(t, lib)
	files, _, _ := db.LibraryFiles()

	res := reorganize(t, lib, db, reorgOpts())

	want := []string{
		"2023/2023-05-14/.DS_Store=finder b",
		"2023/2023-05-14/clash.jpg=imported copy",
		"2023/2023-05-14/clash_1.jpg=already taken",
		"2023/2023-05-14/clash_2.jpg=old copy",
		"2023/2023-05-14/fresh/f.jpg=fresh",
		"2023/2023-05-14/new.jpg=new",
		"2023/2023-05-14/same.jpg=same",
		"2023/2023-05-14/sub/deep.jpg=deep",
		"2023/2023-05-14/sub/x.jpg=x",
	}
	if got := tree(t, lib); !reflect.DeepEqual(got, want) {
		t.Errorf("library:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if _, err := os.Stat(filepath.Join(lib, "2023-05-14")); !os.IsNotExist(err) {
		t.Errorf("source folder still there: %v", err)
	}
	if len(res.Moved) != 1 || !res.Moved[0].Merged {
		t.Errorf("moved = %+v, want one merge", res.Moved)
	}
	if len(res.Duplicates) != 2 || len(res.Renamed) != 1 {
		t.Errorf("duplicates %v, renamed %v", res.Duplicates, res.Renamed)
	}
	if got := filenameOf(t, db, hashOf("old copy")); got != "clash_2.jpg" {
		t.Errorf("renamed file recorded as %q, want clash_2.jpg", got)
	}
	if ok, _ := has(db, hashOf("same")); !ok {
		t.Error("hash of the removed duplicate was forgotten; its twin is still in the library")
	}
	if got, _, _ := db.LibraryFiles(); got != files-1 {
		t.Errorf("library file count %d, want %d (one duplicate photo removed)", got, files-1)
	}
	if got, want := len(scan(t, lib).Paths), files-1; got != want {
		t.Errorf("library has %d media files, want %d", got, want)
	}
}

func TestReorganize_DryRunChangesNothing(t *testing.T) {
	lib := t.TempDir()
	writeFiles(t, lib, map[string]string{
		"2023-05-14/a.jpg":          "a",
		"2023-05-14/clash.jpg":      "one",
		"2023/2023-05-14/clash.jpg": "two",
		"2024-01-01/b.jpg":          "b",
	})
	syncedDB(t, lib).Close()
	before := tree(t, lib)
	db, err := OpenReadOnly(lib)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	opts := reorgOpts()
	opts.DryRun = true
	res := reorganize(t, lib, db, opts)

	if got := tree(t, lib); !reflect.DeepEqual(got, before) {
		t.Errorf("dry run changed the library:\n%v", got)
	}
	if len(res.Moved) != 2 || len(res.Renamed) != 1 || res.Renamed[0].To != filepath.FromSlash("2023/2023-05-14/clash_1.jpg") {
		t.Errorf("dry run reported %+v", res)
	}
	if _, err := os.Stat(filepath.Join(lib, "2024")); !os.IsNotExist(err) {
		t.Errorf("dry run created a year folder: %v", err)
	}
}

func TestReorganize_MissingLibrary(t *testing.T) {
	db := openDB(t, t.TempDir())
	res, err := Reorganize(context.Background(), filepath.Join(t.TempDir(), "nope"), db, reorgOpts())
	if err != nil || len(res.Moved)+len(res.Skipped) != 0 {
		t.Errorf("res = %+v, err = %v", res, err)
	}
}

func TestReorganize_StopsOnCancel(t *testing.T) {
	lib := t.TempDir()
	writeFiles(t, lib, map[string]string{"2023-05-14/a.jpg": "a"})
	db := openDB(t, lib)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Reorganize(ctx, lib, db, reorgOpts()); err != context.Canceled {
		t.Errorf("err = %v, want context.Canceled", err)
	}
	if _, err := os.Stat(filepath.Join(lib, "2023-05-14", "a.jpg")); err != nil {
		t.Errorf("moved despite cancellation: %v", err)
	}
}

func TestReorganize_DatesFoldersFromMediaMetadata(t *testing.T) {
	lib := t.TempDir()
	taken := time.Date(2018, 3, 1, 9, 0, 0, 0, time.UTC)
	writeFiles(t, lib, map[string]string{
		"Phone/IMG_0001.HEIC": string(mediatest.HEIC(taken, mediatest.HEICOptions{})),
		"Phone/clip.mov":      string(mediatest.MP4(taken, 5*time.Second, 16)),
		"Camera/MVI_0002.mp4": string(mediatest.MP4(taken, 5*time.Second, 16)),
	})
	db := syncedDB(t, lib)
	res := reorganize(t, lib, db, reorgOpts())
	if len(res.Moved) != 2 {
		t.Fatalf("moved %v, want Phone and Camera", res.Moved)
	}
	for _, p := range []string{"2018/Phone/IMG_0001.HEIC", "2018/Phone/clip.mov", "2018/Camera/MVI_0002.mp4"} {
		if _, err := os.Stat(filepath.Join(lib, filepath.FromSlash(p))); err != nil {
			t.Error(err)
		}
	}
}
