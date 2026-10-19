package library

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"photosorter/internal/media"
	"photosorter/internal/parallel"
)

// ReorganizeOptions configures [Reorganize].
type ReorganizeOptions struct {
	// Filter says which files are media, for dating folders by their contents.
	Filter media.Filter

	// Skip names top-level folders to leave alone, such as the unknown-date and album
	// folders. Hidden folders and year folders are always left alone.
	Skip []string

	// Location is the zone dates in file names are read in.
	Location *time.Location

	// Workers is the number of files dated concurrently.
	Workers int

	// DryRun works out and reports every change without making any.
	DryRun bool

	// Progress receives a progress bar while folders are dated; nil for none.
	Progress io.Writer
}

// Move is a path that was (or in a dry run, would be) moved. Paths are relative to
// the library root.
type Move struct {
	From, To string
	// Merged is set when a folder's contents were moved into an existing folder.
	Merged bool
}

// Skip is a folder [Reorganize] left in place, and why.
type Skip struct {
	Dir, Reason string
}

// ReorganizeResult describes what [Reorganize] did. Paths are relative to the
// library root.
type ReorganizeResult struct {
	// Moved lists the folders moved into year folders.
	Moved []Move
	// Skipped lists the folders whose year could not be decided.
	Skipped []Skip
	// Duplicates lists files that were removed while merging because a file with the
	// same name and content was already in the target folder.
	Duplicates []string
	// Renamed lists files given a "_N" name while merging because their name was taken
	// in the target folder by different content.
	Renamed []Move
	// Failed lists what could not be moved ("path: error").
	Failed []string
}

// yearDir matches a year folder, which Reorganize leaves alone.
var yearDir = regexp.MustCompile(`^\d{4}$`)

// yearPrefix matches folder names that start with a year, such as "2023-05-14",
// "2023-05-14 Beach" or "2023 Christmas".
var yearPrefix = regexp.MustCompile(`^((?:19|20)\d{2})(?:[-_ .]|$)`)

// Reorganize moves the dated folders at the top of the library at root into year
// folders: "2023-05-14 Beach/" becomes "2023/2023-05-14 Beach/".
//
// A folder's year comes from the start of its name. Failing that, it comes from the
// media inside (EXIF date, then the date in the file name), but only if every media
// file has a date and they all fall in the same year; otherwise the folder is left
// where it is and reported in [ReorganizeResult.Skipped].
//
// A folder whose target does not exist yet is renamed in one step. If the target
// exists, the contents are merged into it file by file without overwriting anything:
// a file whose name and content are both already there is removed, and one whose name
// is taken by other content gets a "_N" suffix (its database record is renamed to
// match). Running Reorganize again does nothing, since year folders are left alone,
// and it finishes a merge that was interrupted.
//
// The database records content hashes and file names, not folders, so it only
// changes for renamed files and for the library's file count when duplicates are
// removed. Problems with individual paths are reported in the result; Reorganize
// returns early with ctx's error if ctx is cancelled.
func Reorganize(ctx context.Context, root string, db *DB, opts ReorganizeOptions) (ReorganizeResult, error) {
	var res ReorganizeResult
	entries, err := os.ReadDir(root)
	if errors.Is(err, fs.ErrNotExist) {
		return res, nil
	}
	if err != nil {
		return res, err
	}

	// Work out each candidate folder's year, dating contents only where the name has none.
	type candidate struct {
		name  string
		year  string
		files []string // media to date, when the name has no year
	}
	var candidates []*candidate
	var toDate []string
	for _, e := range entries {
		name := e.Name()
		if !e.IsDir() || strings.HasPrefix(name, ".") || yearDir.MatchString(name) || skipped(name, opts.Skip) {
			continue
		}
		c := &candidate{name: name}
		if m := yearPrefix.FindStringSubmatch(name); m != nil {
			c.year = m[1]
		} else {
			files, unreadable := mediaUnder(filepath.Join(root, name), opts.Filter)
			if unreadable {
				res.Skipped = append(res.Skipped, Skip{name, "some paths could not be read"})
				continue
			}
			if len(files) == 0 {
				res.Skipped = append(res.Skipped, Skip{name, "no media files to date it by"})
				continue
			}
			c.files = files
			toDate = append(toDate, files...)
		}
		candidates = append(candidates, c)
	}

	years := make(map[string]int, len(toDate)) // path → year, 0 if undated
	var mu sync.Mutex
	parallel.ForEach(ctx, toDate, parallel.Options{
		Workers:  opts.Workers,
		Progress: opts.Progress,
		Label:    "Dating folders",
	}, func(p string) {
		y := fileYear(p, opts.Location)
		mu.Lock()
		years[p] = y
		mu.Unlock()
	})
	if err := ctx.Err(); err != nil {
		return res, err
	}

	r := reorganizer{root: root, db: db, dryRun: opts.DryRun, filter: opts.Filter, res: &res}
	for _, c := range candidates {
		if err := ctx.Err(); err != nil {
			return res, err
		}
		if c.year == "" {
			year, reason := commonYear(c.files, years)
			if reason != "" {
				res.Skipped = append(res.Skipped, Skip{c.name, reason})
				continue
			}
			c.year = year
		}
		r.moveFolder(c.name, filepath.Join(c.year, c.name))
	}
	sort.Slice(res.Skipped, func(i, j int) bool { return res.Skipped[i].Dir < res.Skipped[j].Dir })

	if err := r.dbErr; err != nil {
		return res, err
	}
	if r.mediaRemoved > 0 {
		if n, ok, _ := db.LibraryFiles(); ok {
			return res, db.SetLibraryFiles(max(n-r.mediaRemoved, 0))
		}
	}
	return res, nil
}

