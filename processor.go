package main

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/cespare/xxhash/v2"
	"github.com/schollz/progressbar/v3"
)

type Processor struct {
	Sources         []InputSource
	OutputDir       string
	DB              *HashDB
	Workers         int
	SkipShortVideos int
	DryRun          bool
	Logger          *log.Logger

	// BufferLimit is the largest file (in bytes) read fully into memory so hashing, date
	// detection and the copy share a single read. Larger files are streamed, one read each
	// per step. Zero disables buffering. Peak memory is roughly Workers × BufferLimit.
	BufferLimit int64

	// bufPool recycles read buffers so buffering a file that turns out to be a duplicate
	// doesn't cost an allocation.
	bufPool sync.Pool
}

// DefaultBufferLimit covers typical photos and short clips.
const DefaultBufferLimit = 16 << 20

// NewProcessor builds a Processor. A nil logger discards detailed log output.
func NewProcessor(sources []InputSource, outputDir string, db *HashDB, workers int, skipShortVideos int, dryRun bool, logger *log.Logger) *Processor {
	if logger == nil {
		logger = log.New(io.Discard, "", 0)
	}
	return &Processor{
		Sources:         sources,
		OutputDir:       outputDir,
		DB:              db,
		Workers:         workers,
		SkipShortVideos: skipShortVideos,
		DryRun:          dryRun,
		Logger:          logger,
		BufferLimit:     DefaultBufferLimit,
	}
}

// Run processes every source. It stops early, after finishing or aborting in-flight
// files, when ctx is cancelled or the database stops accepting writes. In both cases
// the final report is still printed and the cause is returned.
// FilesFailedError is returned by Run when the pipeline finished but some files could not be processed.
type FilesFailedError struct {
	Count int
}

func (e *FilesFailedError) Error() string {
	return fmt.Sprintf("%d file(s) failed to process", e.Count)
}

func (p *Processor) Run(ctx context.Context) error {
	var stats runStats

	for _, src := range p.Sources {
		if ctx.Err() != nil || p.DB.Err() != nil {
			break
		}
		p.processSource(ctx, src, &stats)
	}

	p.printReport(&stats)

	if err := p.DB.Err(); err != nil {
		return fmt.Errorf("database write failed: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if n := stats.errors.Load(); n > 0 {
		return &FilesFailedError{Count: int(n)}
	}
	return nil
}

// processSource runs every media file of one source through the worker pool.
func (p *Processor) processSource(ctx context.Context, src InputSource, stats *runStats) {
	p.Logger.Printf("Scanning source: %s", src.Name())

	mediaFiles, err := src.MediaFiles()
	if w, ok := src.(interface{ Warnings() []string }); ok {
		for _, msg := range w.Warnings() {
			stats.unreadable.Add(1)
			p.Logger.Printf("Skipped unreadable path: %s", msg)
		}
	}
	if err != nil {
		p.Logger.Printf("Failed to scan source %s: %v", src.Name(), err)
		fmt.Printf("Failed to scan source %s: %v\n", src.Name(), err)
		return
	}
	if len(mediaFiles) == 0 {
		return
	}

	bar := progressbar.Default(int64(len(mediaFiles)), fmt.Sprintf("Processing %s", src.Name()))

	taskCh := make(chan MediaFile, len(mediaFiles))
	for _, mF := range mediaFiles {
		taskCh <- mF
	}
	close(taskCh)

	var wg sync.WaitGroup
	for i := 0; i < p.Workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for mF := range taskCh {
				if ctx.Err() != nil || p.DB.Err() != nil {
					return
				}
				p.handle(ctx, mF, stats)
				bar.Add(1)
			}
		}()
	}
	wg.Wait()
}

// handle processes one file and records its outcome in the log and the run statistics.
func (p *Processor) handle(ctx context.Context, f MediaFile, stats *runStats) {
	res, err := p.safeProcessFile(ctx, f)
	switch {
	case errors.Is(err, context.Canceled):
		stats.interrupted.Add(1)
	case err != nil:
		p.Logger.Printf("Error processing %s: %v", f.Name(), err)
		stats.errors.Add(1)
	case res.Outcome == SkippedDuplicate:
		stats.duplicates.Add(1)
		p.Logger.Printf("Skipped duplicate: %s", f.Name())
	case res.Outcome == SkippedShortVideo:
		stats.shortVideos.Add(1)
		p.Logger.Printf("Skipped short video: %s", f.Name())
	default:
		stats.countDate(res.Date)
		p.Logger.Printf("Successfully organized: %s (via %s)", f.Name(), res.Date)
	}
}

