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
	Sources    []InputSource
	OutputDir  string
	DB         *HashDB
	Workers    int
	DryRun     bool
	Logger     *log.Logger
	TotalItems int
}

func NewProcessor(sources []InputSource, outputDir string, db *HashDB, workers int, dryRun bool, logger *log.Logger) *Processor {
	return &Processor{
		Sources:   sources,
		OutputDir: outputDir,
		DB:        db,
		Workers:   workers,
		DryRun:    dryRun,
		Logger:    logger,
	}
}

func (p *Processor) Run() error {
	var duplicatesSkipped int32
	var countExif int32
	var countJsonTaken int32
	var countJsonCreation int32
	var countFilename int32
	var countUnknown int32
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
	fmt.Printf("Errors                   : %d\n", errorsCount)

	return nil
}

// processFile handles extracting a single file using a two-pass method to minimize disk writes.
func (p *Processor) processFile(f MediaFile) (string, error) {
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
	
	// Evaluate hash
	hashStr := hex.EncodeToString(h.Sum(nil))

	exists, err := p.DB.Exists(hashStr)
	if err != nil {
		return "", err
	}

	// Skip extraction entirely if duplicate (Zero Disk Write IO!)
	if exists {
		return "skipped", nil
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
	if !p.DryRun {
		if err := os.MkdirAll(destDir, 0755); err != nil {
			return "", err
		}
	}

	baseName := filepath.Base(f.Name())
	destPath := getUniqueDestPath(filepath.Join(destDir, baseName))

	if !p.DryRun {
		// PASS 2: Stream the uncompressed data AGAIN to write it to disk
		rc2, err := f.Open()
		if err != nil {
			return "", err
		}
		defer rc2.Close()

		out, err := os.Create(destPath)
		if err != nil {
			return "", err
		}
		defer out.Close()

		if _, err := io.Copy(out, rc2); err != nil {
			return "", err
		}
		
		// Graceful close of disk file
		if err := out.Close(); err != nil {
			return "", err
		}

		// Finalize record
		if err := p.DB.Insert(hashStr, baseName); err != nil {
			return "", err
		}
	}

	return origin, nil
}

func getUniqueDestPath(basePath string) string {
	ext := filepath.Ext(basePath)
	name := strings.TrimSuffix(basePath, ext)
	out := basePath
	counter := 1
	for {
		if _, err := os.Stat(out); os.IsNotExist(err) {
			break
		}
		out = fmt.Sprintf("%s_%d%s", name, counter, ext)
		counter++
	}
	return out
}
