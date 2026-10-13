package main

import (
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"

	"github.com/cespare/xxhash/v2"
	"github.com/schollz/progressbar/v3"
)

// CountMediaFiles quickly walks the directory and counts the amount of media files detected.
func CountMediaFiles(dir string) (int, error) {
	count := 0
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // Skip paths we can't access
		}
		if !d.IsDir() && IsMedia(relMediaPath(dir, path)) {
			count++
		}
		return nil
	})
	return count, err
}

// SyncFolderWithDB hashes all media files in the target directory and inserts them into the DB,
// using up to workers concurrent hashers.
func SyncFolderWithDB(ctx context.Context, dir string, db *HashDB, workers int) error {
	var mediaFiles []string
	var skipped []string

	fmt.Println("Scanning directory for media files...")
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			skipped = append(skipped, fmt.Sprintf("%s: %v", path, err))
			return nil
		}
		if !d.IsDir() && IsMedia(relMediaPath(dir, path)) {
			mediaFiles = append(mediaFiles, path)
		}
		return nil
	})
	if err != nil {
		return err
	}
	reportSkipped(skipped)

	if len(mediaFiles) == 0 {
		fmt.Println("No media files found to sync.")
		return nil
	}

	if workers < 1 {
		workers = 1
	}
	bar := progressbar.Default(int64(len(mediaFiles)), "Syncing to database")

	taskCh := make(chan string, len(mediaFiles))
	for _, path := range mediaFiles {
		taskCh <- path
	}
	close(taskCh)

	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for path := range taskCh {
				if ctx.Err() != nil || db.Err() != nil {
					return
				}
				if err := appendToDB(path, db); err != nil {
					fmt.Fprintf(os.Stderr, "\nError hashing %s: %v\n", path, err)
				}
				bar.Add(1)
			}
		}()
	}
	wg.Wait()

	if err := ctx.Err(); err != nil {
		return err
	}
	if err := db.Err(); err != nil {
		return err
	}

	fmt.Println("\nSync complete.")
	return nil
}

func appendToDB(filePath string, db *HashDB) error {
	f, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer f.Close()

	h := xxhash.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}

	hashStr := hex.EncodeToString(h.Sum(nil))
	baseName := filepath.Base(filePath)

	return db.Insert(hashStr, baseName)
}

// reportSkipped warns about paths a scan could not read, since files in them are not
// being tracked and may later be imported again as duplicates.
func reportSkipped(skipped []string) {
	const maxShown = 10
	if len(skipped) == 0 {
		return
	}
	fmt.Fprintf(os.Stderr, "Warning: skipped %d unreadable path(s):\n", len(skipped))
	for i, s := range skipped {
		if i == maxShown {
			fmt.Fprintf(os.Stderr, "  ... and %d more\n", len(skipped)-maxShown)
			break
		}
		fmt.Fprintf(os.Stderr, "  %s\n", s)
	}
}