// skipped reports whether name is one of skip. Case is ignored, as many filesystems
// (macOS, Windows) ignore it.
func skipped(name string, skip []string) bool {
	for _, s := range skip {
		if strings.EqualFold(name, s) {
			return true
		}
	}
	return false
}

// mediaUnder lists the media files under dir. unreadable is set if any path could
// not be read, since the folder's year can then not be known.
func mediaUnder(dir string, filter media.Filter) (files []string, unreadable bool) {
	filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		switch {
		case err != nil:
			unreadable = true
		case !d.IsDir() && filter.MatchIn(dir, p):
			files = append(files, p)
		}
		return nil
	})
	return files, unreadable
}

// fileYear returns the year the media file at path was taken, from its EXIF header or
// its file name, or 0 if neither has a date. Library files have no Takeout sidecars.
func fileYear(path string, loc *time.Location) int {
	if media.HasEXIF(path) {
		if f, err := os.Open(path); err == nil {
			t, ok := media.EXIFDate(f)
			f.Close()
			if ok {
				return t.Year()
			}
		}
	}
	if t, ok := media.DateFromFilename(path, loc); ok {
		return t.Year()
	}
	return 0
}

// commonYear returns the year shared by every file, or why there is none.
func commonYear(files []string, years map[string]int) (year, reason string) {
	undated := 0
	seen := map[int]bool{}
	for _, p := range files {
		if y := years[p]; y == 0 {
			undated++
		} else {
			seen[y] = true
		}
	}
	if undated > 0 {
		return "", fmt.Sprintf("%d of %d media files have no date", undated, len(files))
	}
	if len(seen) > 1 {
		list := make([]int, 0, len(seen))
		for y := range seen {
			list = append(list, y)
		}
		sort.Ints(list)
		parts := make([]string, len(list))
		for i, y := range list {
			parts[i] = strconv.Itoa(y)
		}
		return "", "media from several years: " + strings.Join(parts, ", ")
	}
	for y := range seen {
		return strconv.Itoa(y), ""
	}
	return "", "no media files to date it by" // unreachable: files is never empty
}

// reorganizer carries out the moves of one Reorganize call.
type reorganizer struct {
	root   string
	db     *DB
	dryRun bool
	filter media.Filter
	res    *ReorganizeResult

	// mediaRemoved counts duplicate media files removed, for the library file count.
	mediaRemoved int
	// dbErr is the first database failure; the run stops reporting it.
	dbErr error
}

func (r *reorganizer) abs(rel string) string { return filepath.Join(r.root, rel) }

func (r *reorganizer) fail(rel string, err error) {
	r.res.Failed = append(r.res.Failed, fmt.Sprintf("%s: %v", rel, err))
}

// moveFolder moves the folder from to to (both relative to the root), merging into to
// if it already exists.
func (r *reorganizer) moveFolder(from, to string) {
	info, err := os.Lstat(r.abs(to))
	switch {
	case errors.Is(err, fs.ErrNotExist):
		if !r.dryRun {
			if err := os.MkdirAll(filepath.Dir(r.abs(to)), 0755); err != nil {
				r.fail(from, err)
				return
			}
			if err := os.Rename(r.abs(from), r.abs(to)); err != nil {
				r.fail(from, err)
				return
			}
		}
		r.res.Moved = append(r.res.Moved, Move{From: from, To: to})
	case err != nil:
		r.fail(from, err)
	case !info.IsDir():
		r.fail(from, fmt.Errorf("%s exists and is not a folder", to))
	default:
		failed := len(r.res.Failed)
		r.mergeDir(from, to)
		if len(r.res.Failed) == failed {
			r.res.Moved = append(r.res.Moved, Move{From: from, To: to, Merged: true})
		}
	}
}

