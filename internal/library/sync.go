package library

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"errors"
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

// Media lists the media files found under a library folder by [Scan].
type Media struct {
	// Paths are the media files.
	Paths []string
	// Unreadable lists paths the walk could not read ("path: error").
	Unreadable []string
}

// Scan lists the media files under dir. Unreadable paths are reported rather than
// stopping the walk; a missing dir is reported the same way. It returns early with
// ctx's error if ctx is cancelled.
func Scan(ctx context.Context, dir string, filter media.Filter) (Media, error) {
	var m Media
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		switch {
		case err != nil:
			if p != dir || !errors.Is(err, fs.ErrNotExist) {
				m.Unreadable = append(m.Unreadable, fmt.Sprintf("%s: %v", p, err))
			}
		case !d.IsDir() && filter.MatchIn(dir, p):
			m.Paths = append(m.Paths, p)
		}
		return nil
	})
	return m, err
}

// SyncOptions configures [Sync].
type SyncOptions struct {
	Workers  int
	Progress io.Writer // progress bar destination; nil for none

	// Prune also forgets recorded hashes whose content is no longer in the library
	// (see [DB.Prune]), so it can be imported again. It is skipped if the library looks
	// incomplete: no media found, or any path that could not be read or hashed.
	Prune bool
}

// SyncResult describes what [Sync] did.
type SyncResult struct {
	// Hashed is the number of files hashed and recorded.
	Hashed int
	// Failed lists files that could not be hashed ("path: error").
	Failed []string
	// Pruned lists the file names of the records removed by [SyncOptions.Prune].
	Pruned []string
	// PruneSkipped says why pruning was asked for but not done; empty otherwise.
	PruneSkipped string
}

// Sync hashes the library's media files (as found by [Scan]) and records them in db,
// so content already in the library is recognised as a duplicate. On success it also
// stores the number of files as [DB.LibraryFiles]. It returns early with ctx's error
// if ctx is cancelled, or with the database error if writes start failing.
//
// Files that cannot be read are reported in the result rather than stopping the sync:
// they are simply not protected against re-import.
//
// With [SyncOptions.Prune], hashes of content no longer found in the library are then
// removed from db.
func Sync(ctx context.Context, files Media, db *DB, opts SyncOptions) (SyncResult, error) {
	var res SyncResult
	var mu sync.Mutex
	seen := make(map[string]struct{}, len(files.Paths))
	parallel.ForEach(ctx, files.Paths, parallel.Options{
		Workers:  opts.Workers,
		Stop:     func() bool { return db.Err() != nil },
		Progress: opts.Progress,
		Label:    "Syncing library",
	}, func(p string) {
		hash, err := recordFile(p, db)
		mu.Lock()
		defer mu.Unlock()
		if err != nil {
			res.Failed = append(res.Failed, fmt.Sprintf("%s: %v", p, err))
		} else {
			res.Hashed++
			seen[hash] = struct{}{}
		}
	})

	if err := ctx.Err(); err != nil {
		return res, err
	}
	if err := db.Err(); err != nil {
		return res, err
	}
	if opts.Prune {
		// A hash is only stale if we are sure its content is gone; an unreadable file
		// or an empty scan (say, an unmounted drive) would wrongly forget content.
		switch {
		case len(files.Paths) == 0:
			res.PruneSkipped = "no media files were found in the library"
		case len(files.Unreadable) > 0 || len(res.Failed) > 0:
			res.PruneSkipped = "some library paths could not be read"
		default:
			pruned, err := db.Prune(seen)
			if err != nil {
				return res, err
			}
			res.Pruned = pruned
		}
	}
	return res, db.SetLibraryFiles(len(files.Paths))
}

// recordFile hashes the file at path and records it under its base name.
func recordFile(path string, db *DB) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	hash, err := Hash(f)
	if err != nil {
		return "", err
	}
	return hash, db.Record(hash, filepath.Base(path))
}
