// Package cli is PhotoSorter's command-line front end. It turns arguments and the
// config file into settings, then drives one run:
//
//	open inputs → open library database → reorganize if asked → sync library if needed
//	→ import → report
//
// Status messages go to stdout; warnings, errors and progress bars go to stderr. A
// detailed per-file log is written to the library folder.
//
// With no arguments, or with -ui, it starts the browser interface (package ui)
// instead. That interface runs imports through [Execute], so both front ends share
// one pipeline and print the same messages.
package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"photosorter/internal/config"
	"photosorter/internal/importer"
	"photosorter/internal/library"
	"photosorter/internal/media"
	"photosorter/internal/source"
	"photosorter/internal/ui"
)

// Exit codes.
const (
	ExitOK          = 0   // everything was processed
	ExitFailure     = 1   // a fatal error, or some files could not be imported
	ExitInterrupted = 130 // stopped by Ctrl-C or SIGTERM; safe to re-run
)

// maxListed caps how many unreadable or failed paths are printed to the terminal.
const maxListed = 10

// serveUI starts the browser interface. Tests replace it so no browser opens.
var serveUI = ui.Serve

// Run executes PhotoSorter with the given arguments (without the program name) and
// returns the process exit code. Cancelling ctx stops the run gracefully.
func Run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	p, err := parseArgs(args, stderr)
	if errors.Is(err, flag.ErrHelp) {
		return ExitOK
	}
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\nRun 'photosorter -h' for usage.\n", err)
		return ExitFailure
	}
	if p.ui {
		err := serveUI(ctx, ui.Options{
			Initial:    p.cfg,
			ConfigFile: p.configFile,
			Execute:    Execute,
			Stdout:     stdout,
			Stderr:     stderr,
		})
		if err != nil {
			fmt.Fprintf(stderr, "error: %v\n", err)
			return ExitFailure
		}
		return ExitOK
	}
	return Execute(ctx, p.cfg, stdout, stderr)
}

// Execute performs one run with settings that have already been resolved and
// validated, and returns its exit code. Cancelling ctx stops the run gracefully.
func Execute(ctx context.Context, cfg config.Config, stdout, stderr io.Writer) int {
	r := &runner{cfg: cfg, stdout: stdout, stderr: stderr}
	return r.run(ctx)
}

// runner holds the state of one run.
type runner struct {
	cfg            config.Config
	stdout, stderr io.Writer
	filter         media.Filter

	// log is this run's detail log, opened on first use by logger.
	log      *log.Logger
	closeLog func()
}

func (r *runner) printf(format string, args ...any) { fmt.Fprintf(r.stdout, format, args...) }

func (r *runner) errorf(format string, args ...any) {
	fmt.Fprintf(r.stderr, "error: "+format+"\n", args...)
}

func (r *runner) run(ctx context.Context) (code int) {
	r.filter, _ = r.cfg.Filter() // validated by parseArgs

	sources, err := source.Open(source.Inputs{
		Folders: r.cfg.Folders,
		Zips:    r.cfg.Zips,
		Library: r.cfg.Library,
		Filter:  r.filter,
	})
	if err != nil {
		r.errorf("%v", err)
		return ExitFailure
	}
	defer source.CloseAll(sources)

	// A dry run must not create the library or touch its database.
	openDB := library.Open
	if r.cfg.DryRun {
		openDB = library.OpenReadOnly
	}
	db, err := openDB(r.cfg.Library)
	if err != nil {
		r.errorf("cannot open library database: %v", err)
		return ExitFailure
	}
	// Closing flushes pending records, so its failure turns any result into a failure.
	defer func() {
		if err := db.Close(); err != nil {
			r.errorf("failed to save the library database: %v", err)
			fmt.Fprintln(r.stderr, "Files already written may be imported again next time; run with -sync to rebuild the database.")
			if code == ExitOK {
				code = ExitFailure
			}
		}
	}()

	defer func() {
		if r.closeLog != nil {
			r.closeLog()
		}
	}()

	if r.cfg.Reorganize {
		if code := r.reorganize(ctx, db); code != ExitOK {
			return code
		}
	}
	libraryFiles, code, ok := r.syncLibrary(ctx, db)
	if !ok {
		return code
	}
	if len(sources) > 0 {
		if code := r.importAll(ctx, db, sources, libraryFiles); code != ExitOK {
			return code
		}
	}
	r.printf("Done!\n")
	return ExitOK
}

