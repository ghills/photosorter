package importer

import (
	"context"
	"io"
	"os"
	"path/filepath"

	"photosorter/internal/media"
	"photosorter/internal/source"
)

// outcome is what happened to a file that was processed without error.
type outcome int

const (
	imported outcome = iota
	duplicate
	shortVideo
)

// result describes one processed file. date and dest are set only when imported.
type result struct {
	outcome outcome
	date    dated
	dest    string // path relative to the library
}

// importFile runs the per-file pipeline described in the package documentation.
func (im *Importer) importFile(ctx context.Context, f source.File) (result, error) {
	// 1. Hash, keeping small files in memory for the steps below.
	hash, f, release, err := im.hashAndBuffer(ctx, f)
	if err != nil {
		return result{}, err
	}
	defer release()

	// 2. Claim the hash so concurrent workers can't both import identical content.
	claimed, err := im.db.Claim(hash)
	if err != nil {
		return result{}, err
	}
	if !claimed {
		return result{outcome: duplicate}, nil
	}
	keepClaim := false
	defer func() {
		if !keepClaim {
			im.db.Release(hash)
		}
	}()

	// 3. Skip motion-photo clips and other very short videos. They are not recorded, so
	// a later run with a lower threshold can still import them.
	if im.isShortVideo(f) {
		return result{outcome: shortVideo}, nil
	}

	// 4 & 5. Date the file and choose its folder.
	date := im.dateFile(f)
	dir := im.opts.Layout.Dir(f.Name(), date.t, date.prec, date.src)
	rel := filepath.Join(dir, filepath.Base(f.Name()))

	if im.opts.DryRun {
		keepClaim = true
		im.db.Keep(hash) // so identical files later in the run count as duplicates
		return result{outcome: imported, date: date, dest: rel}, nil
	}

	// 6. Copy under a free name, then record the hash.
	if err := os.MkdirAll(filepath.Join(im.opts.Library, dir), 0755); err != nil {
		return result{}, err
	}
	written, err := writeFile(ctx, f, filepath.Join(im.opts.Library, rel), im.tempDir())
	if err != nil {
		return result{}, err
	}
	if err := im.db.Record(hash, filepath.Base(written)); err != nil {
		return result{}, err
	}
	keepClaim = true
	rel, _ = filepath.Rel(im.opts.Library, written)
	return result{outcome: imported, date: date, dest: rel}, nil
}

// maxDurationScan is how far into a non-seekable stream (a zip entry too large to
// buffer) the duration check reads before giving up. Without a seek, a video whose
// moov box sits after its media data would otherwise be decompressed in full.
const maxDurationScan = 64 << 20

// isShortVideo reports whether f is a video no longer than the configured threshold.
// Videos whose duration can't be read are never considered short.
func (im *Importer) isShortVideo(f source.File) bool {
	if im.opts.SkipVideosUpTo <= 0 || !media.HasMP4Duration(f.Name()) {
		return false
	}
	rc, err := f.Open()
	if err != nil {
		return false
	}
	defer rc.Close()
	var r io.Reader = rc
	if _, ok := rc.(io.Seeker); !ok {
		r = io.LimitReader(rc, maxDurationScan)
	}
	d, err := media.MP4Duration(r)
	return err == nil && d > 0 && d <= im.opts.SkipVideosUpTo
}
