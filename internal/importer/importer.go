package importer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"sync"
	"time"

	"photosorter/internal/library"
	"photosorter/internal/parallel"
	"photosorter/internal/source"
)

// DefaultBufferLimit covers typical photos and short clips.
const DefaultBufferLimit = 16 << 20

// Options configures an [Importer]. The zero value of each field is usable except
// Library, which is required.
type Options struct {
	// Library is the folder to import into.
	Library string

	// Layout decides which folder each file goes to. The zero value means
	// DefaultLayout().
	Layout Layout

	// Workers is the number of files processed concurrently (minimum 1).
	Workers int

	// BufferLimit is the largest file, in bytes, read fully into memory so hashing,
	// date detection and the copy share a single read. Larger files are read once per
	// step. Zero disables buffering. Peak memory is roughly Workers × BufferLimit.
	BufferLimit int64

	// SkipVideosUpTo skips MP4/MOV videos no longer than this. Zero imports all videos.
	SkipVideosUpTo time.Duration

	// DryRun goes through every step except writing files and database records.
	DryRun bool

	// Location is the time zone used to turn sidecar timestamps into dates. Nil means
	// time.Local. EXIF dates are wall-clock times and are used as recorded.
	Location *time.Location

	// Log receives one line per file. Nil discards it.
	Log *log.Logger

	// Progress receives a progress bar per source. Nil disables it.
	Progress io.Writer
}

// Importer imports media into a library. Create one with [New].
type Importer struct {
	db   *library.DB
	opts Options

	// bufPool recycles read buffers so buffering a file that turns out to be a
	// duplicate doesn't cost an allocation.
	bufPool sync.Pool
}

// New returns an Importer that records imported content in db.
func New(db *library.DB, opts Options) *Importer {
	if opts.Log == nil {
		opts.Log = log.New(io.Discard, "", 0)
	}
	if opts.Location == nil {
		opts.Location = time.Local
	}
	if opts.Layout == (Layout{}) {
		opts.Layout = DefaultLayout()
	}
	return &Importer{db: db, opts: opts}
}

// FilesFailedError is returned by [Importer.Run] when the run finished but some files
// could not be imported. Details are in the log.
type FilesFailedError struct {
	Count int
}

func (e *FilesFailedError) Error() string {
	return fmt.Sprintf("%d file(s) could not be imported", e.Count)
}

// Run imports every file of every source and returns a summary.
//
// It stops early, after in-flight files finish or abort, when ctx is cancelled or the
// database stops accepting writes; the error then says why. Otherwise it returns a
// [*FilesFailedError] if any file failed, or nil. The report is valid in every case.
func (im *Importer) Run(ctx context.Context, sources []source.Source) (Report, error) {
	t := newTally(im.opts.DryRun)
	for _, src := range sources {
		if ctx.Err() != nil || im.db.Err() != nil {
			break
		}
		im.importSource(ctx, src, t)
	}
	report := t.report()

	if err := im.db.Err(); err != nil {
		return report, fmt.Errorf("library database write failed: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return report, err
	}
	if report.Failed > 0 {
		return report, &FilesFailedError{Count: report.Failed}
	}
	return report, nil
}

// importSource runs every file of one source through the worker pool.
func (im *Importer) importSource(ctx context.Context, src source.Source, t *tally) {
	im.opts.Log.Printf("Importing from %s", src.Name())
	for _, w := range src.Warnings() {
		t.add(func(r *Report) { r.Unreadable++ })
		im.opts.Log.Printf("Skipped unreadable path: %s", w)
	}

	parallel.ForEach(ctx, src.Files(), parallel.Options{
		Workers:  im.opts.Workers,
		Stop:     func() bool { return im.db.Err() != nil },
		Progress: im.opts.Progress,
		Label:    "Importing " + src.Name(),
	}, func(f source.File) {
		im.handle(ctx, f, t)
	})
}

// handle imports one file and records its outcome in the log and the tally.
func (im *Importer) handle(ctx context.Context, f source.File, t *tally) {
	res, err := im.safeImportFile(ctx, f)
	log := im.opts.Log
	switch {
	case errors.Is(err, context.Canceled):
		t.add(func(r *Report) { r.Interrupted++ })
	case err != nil:
		t.add(func(r *Report) { r.Failed++ })
		log.Printf("Failed: %s: %v", f.Name(), err)
	case res.outcome == duplicate:
		t.add(func(r *Report) { r.Duplicates++ })
		log.Printf("Duplicate: %s", f.Name())
	case res.outcome == shortVideo:
		t.add(func(r *Report) { r.ShortVideos++ })
		log.Printf("Short video skipped: %s", f.Name())
	default:
		t.add(func(r *Report) { r.Imported[res.date]++ })
		log.Printf("Imported: %s -> %s (date from %s)", f.Name(), res.dest, res.date)
	}
}

// safeImportFile converts a panic while handling one file into an error so a single
// bad file can't take down the whole run.
func (im *Importer) safeImportFile(ctx context.Context, f source.File) (res result, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic: %v", r)
		}
	}()
	return im.importFile(ctx, f)
}