// syncLibrary hashes the existing library when asked to, or when the database looks
// out of date. It returns how many media files the library is known to hold, for
// importAll to update. ok is false if the run should stop with code.
func (r *runner) syncLibrary(ctx context.Context, db *library.DB) (libraryFiles, code int, ok bool) {
	tracked, err := db.Count()
	if err != nil {
		r.errorf("cannot read library database: %v", err)
		return 0, ExitFailure, false
	}
	r.printf("Library database: %d files tracked.\n", tracked)
	// Databases from older versions don't store the file count; the hash count is the
	// closest estimate (it undercounts libraries holding duplicate content).
	libraryFiles, known, err := db.LibraryFiles()
	if err != nil {
		r.errorf("cannot read library database: %v", err)
		return 0, ExitFailure, false
	}
	if !known {
		libraryFiles = tracked
	}

	forced := r.cfg.Sync || r.cfg.Rehash || r.cfg.Prune
	if !forced && r.cfg.NoSync {
		return libraryFiles, ExitOK, true
	}
	files, err := library.Scan(ctx, r.cfg.Library, r.filter)
	if err != nil { // only cancellation stops a scan
		r.printf("Interrupted while scanning the library.\n")
		return 0, ExitInterrupted, false
	}
	needed := forced
	if onDisk := len(files.Paths); !needed {
		switch {
		case onDisk > 0 && tracked == 0:
			r.printf("Found %d media files but an empty database; syncing the library first.\n", onDisk)
			needed = true
		case onDisk > libraryFiles:
			r.printf("The library has %d media files but the database knows of %d (added by hand?); syncing the library first.\n", onDisk, libraryFiles)
			needed = true
		}
	}
	if !needed {
		return libraryFiles, ExitOK, true
	}

	r.printf("Syncing library %s...\n", r.cfg.Library)
	res, err := library.Sync(ctx, files, db, library.SyncOptions{
		Workers:  r.cfg.Workers,
		Progress: r.stderr,
		Prune:    r.cfg.Prune,
		Rehash:   r.cfg.Rehash,
	})
	r.listPaths("could not read", files.Unreadable)
	r.listPaths("could not hash", res.Failed)
	switch {
	case errors.Is(err, context.Canceled):
		r.printf("Interrupted during sync.\n")
		return 0, ExitInterrupted, false
	case err != nil:
		r.errorf("sync failed: %v", err)
		return 0, ExitFailure, false
	}
	r.printf("Sync complete: %d files hashed, %d unchanged since the last sync.\n", res.Hashed, res.Unchanged)
	if r.cfg.Prune {
		r.reportPrune(res)
	}
	return len(files.Paths), ExitOK, true
}

// reportPrune prints the outcome of a -prune sync.
func (r *runner) reportPrune(res library.SyncResult) {
	if res.PruneSkipped != "" {
		fmt.Fprintf(r.stderr, "warning: not pruning the database: %s.\n", res.PruneSkipped)
		return
	}
	verb := "Pruned"
	if r.cfg.DryRun {
		verb = "Would prune"
	}
	r.printf("%s %d cached hash(es) of files no longer in the library.\n", verb, len(res.Pruned))
	for i, name := range res.Pruned {
		if i == maxListed {
			r.printf("  ... and %d more\n", len(res.Pruned)-maxListed)
			break
		}
		r.printf("  %s\n", name)
	}
}

