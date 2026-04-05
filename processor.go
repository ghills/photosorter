package main

import (
	"archive/zip"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/cespare/xxhash/v2"
	"github.com/schollz/progressbar/v3"
)

type Processor struct {
	ZipPath    string
	OutputDir  string
	DB         *HashDB
	ZipIndex   map[string]*zip.File
	TotalItems int
}

func NewProcessor(zipPath, outputDir string, db *HashDB) *Processor {
	return &Processor{
		ZipPath:   zipPath,
		OutputDir: outputDir,
		DB:        db,
		ZipIndex:  make(map[string]*zip.File),
	}
}

func (p *Processor) Run() error {
	r, err := zip.OpenReader(p.ZipPath)
	if err != nil {
		return fmt.Errorf("failed to open zip: %w", err)
	}
	defer r.Close()

	// Build index
	var mediaFiles []*zip.File
	for _, f := range r.File {
		if f.FileInfo().IsDir() {
			continue
		}
		p.ZipIndex[f.Name] = f
		if IsMedia(f.Name) {
			mediaFiles = append(mediaFiles, f)
		}
	}

	p.TotalItems = len(mediaFiles)
	if p.TotalItems == 0 {
		fmt.Println("No media files found in zip.")
		return nil
	}

	bar := progressbar.Default(int64(p.TotalItems), "Processing media")

	var duplicatesSkipped int
	var countExif int
	var countJsonTaken int
	var countJsonCreation int
	var countFilename int
	var countUnknown int
	var errorsCount int

	for _, mF := range mediaFiles {
		origin, err := p.processFile(mF)
		if err != nil {
			fmt.Fprintf(os.Stderr, "\nError processing %s: %v\n", mF.Name, err)
			errorsCount++
		} else {
			switch origin {
			case "skipped":
				duplicatesSkipped++
			case "exif":
				countExif++
			case "json_taken":
				countJsonTaken++
			case "json_creation":
				countJsonCreation++
			case "filename":
				countFilename++
			case "unknown":
				countUnknown++
			}
		}
		bar.Add(1)
	}

	totalOrganized := countExif + countJsonTaken + countJsonCreation + countFilename + countUnknown

	fmt.Printf("\n\n--- Final Report ---\n")
	fmt.Printf("Total Media Found        : %d\n", p.TotalItems)
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
func (p *Processor) processFile(f *zip.File) (string, error) {
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
	captureTime, origin := FindCaptureDate(f, p.ZipIndex)

	var folderDate string
	if origin == "unknown" {
		folderDate = "Unknown_Date"
	} else {
		folderDate = captureTime.Format("2006-01-02")
	}

	destDir := filepath.Join(p.OutputDir, folderDate)
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return "", err
	}

	baseName := filepath.Base(f.Name)
	destPath := getUniqueDestPath(filepath.Join(destDir, baseName))

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