func (p *Processor) printReport(s *runStats) {
	fmt.Printf("\n\n--- Final Report ---\n")
	if p.DryRun {
		fmt.Printf("[DRY RUN MODE - No files were written]\n")
	}
	fmt.Printf("Successfully Organized   : %d\n", s.organized())
	fmt.Printf("  └ via EXIF             : %d\n", s.exif.Load())
	fmt.Printf("  └ via JSON (Taken)     : %d\n", s.jsonTaken.Load())
	fmt.Printf("  └ via JSON (Creation)  : %d\n", s.jsonCreation.Load())
	fmt.Printf("  └ via Filename Regex   : %d\n", s.filename.Load())
	fmt.Printf("  └ Sent to Unknown_Date : %d\n", s.unknown.Load())
	fmt.Printf("Duplicates Skipped       : %d\n", s.duplicates.Load())
	fmt.Printf("Short Videos Skipped     : %d\n", s.shortVideos.Load())
	fmt.Printf("Errors                   : %d\n", s.errors.Load())
	if n := s.unreadable.Load(); n > 0 {
		fmt.Printf("Unreadable Paths Skipped : %d (see log)\n", n)
	}
	if n := s.interrupted.Load(); n > 0 {
		fmt.Printf("Aborted mid-copy         : %d (safe to re-run)\n", n)
	}
}

// Outcome says what happened to a file that was processed without error.
type Outcome int

const (
	Organized Outcome = iota
	SkippedDuplicate
	SkippedShortVideo
)

// Result is what processFile reports for one file. Date is only meaningful when Outcome is Organized.
type Result struct {
	Outcome Outcome
	Date    DateSource
}

// runStats holds the counters behind the final report; all fields are safe for concurrent use.
type runStats struct {
	exif, jsonTaken, jsonCreation, filename, unknown atomic.Int64
	duplicates, shortVideos                          atomic.Int64
	errors, interrupted, unreadable                  atomic.Int64
}

func (s *runStats) countDate(d DateSource) {
	switch d {
	case DateFromEXIF:
		s.exif.Add(1)
	case DateFromJSONTaken:
		s.jsonTaken.Add(1)
	case DateFromJSONCreation:
		s.jsonCreation.Add(1)
	case DateFromFilename:
		s.filename.Add(1)
	case DateUnknown:
		s.unknown.Add(1)
	}
}

func (s *runStats) organized() int64 {
	return s.exif.Load() + s.jsonTaken.Load() + s.jsonCreation.Load() + s.filename.Load() + s.unknown.Load()
}

// safeProcessFile converts a panic while handling one file into an error so a single
// bad file can't take down the whole run.
func (p *Processor) safeProcessFile(ctx context.Context, f MediaFile) (res Result, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic: %v", r)
		}
	}()
	return p.processFile(ctx, f)
}

// processFile handles extracting a single file using a two-pass method to minimize disk writes.
func (p *Processor) processFile(ctx context.Context, f MediaFile) (res Result, err error) {
	// PASS 1: Read the file to calculate its hash. Small files are kept in memory so the
	// remaining steps (date detection, copy) don't have to read the source again.
	hashStr, f, release, err := p.hashAndBuffer(ctx, f)
	if err != nil {
		return Result{}, err
	}
	defer release()

	// Atomically claim the hash so concurrent workers can't both import identical content.
	// Skip extraction entirely if duplicate (Zero Disk Write IO!)
	claimed, err := p.DB.Claim(hashStr)
	if err != nil {
		return Result{}, err
	}
	if !claimed {
		return Result{Outcome: SkippedDuplicate}, nil
	}
	recorded := false
	defer func() {
		if !recorded {
			p.DB.Release(hashStr)
		}
	}()

	if p.SkipShortVideos > 0 && IsMP4Video(f.Name()) {
		if dur, err := GetMediaDuration(f); err == nil && dur > 0 {
			if dur.Seconds() <= float64(p.SkipShortVideos) {
				if !p.DryRun {
					// Finalize record so we don't scan it again if re-run
					_ = p.DB.Insert(hashStr, filepath.Base(f.Name()))
					recorded = true
				}
				return Result{Outcome: SkippedShortVideo}, nil
			}
		}
	}

	// 1. Determine destination path
	captureTime, date := FindCaptureDate(f)
	albumName := ExtractAlbumName(f.Name())

	var folderDate string
	if albumName != "" {
		folderDate = filepath.Join("Albums", albumName)
	} else if date == DateUnknown {
		folderDate = "Unknown_Date"
	} else {
		folderDate = captureTime.Format("2006-01-02")
	}

	destDir := filepath.Join(p.OutputDir, folderDate)
	baseName := filepath.Base(f.Name())

	if p.DryRun {
		return Result{Date: date}, nil
	}

	if err := os.MkdirAll(destDir, 0755); err != nil {
		return Result{}, err
	}

	// PASS 2: Stream the uncompressed data AGAIN to write it to disk
	if err := p.writeFile(ctx, f, filepath.Join(destDir, baseName)); err != nil {
		return Result{}, err
	}

	// Finalize record
	if err := p.DB.Insert(hashStr, baseName); err != nil {
		return Result{}, err
	}
	recorded = true

	return Result{Date: date}, nil
}

