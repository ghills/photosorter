package main

import (
	"encoding/hex"
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
	TotalItems      int
}

func NewProcessor(sources []InputSource, outputDir string, db *HashDB, workers int, skipShortVideos int, dryRun bool, logger *log.Logger) *Processor {
	return &Processor{
		Sources:         sources,
		OutputDir:       outputDir,
		DB:              db,
		Workers:         workers,
		SkipShortVideos: skipShortVideos,
		DryRun:          dryRun,
		Logger:          logger,
	}
}

func (p *Processor) Run() error {
	var duplicatesSkipped int32
	var countExif int32
	var countJsonTaken int32
	var countJsonCreation int32
	var countFilename int32
	var countUnknown int32
	var shortVideosSkipped int32
	var errorsCount int32

	for _, src := range p.Sources {
		if p.Logger != nil {
			p.Logger.Printf("Scanning source: %s", src.Name())
		}

		mediaFiles, err := src.MediaFiles()
		if err != nil {
			if p.Logger != nil {
				p.Logger.Printf("Failed to scan source %s: %v", src.Name(), err)
			}
			fmt.Printf("Failed to scan source %s: %v\n", src.Name(), err)
			continue
		}

		if len(mediaFiles) == 0 {
			src.Close()
			continue
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
					origin, err := p.processFile(mF)
					if err != nil {
						if p.Logger != nil {
							p.Logger.Printf("Error processing %s: %v", mF.Name(), err)
						}
						atomic.AddInt32(&errorsCount, 1)
					} else {
						if origin == "skipped" {
							atomic.AddInt32(&duplicatesSkipped, 1)
							if p.Logger != nil {
								p.Logger.Printf("Skipped duplicate: %s", mF.Name())
							}
						} else if origin == "skipped_short_video" {
							atomic.AddInt32(&shortVideosSkipped, 1)
							if p.Logger != nil {
								p.Logger.Printf("Skipped short video: %s", mF.Name())
							}
						} else {
							if p.Logger != nil {
								p.Logger.Printf("Successfully organized: %s (via %s)", mF.Name(), origin)
							}
							switch origin {
							case "exif":
								atomic.AddInt32(&countExif, 1)
							case "json_taken":
								atomic.AddInt32(&countJsonTaken, 1)
							case "json_creation":
								atomic.AddInt32(&countJsonCreation, 1)
							case "filename":
								atomic.AddInt32(&countFilename, 1)
							case "unknown":
								atomic.AddInt32(&countUnknown, 1)
							}
						}
					}
					bar.Add(1)
				}
			}()
		}
		wg.Wait()
		src.Close()
	}

	totalOrganized := countExif + countJsonTaken + countJsonCreation + countFilename + countUnknown

	fmt.Printf("\n\n--- Final Report ---\n")
	if p.DryRun {
		fmt.Printf("[DRY RUN MODE - No files were written]\n")
	}
	fmt.Printf("Successfully Organized   : %d\n", totalOrganized)
	fmt.Printf("  └ via EXIF             : %d\n", countExif)
	fmt.Printf("  └ via JSON (Taken)     : %d\n", countJsonTaken)
	fmt.Printf("  └ via JSON (Creation)  : %d\n", countJsonCreation)
	fmt.Printf("  └ via Filename Regex   : %d\n", countFilename)
	fmt.Printf("  └ Sent to Unknown_Date : %d\n", countUnknown)
	fmt.Printf("Duplicates Skipped       : %d\n", duplicatesSkipped)
	fmt.Printf("Short Videos Skipped     : %d\n", shortVideosSkipped)
	fmt.Printf("Errors                   : %d\n", errorsCount)

	return nil
}

// processFile handles extracting a single file using a two-pass method to minimize disk writes.
func (p *Processor) processFile(f MediaFile) (origin string, err error) {
	// PASS 1: Read the file strictly to calculate its hash
	rc, err := f.Open()
	if err != nil {
		return "", err
	}
	defer rc.Close()

	h := xxhash.New()
	if _, err := io.Copy(h, rc); err != nil {
		return "", err
	}

	hashStr := hex.EncodeToString(h.Sum(nil))

	// Atomically claim the hash so concurrent workers can't both import identical content.
	// Skip extraction entirely if duplicate (Zero Disk Write IO!)
	claimed, err := p.DB.Claim(hashStr)
	if err != nil {
		return "", err
	}
	if !claimed {
		return "skipped", nil
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
				return "skipped_short_video", nil
			}
		}
	}

	// 1. Determine destination path
	captureTime, origin := FindCaptureDate(f)
	albumName := ExtractAlbumName(f.Name())

	var folderDate string
	if albumName != "" {
		folderDate = filepath.Join("Albums", albumName)
	} else if origin == "unknown" {
		folderDate = "Unknown_Date"
	} else {
		folderDate = captureTime.Format("2006-01-02")
	}

	destDir := filepath.Join(p.OutputDir, folderDate)
	baseName := filepath.Base(f.Name())

	if p.DryRun {
		return origin, nil
	}

	if err := os.MkdirAll(destDir, 0755); err != nil {
		return "", err
	}

	// PASS 2: Stream the uncompressed data AGAIN to write it to disk
	if err := p.writeFile(f, filepath.Join(destDir, baseName)); err != nil {
		return "", err
	}

	// Finalize record
	if err := p.DB.Insert(hashStr, baseName); err != nil {
		return "", err
	}
	recorded = true

	return origin, nil
}

// writeFile copies f to a free name derived from basePath. The name is reserved
// atomically (O_EXCL) so concurrent workers never overwrite each other, and the
// data goes through a temp file so a failed copy never leaves a partial file
// under its final name.
func (p *Processor) writeFile(f MediaFile, basePath string) (err error) {
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

	if _, err = io.Copy(tmp, rc); err != nil {
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