// reorganize moves the library's top-level dated folders into year folders and
// reports what it did. It returns ExitOK if the run should go on.
func (r *runner) reorganize(ctx context.Context, db *library.DB) int {
	logger := r.logger()
	loc, _ := r.cfg.Location() // validated by parseArgs
	// Only the first component of a nested folder setting sits at the top level.
	top := func(dir string) string { return strings.SplitN(dir, "/", 2)[0] }
	r.printf("Reorganizing %s into year folders...\n", r.cfg.Library)
	res, err := library.Reorganize(ctx, r.cfg.Library, db, library.ReorganizeOptions{
		Filter:   r.filter,
		Skip:     []string{top(r.cfg.Layout.UnknownDir), top(r.cfg.Layout.AlbumsDir)},
		Location: loc,
		Workers:  r.cfg.Workers,
		DryRun:   r.cfg.DryRun,
		Progress: r.stderr,
	})

	would := func(done, planned string) string {
		if r.cfg.DryRun {
			return planned
		}
		return done
	}
	for _, m := range res.Moved {
		how := "moved"
		if m.Merged {
			how = "merged"
		}
		logger.Printf("REORGANIZE %s: %s -> %s", how, m.From, m.To)
	}
	for _, m := range res.Renamed {
		logger.Printf("REORGANIZE renamed (name taken): %s -> %s", m.From, m.To)
	}
	for _, p := range res.Duplicates {
		logger.Printf("REORGANIZE removed duplicate: %s", p)
	}
	skips := make([]string, len(res.Skipped))
	for i, s := range res.Skipped {
		skips[i] = fmt.Sprintf("%s (%s)", s.Dir, s.Reason)
		logger.Printf("REORGANIZE skipped: %s", skips[i])
	}
	for _, f := range res.Failed {
		logger.Printf("REORGANIZE failed: %s", f)
	}

	switch {
	case errors.Is(err, context.Canceled):
		r.printf("Interrupted while reorganizing. Run again to continue.\n")
		return ExitInterrupted
	case err != nil:
		r.errorf("reorganize failed: %v", err)
		return ExitFailure
	}

	merged := 0
	for i, m := range res.Moved {
		if m.Merged {
			merged++
		}
		if i < maxListed {
			r.printf("  %s -> %s\n", m.From, m.To)
		} else if i == maxListed {
			r.printf("  ... and %d more\n", len(res.Moved)-maxListed)
		}
	}
	r.printf("%s %d folder(s) into year folders", would("Moved", "Would move"), len(res.Moved))
	if merged > 0 {
		r.printf(" (%d merged into existing folders)", merged)
	}
	r.printf(".\n")
	if n := len(res.Duplicates); n > 0 {
		r.printf("%s %d file(s) already in the target folder.\n", would("Removed", "Would remove"), n)
	}
	if n := len(res.Renamed); n > 0 {
		r.printf("%s %d file(s) whose name was taken in the target folder.\n", would("Renamed", "Would rename"), n)
	}
	r.listPaths("left in place", skips)
	if len(res.Failed) > 0 {
		r.listPaths("could not move", res.Failed)
		return ExitFailure
	}
	return ExitOK
}

