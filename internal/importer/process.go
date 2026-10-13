package importer

import (
	"context"
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
	date    DateSource
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

	// 3. Skip motion-photo clips and other very short videos. They are recorded, so a
	// later run doesn't have to inspect them again.
	if im.isShortVideo(f) {
		keepClaim = true
		if im.opts.DryRun {
			return result{outcome: shortVideo}, nil
		}
		return result{outcome: shortVideo}, im.db.Record(hash, filepath.Base(f.Name()))
	}

	// 4 & 5. Date the file and choose its folder.
	taken, dateSrc := captureDate(f, im.opts.Location)
	dir := im.opts.Layout.Dir(f.Name(), taken, dateSrc)
	rel := filepath.Join(dir, filepath.Base(f.Name()))

	if im.opts.DryRun {
		keepClaim = true // so identical files later in the run count as duplicates
		return result{outcome: imported, date: dateSrc, dest: rel}, nil
	}

	// 6. Copy under a free name, then record the hash.
	if err := os.MkdirAll(filepath.Join(im.opts.Library, dir), 0755); err != nil {
		return result{}, err
	}
	written, err := writeFile(ctx, f, filepath.Join(im.opts.Library, rel))
	if err != nil {
		return result{}, err
	}
	if err := im.db.Record(hash, filepath.Base(written)); err != nil {
		return result{}, err
	}
	keepClaim = true
	rel, _ = filepath.Rel(im.opts.Library, written)
	return result{outcome: imported, date: dateSrc, dest: rel}, nil
}

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
	d, err := media.MP4Duration(rc)
	return err == nil && d > 0 && d <= im.opts.SkipVideosUpTo
}