// writeFile copies f to a free name derived from basePath. The name is reserved
// atomically (O_EXCL) so concurrent workers never overwrite each other, and the
// data goes through a temp file so a failed copy never leaves a partial file
// under its final name.
func (p *Processor) writeFile(ctx context.Context, f MediaFile, basePath string) (err error) {
	destPath, err := reserveDestPath(basePath)
	if err != nil {
		return err
	}
	tmpPath := ""
	defer func() {
		if err != nil {
			if tmpPath != "" {
				os.Remove(tmpPath)
			}
			os.Remove(destPath) // release the empty reservation
		}
	}()

	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()

	tmp, err := os.CreateTemp(filepath.Dir(destPath), ".photosorter-*.tmp")
	if err != nil {
		return err
	}
	tmpPath = tmp.Name()

	if _, err = io.Copy(tmp, &ctxReader{ctx: ctx, r: rc}); err != nil {
		tmp.Close()
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	// CreateTemp uses 0600; match the 0666-minus-umask mode os.Create would have used.
	if err = os.Chmod(tmpPath, 0644); err != nil {
		return err
	}
	// Rename replaces the empty reservation atomically.
	return os.Rename(tmpPath, destPath)
}

// reserveDestPath creates an empty file at basePath, or at basePath with a _N suffix
// if that name is taken, and returns the path it created. O_EXCL makes the check and
// the creation a single atomic step.
func reserveDestPath(basePath string) (string, error) {
	ext := filepath.Ext(basePath)
	name := strings.TrimSuffix(basePath, ext)
	out := basePath
	for counter := 1; ; counter++ {
		f, err := os.OpenFile(out, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
		if err == nil {
			return out, f.Close()
		}
		if !os.IsExist(err) {
			return "", err
		}
		out = fmt.Sprintf("%s_%d%s", name, counter, ext)
	}
}

// ctxReader makes long copies abortable: reads fail with the context's error once it is cancelled.
type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (c *ctxReader) Read(b []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(b)
}

// hashAndBuffer hashes f. When f is small enough it is read into memory and the returned
// MediaFile serves its content from there, so later steps cost no further source reads.
// The caller must call release when done with the returned MediaFile.
func (p *Processor) hashAndBuffer(ctx context.Context, f MediaFile) (hash string, mf MediaFile, release func(), err error) {
	if sz, ok := f.(interface{ Size() int64 }); ok && p.BufferLimit > 0 {
		if size := sz.Size(); size >= 0 && size <= p.BufferLimit {
			hash, mf, release, ok, err := p.bufferAndHash(ctx, f, size)
			if err != nil || ok {
				return hash, mf, release, err
			}
			// Larger than advertised: fall through and stream it instead of holding it in memory.
		}
	}
	hash, err = streamHash(ctx, f)
	return hash, f, func() {}, err
}

// bufferAndHash reads f into a pooled buffer and hashes it. ok is false if the file turned out
// to exceed BufferLimit (the size hint came from untrusted metadata), in which case nothing is kept.
func (p *Processor) bufferAndHash(ctx context.Context, f MediaFile, size int64) (hash string, mf MediaFile, release func(), ok bool, err error) {
	rc, err := f.Open()
	if err != nil {
		return "", nil, nil, false, err
	}
	defer rc.Close()

	buf, _ := p.bufPool.Get().(*bytes.Buffer)
	if buf == nil {
		buf = new(bytes.Buffer)
	}
	buf.Reset()
	buf.Grow(int(size) + 1)
	release = func() { p.bufPool.Put(buf) }

	r := io.LimitReader(&ctxReader{ctx: ctx, r: rc}, p.BufferLimit+1)
	if _, err := buf.ReadFrom(r); err != nil {
		release()
		return "", nil, nil, false, err
	}
	if int64(buf.Len()) > p.BufferLimit {
		release()
		return "", nil, nil, false, nil
	}

	h := xxhash.New()
	h.Write(buf.Bytes())
	return hex.EncodeToString(h.Sum(nil)), &bufferedFile{MediaFile: f, data: buf.Bytes()}, release, true, nil
}

func streamHash(ctx context.Context, f MediaFile) (string, error) {
	rc, err := f.Open()
	if err != nil {
		return "", err
	}
	defer rc.Close()
	h := xxhash.New()
	if _, err := io.Copy(h, &ctxReader{ctx: ctx, r: rc}); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// bufferedFile serves an already-read file's content from memory.
type bufferedFile struct {
	MediaFile
	data []byte
}

func (b *bufferedFile) Open() (io.ReadCloser, error) {
	return memReader{bytes.NewReader(b.data)}, nil
}

// memReader is a ReadCloser that keeps bytes.Reader's Seek, which lets callers skip data cheaply.
type memReader struct{ *bytes.Reader }

func (memReader) Close() error { return nil }