// importAll runs the importer over every source and prints its report. libraryFiles
// is the library's media file count before the import; the files imported are added
// to it in the database.
func (r *runner) importAll(ctx context.Context, db *library.DB, sources []source.Source, libraryFiles int) int {
	logger := r.logger()
	loc, _ := r.cfg.Location()                  // validated by parseArgs
	forced, fallback, _ := r.cfg.Manual.Dates() // validated by parseArgs
	if summary := r.manualSummary(); summary != "" {
		r.printf("%s\n", summary)
		logger.Print(summary)
	}
	im := importer.New(db, importer.Options{
		Library: r.cfg.Library,
		Layout: importer.Layout{
			DateFormat: r.cfg.Layout.DateFormat,
			UnknownDir: r.cfg.Layout.UnknownDir,
			AlbumsDir:  r.cfg.Layout.AlbumsDir,
			KeepAlbums: r.cfg.Layout.Albums == config.AlbumsFolder,
			Album:      r.cfg.Manual.Album,
			Event:      r.cfg.Manual.Event,
		},
		ForcedDate:     forced,
		FallbackDate:   fallback,
		Workers:        r.cfg.Workers,
		BufferLimit:    r.cfg.BufferLimit(),
		SkipVideosUpTo: r.cfg.ShortVideoLimit(),
		NoXMP:          r.cfg.NoXMP,
		DryRun:         r.cfg.DryRun,
		Location:       loc,
		Log:            logger,
		Progress:       r.stderr,
	})

	report, err := im.Run(ctx, sources)
	report.Print(r.stdout)
	if n := report.TotalImported(); n > 0 && db.Err() == nil {
		if err := db.SetLibraryFiles(libraryFiles + n); err != nil {
			fmt.Fprintf(r.stderr, "warning: cannot update the library file count: %v\n", err)
		}
	}

	var failed *importer.FilesFailedError
	switch {
	case err == nil:
		return ExitOK
	case errors.Is(err, context.Canceled):
		r.printf("Interrupted. Finished files were recorded; run again to continue.\n")
		return ExitInterrupted
	case errors.As(err, &failed):
		r.printf("Finished, but %d file(s) could not be imported. See the log for details.\n", failed.Count)
		return ExitFailure
	default:
		r.errorf("%v", err)
		return ExitFailure
	}
}

// manualSummary describes the manual-import settings in effect, or returns "" if
// there are none, so the user can see what a run will do before it starts.
func (r *runner) manualSummary() string {
	m := r.cfg.Manual
	var parts []string
	switch {
	case m.Date != "":
		parts = append(parts, "every file dated "+m.Date)
	case m.FallbackDate != "":
		parts = append(parts, "files without a date of their own dated "+m.FallbackDate)
	}
	if m.Event != "" {
		parts = append(parts, fmt.Sprintf("event %q", m.Event))
	}
	if m.Album != "" {
		parts = append(parts, fmt.Sprintf("everything in album %q", m.Album))
	}
	if len(parts) == 0 {
		return ""
	}
	return "Manual import: " + strings.Join(parts, ", ") + "."
}

// logger returns this run's detail log, opening it on first use.
func (r *runner) logger() *log.Logger {
	if r.log == nil {
		r.log, r.closeLog = r.openLog()
	}
	return r.log
}

// openLog creates this run's detail log in the library folder, or for a dry run in the
// system temp folder. If that fails, the run continues without one.
func (r *runner) openLog() (*log.Logger, func()) {
	dir := r.cfg.Library
	if r.cfg.DryRun {
		dir = os.TempDir()
	}
	path := filepath.Join(dir, fmt.Sprintf("photosorter_%s.log", time.Now().Format("20060102_150405")))
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		fmt.Fprintf(r.stderr, "warning: cannot create log file, details will not be saved: %v\n", err)
		return log.New(io.Discard, "", 0), func() {}
	}
	r.printf("Writing detailed log to %s\n", path)
	return log.New(f, "", log.LstdFlags), func() { f.Close() }
}

// listPaths prints up to maxListed problem paths to stderr. Files in them are not
// tracked, so they may be imported again later.
func (r *runner) listPaths(what string, paths []string) {
	if len(paths) == 0 {
		return
	}
	fmt.Fprintf(r.stderr, "warning: %s %d path(s):\n", what, len(paths))
	for i, p := range paths {
		if i == maxListed {
			fmt.Fprintf(r.stderr, "  ... and %d more\n", len(paths)-maxListed)
			break
		}
		fmt.Fprintf(r.stderr, "  %s\n", p)
	}
}
