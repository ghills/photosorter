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
}

// SyncResult describes what [Sync] did.
type SyncResult struct {
	// Hashed is the number of files hashed and recorded.
	Hashed int
	// Failed lists files that could not be hashed ("path: error").
	Failed []string
}

// Sync hashes the library's media files (as found by [Scan]) and records them in db,
// so content already in the library is recognised as a duplicate. On success it also
// stores the number of files as [DB.LibraryFiles]. It returns early with ctx's error
// if ctx is cancelled, or with the database error if writes start failing.
//
// Files that cannot be read are reported in the result rather than stopping the sync:
// they are simply not protected against re-import.
func Sync(ctx context.Context, files Media, db *DB, opts SyncOptions) (SyncResult, error) {
	var res SyncResult
	var mu sync.Mutex
	parallel.ForEach(ctx, files.Paths, parallel.Options{
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
	if err := db.Err(); err != nil {
		return res, err
	}
	return res, db.SetLibraryFiles(len(files.Paths))
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
