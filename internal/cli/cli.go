// Package cli is PhotoSorter's command-line front end. It turns arguments and the
// config file into settings, then drives one run:
//
//	open inputs → open library database → sync library if needed → import → report
//
// Status messages go to stdout; warnings, errors and progress bars go to stderr. A
// detailed per-file log is written to the library folder.
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
	"time"

	"photosorter/internal/config"
	"photosorter/internal/importer"
	"photosorter/internal/library"
	"photosorter/internal/media"
	"photosorter/internal/source"
)

// Exit codes.
const (
	ExitOK          = 0   // everything was processed
	ExitFailure     = 1   // a fatal error, or some files could not be imported
	ExitInterrupted = 130 // stopped by Ctrl-C or SIGTERM; safe to re-run
)

// maxListed caps how many unreadable or failed paths are printed to the terminal.
const maxListed = 10

// Run executes PhotoSorter with the given arguments (without the program name) and
// returns the process exit code. Cancelling ctx stops the run gracefully.
func Run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	cfg, err := parseArgs(args, stderr)
	if errors.Is(err, flag.ErrHelp) {
		return ExitOK
	}
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\nRun 'photosorter -h' for usage.\n", err)
		return ExitFailure
	}
	r := &runner{cfg: cfg, stdout: stdout, stderr: stderr}
	return r.run(ctx)
}

// runner holds the state of one run.
type runner struct {
	cfg            config.Config
	stdout, stderr io.Writer
	filter         media.Filter
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

	forced := r.cfg.Sync || r.cfg.Prune
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
			fmt.Fprintf(r.stderr, "warning: the library has %d media files but the database knows of %d. Run with -sync if files were added by other means.\n", onDisk, libraryFiles)
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
	r.printf("Sync complete: %d files hashed.\n", res.Hashed)
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

// importAll runs the importer over every source and prints its report. libraryFiles
// is the library's media file count before the import; the files imported are added
// to it in the database.
func (r *runner) importAll(ctx context.Context, db *library.DB, sources []source.Source, libraryFiles int) int {
	logger, closeLog := r.openLog()
	defer closeLog()

	loc, _ := r.cfg.Location() // validated by parseArgs
	im := importer.New(db, importer.Options{
		Library: r.cfg.Library,
		Layout: importer.Layout{
			DateFormat: r.cfg.Layout.DateFormat,
			UnknownDir: r.cfg.Layout.UnknownDir,
			AlbumsDir:  r.cfg.Layout.AlbumsDir,
			KeepAlbums: r.cfg.Layout.Albums == config.AlbumsFolder,
		},
		Workers:        r.cfg.Workers,
		BufferLimit:    r.cfg.BufferLimit(),
		SkipVideosUpTo: r.cfg.ShortVideoLimit(),
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
