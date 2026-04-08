package main

import (
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"

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
		if !d.IsDir() && IsMedia(d.Name()) {
			count++
		}
		return nil
	})
	return count, err
}

// SyncFolderWithDB hashes all media files in the target directory and inserts them into the DB.
func SyncFolderWithDB(dir string, db *HashDB) error {
	var mediaFiles []string

	fmt.Println("Scanning directory for media files...")
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() && IsMedia(d.Name()) {
			mediaFiles = append(mediaFiles, path)
		}
		return nil
	})
	if err != nil {
		return err
	}

	if len(mediaFiles) == 0 {
		fmt.Println("No media files found to sync.")
		return nil
	}

	bar := progressbar.Default(int64(len(mediaFiles)), "Syncing to database")

	for _, path := range mediaFiles {
		err := appendToDB(path, db)
		if err != nil {
			fmt.Fprintf(os.Stderr, "\nError hashing %s: %v\n", path, err)
		}
		bar.Add(1)
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
