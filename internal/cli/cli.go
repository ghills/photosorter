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

	db, err := library.Open(r.cfg.Library)
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

	if code, ok := r.syncLibrary(ctx, db); !ok {
		return code
	}
	if len(sources) > 0 {
		if code := r.importAll(ctx, db, sources); code != ExitOK {
			return code
		}
	}
	r.printf("Done!\n")
	return ExitOK
}

// syncLibrary hashes the existing library when asked to, or when the database looks
// out of date. ok is false if the run should stop with code.
func (r *runner) syncLibrary(ctx context.Context, db *library.DB) (code int, ok bool) {
	tracked, err := db.Count()
	if err != nil {
		r.errorf("cannot read library database: %v", err)
		return ExitFailure, false
	}
	r.printf("Library database: %d files tracked.\n", tracked)

	needed := r.cfg.Sync
	if !needed && !r.cfg.NoSync {
		onDisk := library.CountMedia(r.cfg.Library, r.filter)
		switch {
		case onDisk > 0 && tracked == 0:
			r.printf("Found %d media files but an empty database; syncing the library first.\n", onDisk)
			needed = true
		case onDisk > tracked:
			fmt.Fprintf(r.stderr, "warning: the library has %d media files but the database tracks %d. Run with -sync if files were added by other means.\n", onDisk, tracked)
		}
	}
	if !needed {
		return ExitOK, true
	}

	r.printf("Syncing library %s...\n", r.cfg.Library)
	res, err := library.Sync(ctx, r.cfg.Library, db, library.SyncOptions{
		Workers:  r.cfg.Workers,
		Filter:   r.filter,
		Progress: r.stderr,
	})
	r.listPaths("could not read", res.Unreadable)
	r.listPaths("could not hash", res.Failed)
	switch {
	case errors.Is(err, context.Canceled):
		r.printf("Interrupted during sync.\n")
		return ExitInterrupted, false
	case err != nil:
		r.errorf("sync failed: %v", err)
		return ExitFailure, false
	}
	r.printf("Sync complete: %d files hashed.\n", res.Hashed)
	return ExitOK, true
}

// importAll runs the importer over every source and prints its report.
func (r *runner) importAll(ctx context.Context, db *library.DB, sources []source.Source) int {
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

// openLog creates this run's detail log in the library folder. If that fails, the run
// continues without one.
func (r *runner) openLog() (*log.Logger, func()) {
	path := filepath.Join(r.cfg.Library, fmt.Sprintf("photosorter_%s.log", time.Now().Format("20060102_150405")))
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
