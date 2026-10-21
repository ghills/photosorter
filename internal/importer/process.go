package importer

import (
	"context"
	"io"
	"os"
	"path/filepath"

	"photosorter/internal/library"
	"photosorter/internal/media"
	"photosorter/internal/source"
	"photosorter/internal/xmp"
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

	// xmp is the XMP sidecar written next to dest, relative to the library, or ""
	// for none. xmpErr is why writing one failed; the file is still imported.
	xmp    string
	xmpErr error
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
	movie := readMovie(f)
	if im.isShortVideo(movie) {
		return result{outcome: shortVideo}, nil
	}

	// 4 & 5. Date the file and choose its folder.
	meta := readSidecar(f)
	date := im.dateFile(f, movie, meta)
	dir := im.opts.Layout.Dir(f.Name(), date.t, date.prec, date.src)
	rel := filepath.Join(dir, filepath.Base(f.Name()))
	var fields xmp.Fields
	if !im.opts.NoXMP {
		fields = xmpFields(meta, date)
	}

	if im.opts.DryRun {
		keepClaim = true
		im.db.Keep(hash) // so identical files later in the run count as duplicates
		res := result{outcome: imported, date: date, dest: rel}
		if !fields.Empty() {
			res.xmp = xmp.SidecarPath(rel)
		}
		return res, nil
	}

	// 6. Copy under a free name, then record the hash.
	if err := os.MkdirAll(filepath.Join(im.opts.Library, dir), 0755); err != nil {
		return result{}, err
	}
	written, err := writeFile(ctx, f, filepath.Join(im.opts.Library, rel), im.tempDir())
	if err != nil {
		return result{}, err
	}
	rel, _ = filepath.Rel(im.opts.Library, written)
	res := result{outcome: imported, date: date, dest: rel}

	// Remember where the file went, so a later sync needn't hash it again.
	if state, statErr := library.StatFile(written, rel); statErr == nil {
		err = im.db.RecordFile(hash, state)
	} else {
		err = im.db.Record(hash, filepath.Base(written))
	}
	if err != nil {
		return result{}, err
	}
	keepClaim = true

	// 7. Carry the sidecar's metadata over, if the date came from it.
	if !fields.Empty() {
		if sc, err := xmp.WriteSidecar(written, fields); err != nil {
			res.xmpErr = err
		} else {
			res.xmp, _ = filepath.Rel(im.opts.Library, sc)
		}
	}
	return res, nil
}

// maxMovieScan is how far into a non-seekable stream (a zip entry too large to buffer)
// readMovie reads before giving up. Without a seek, a video whose moov box sits after
// its media data would otherwise be decompressed in full.
const maxMovieScan = 64 << 20

// readMovie returns f's MP4/QuickTime movie header, or nil if f is not such a video or
// the header can't be read. It is read once and used for both the short-video check
// and the date.
func readMovie(f source.File) *media.MovieHeader {
	if !media.IsMP4(f.Name()) {
		return nil
	}
	rc, err := f.Open()
	if err != nil {
		return nil
	}
	defer rc.Close()
	var r io.Reader = rc
	if _, ok := rc.(io.Seeker); !ok {
		r = io.LimitReader(rc, maxMovieScan)
	}
	h, err := media.ReadMovieHeader(r)
	if err != nil {
		return nil
	}
	return &h
}

// isShortVideo reports whether movie is no longer than the configured threshold.
// Videos whose duration can't be read are never considered short.
func (im *Importer) isShortVideo(movie *media.MovieHeader) bool {
	if im.opts.SkipVideosUpTo <= 0 || movie == nil {
		return false
	}
	d, err := movie.Duration()
	return err == nil && d > 0 && d <= im.opts.SkipVideosUpTo
}
