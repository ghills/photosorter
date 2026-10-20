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
	// Root is the folder that was scanned.
	Root string
	// Paths are the media files.
	Paths []string
	// Unreadable lists paths the walk could not read ("path: error").
	Unreadable []string
}

// Scan lists the media files under dir. Unreadable paths are reported rather than
// stopping the walk; a missing dir is reported the same way. It returns early with
// ctx's error if ctx is cancelled.
func Scan(ctx context.Context, dir string, filter media.Filter) (Media, error) {
	m := Media{Root: dir}
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

	// Rehash hashes every file, even those whose size and modification time match
	// what the database recorded for their path.
	Rehash bool
}

// SyncResult describes what [Sync] did.
type SyncResult struct {
	// Hashed is the number of files hashed and recorded.
	Hashed int
	// Unchanged is the number of files skipped because their path, size and
	// modification time match what the database recorded.
	Unchanged int
	// Failed lists files that could not be hashed ("path: error").
	Failed []string
	// Pruned lists the file names of the records removed by [SyncOptions.Prune].
	Pruned []string
	// PruneSkipped says why pruning was asked for but not done; empty otherwise.
	PruneSkipped string
}

// Sync hashes the library's media files (as found by [Scan]) and records them in db,
// so content already in the library is recognised as a duplicate. Files whose path,
// size and modification time match [DB.Files] are not hashed again unless
// [SyncOptions.Rehash] is set; paths no longer in the library are dropped from it. On success it also
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
	stored, err := db.Files()
	if err != nil {
		return res, err
	}
	var mu sync.Mutex
	seen := make(map[string]struct{}, len(files.Paths))
	scanned := make(map[string]struct{}, len(files.Paths))
	parallel.ForEach(ctx, files.Paths, parallel.Options{
		Workers:  opts.Workers,
		Stop:     func() bool { return db.Err() != nil },
		Progress: opts.Progress,
		Label:    "Syncing library",
	}, func(p string) {
		hash, unchanged, rel, err := syncFile(files.Root, p, stored, opts.Rehash, db)
		mu.Lock()
		defer mu.Unlock()
		scanned[rel] = struct{}{}
		switch {
		case err != nil:
			res.Failed = append(res.Failed, fmt.Sprintf("%s: %v", p, err))
		case unchanged:
			res.Unchanged++
			seen[hash] = struct{}{}
		default:
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
	var gone []string
	for p := range stored {
		if _, ok := scanned[p]; !ok {
			gone = append(gone, p)
		}
	}
	if err := db.ForgetFiles(gone); err != nil {
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

// syncFile records the library file at path in db and returns its hash. If its path,
// size and modification time match stored, the stored hash is used without reading the
// file (unless rehash is set) and unchanged is true. rel is its path relative to root.
func syncFile(root, path string, stored map[string]StoredFile, rehash bool, db *DB) (hash string, unchanged bool, rel string, err error) {
	rel, err = filepath.Rel(root, path)
	if err != nil {
		rel = path
	}
	rel = filepath.ToSlash(rel)
	// Stat before reading, so a file changed while it is hashed looks changed next time.
	state, err := StatFile(path, rel)
	if err != nil {
		return "", false, rel, err
	}
	if s, ok := stored[rel]; ok && !rehash && s.FileState == state {
		// Already in library_files; make sure the hash is known too (a no-op normally).
		return s.Hash, true, rel, db.Record(s.Hash, filepath.Base(path))
	}
	f, err := os.Open(path)
	if err != nil {
		return "", false, rel, err
	}
	defer f.Close()
	if hash, err = Hash(f); err != nil {
		return "", false, rel, err
	}
	return hash, false, rel, db.RecordFile(hash, state)
}