// mergeDir moves the contents of the folder from into the existing folder to, then
// removes from if that left it empty.
func (r *reorganizer) mergeDir(from, to string) {
	entries, err := os.ReadDir(r.abs(from))
	if err != nil {
		r.fail(from, err)
		return
	}
	for _, e := range entries {
		src, dst := filepath.Join(from, e.Name()), filepath.Join(to, e.Name())
		info, err := os.Lstat(r.abs(dst))
		switch {
		case err != nil && !errors.Is(err, fs.ErrNotExist):
			r.fail(src, err)
		case e.IsDir() && err != nil: // nothing there yet: move the whole folder
			if !r.dryRun {
				if err := os.Rename(r.abs(src), r.abs(dst)); err != nil {
					r.fail(src, err)
				}
			}
		case e.IsDir() && info.IsDir():
			r.mergeDir(src, dst)
		case e.IsDir() || (err == nil && info.IsDir()):
			r.fail(src, fmt.Errorf("%s exists as a different kind of entry", dst))
		case err != nil: // a file, and its name is free
			r.moveFile(src, dst)
		default:
			r.mergeFile(src, dst)
		}
		if r.dbErr != nil {
			return
		}
	}
	if !r.dryRun {
		// Only succeeds once everything has moved out; anything left was reported.
		os.Remove(r.abs(from))
	}
}

// moveFile moves the file from to the free name to, or a "_N" variant if that was
// taken meanwhile, and returns the name used.
func (r *reorganizer) moveFile(from, to string) string {
	if r.dryRun {
		return to
	}
	placed, err := PlaceFile(r.abs(from), r.abs(to))
	if err != nil {
		r.fail(from, err)
		return ""
	}
	// PlaceFile made a second link, or (without hard links) renamed the file away.
	if err := os.Remove(r.abs(from)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		r.fail(from, err)
	}
	rel, _ := filepath.Rel(r.root, placed)
	return rel
}

// mergeFile handles the file from whose name is already taken at to: an identical
// copy is removed, different content is moved under a "_N" name.
func (r *reorganizer) mergeFile(from, to string) {
	same, hash, err := sameContent(r.abs(from), r.abs(to))
	if err != nil {
		r.fail(from, err)
		return
	}
	// .DS_Store files are Finder's per-folder settings, not library content.
	if same || filepath.Base(from) == ".DS_Store" {
		if !r.dryRun {
			if err := os.Remove(r.abs(from)); err != nil {
				r.fail(from, err)
				return
			}
		}
		r.res.Duplicates = append(r.res.Duplicates, from)
		if r.filter.MatchIn(r.root, r.abs(from)) {
			r.mediaRemoved++
		}
		return
	}

	placed := r.moveFile(from, freeName(r.abs(to), r.root))
	if placed == "" {
		return
	}
	r.res.Renamed = append(r.res.Renamed, Move{From: from, To: placed})
	if r.filter.MatchIn(r.root, r.abs(placed)) {
		if err := r.db.SetFilename(hash, filepath.Base(placed)); err != nil {
			r.dbErr = err
		}
	}
}

// freeName returns path, relative to root, with the first "_N" suffix not in use.
func freeName(path, root string) string {
	ext := filepath.Ext(path)
	stem := strings.TrimSuffix(path, ext)
	for n := 1; ; n++ {
		candidate := fmt.Sprintf("%s_%d%s", stem, n, ext)
		if _, err := os.Lstat(candidate); errors.Is(err, fs.ErrNotExist) {
			rel, _ := filepath.Rel(root, candidate)
			return rel
		}
	}
}

// sameContent reports whether the files a and b hold the same bytes, and returns a's
// content hash.
func sameContent(a, b string) (same bool, hashA string, err error) {
	hashA, err = hashFile(a)
	if err != nil {
		return false, "", err
	}
	ia, errA := os.Stat(a)
	ib, errB := os.Stat(b)
	if errA == nil && errB == nil {
		if os.SameFile(ia, ib) {
			return true, hashA, nil
		}
		if ia.Size() != ib.Size() {
			return false, hashA, nil
		}
	}
	hashB, err := hashFile(b)
	if err != nil {
		return false, "", err
	}
	return hashA == hashB, hashA, nil
}

func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	return Hash(f)
}
