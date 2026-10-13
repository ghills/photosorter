package library

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sync"

	"github.com/cespare/xxhash/v2"

	"photosorter/internal/media"
	"photosorter/internal/parallel"
)

// Hash returns the content hash used to recognise duplicate media.
func Hash(r io.Reader) (string, error) {
	h := xxhash.New()
	if _, err := io.Copy(h, r); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// HashBytes is [Hash] for content already in memory.
func HashBytes(b []byte) string {
	return hex.EncodeToString(binary.BigEndian.AppendUint64(nil, xxhash.Sum64(b)))
}

// CountMedia counts the media files under dir. Unreadable paths are skipped.
func CountMedia(dir string, filter media.Filter) int {
	n := 0
	filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && filter.MatchIn(dir, p) {
			n++
		}
		return nil
	})
	return n
}

// SyncOptions configures [Sync].
type SyncOptions struct {
	Workers  int
	Filter   media.Filter
	Progress io.Writer // progress bar destination; nil for none
}

// SyncResult describes what [Sync] did.
type SyncResult struct {
	// Hashed is the number of files hashed and recorded.
	Hashed int
	// Unreadable lists paths the folder walk could not read ("path: error").
	Unreadable []string
	// Failed lists files that could not be hashed ("path: error").
	Failed []string
}

// Sync hashes every media file under dir and records it in db, so content already in
// the library is recognised as a duplicate. It returns early with ctx's error if ctx is
// cancelled, or with the database error if writes start failing.
//
// Files that cannot be read are reported in the result rather than stopping the sync:
// they are simply not protected against re-import.
func Sync(ctx context.Context, dir string, db *DB, opts SyncOptions) (SyncResult, error) {
	var res SyncResult
	var paths []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		switch {
		case err != nil:
			res.Unreadable = append(res.Unreadable, fmt.Sprintf("%s: %v", p, err))
		case !d.IsDir() && opts.Filter.MatchIn(dir, p):
			paths = append(paths, p)
		}
		return nil
	})
	if err != nil {
		return res, err
	}

	var mu sync.Mutex
	parallel.ForEach(ctx, paths, parallel.Options{
		Workers:  opts.Workers,
		Stop:     func() bool { return db.Err() != nil },
		Progress: opts.Progress,
		Label:    "Syncing library",
	}, func(p string) {
		err := recordFile(p, db)
		mu.Lock()
		defer mu.Unlock()
		if err != nil {
			res.Failed = append(res.Failed, fmt.Sprintf("%s: %v", p, err))
		} else {
			res.Hashed++
		}
	})

	if err := ctx.Err(); err != nil {
		return res, err
	}
	return res, db.Err()
}

// recordFile hashes the file at path and records it under its base name.
func recordFile(path string, db *DB) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	hash, err := Hash(f)
	if err != nil {
		return err
	}
	return db.Record(hash, filepath.Base(path))
}
