package main

import (
	"archive/zip"
	"encoding/json"
	"io"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/rwcarlsen/goexif/exif"
)

var dateRegex = regexp.MustCompile(`(?i)(?:^|IMG[-_]?|VID[-_]?|Screenshot[-_]?|WhatsApp Image )?((?:19|20)\d{2})[-_]?(\d{2})[-_]?(\d{2})`)

// parseDateFromFilename attempts to extract dates from typical camera filename patterns.
func parseDateFromFilename(filename string) (time.Time, bool) {
	base := filepath.Base(filename)
	matches := dateRegex.FindStringSubmatch(base)
	if len(matches) == 4 {
		year, _ := strconv.Atoi(matches[1])
		month, _ := strconv.Atoi(matches[2])
		day, _ := strconv.Atoi(matches[3])
		
		if month >= 1 && month <= 12 && day >= 1 && day <= 31 {
			// Using Local timezone closely matches our Unix timestamp parsing standard
			return time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.Local), true
		}
	}
	return time.Time{}, false
}

type GooglePhotoMetadata struct {
	Title          string `json:"title"`
	Description    string `json:"description"`
	CreationTime   struct {
		Timestamp string `json:"timestamp"`
		Formatted string `json:"formatted"`
	} `json:"creationTime"`
	PhotoTakenTime struct {
		Timestamp string `json:"timestamp"`
		Formatted string `json:"formatted"`
	} `json:"photoTakenTime"`
}

// FindCaptureDate attempts to determine the date a media file was taken.
// It checks EXIF data first (reading a 256KB header), then checks associated Google Photos
// metadata JSON sidecars, and finally falls back to parsing dates out of the filename.
// Returns the parsed time object and the origin method used to find it.
func FindCaptureDate(f *zip.File, zipIndex map[string]*zip.File) (time.Time, string) {
	// First: Try reading Exif directly from zip stream
	if IsImage(f.Name) {
		rc, err := f.Open()
		if err == nil {
			lr := io.LimitReader(rc, 262144) // Check first 256KB for performance mapping
			x, err := exif.Decode(lr)
			if err == nil {
				tm, err := x.DateTime()
				if err == nil {
					rc.Close()
					return tm, "exif"
				}
			}
			rc.Close()
		}
	}

	// Second: fallback to JSON sidecar metadata
	base := strings.TrimSuffix(f.Name, filepath.Ext(f.Name))
	jsonFilesToTry := []string{
		f.Name + ".json",
		f.Name + ".supplemental-metadata.json",
		base + ".json",
		base + ".supplemental-metadata.json",
	}

	var jsonFile *zip.File
	var ok bool
	for _, name := range jsonFilesToTry {
		if jf, found := zipIndex[name]; found {
			jsonFile = jf
			ok = true
			break
		}
	}
	
	if ok {
		jrc, err := jsonFile.Open()
		if err == nil {
			var metadata GooglePhotoMetadata
			err = json.NewDecoder(jrc).Decode(&metadata)
			jrc.Close()
			if err == nil {
				if metadata.PhotoTakenTime.Timestamp != "" {
					ts, err := strconv.ParseInt(metadata.PhotoTakenTime.Timestamp, 10, 64)
					if err == nil && ts > 0 {
						return time.Unix(ts, 0), "json_taken"
					}
				}
				if metadata.CreationTime.Timestamp != "" {
					ts, err := strconv.ParseInt(metadata.CreationTime.Timestamp, 10, 64)
					if err == nil && ts > 0 {
						return time.Unix(ts, 0), "json_creation"
					}
				}
			}
		}
	}

	// Third: Fallback to reading the date from filename
	if tm, found := parseDateFromFilename(f.Name); found {
		return tm, "filename"
	}

	// Fallback to unknown
	return time.Time{}, "unknown"
}

// IsImage checks if a filename extension corresponds to a commonly EXIF-supported format.
func IsImage(filename string) bool {
	ext := strings.ToLower(filepath.Ext(filename))
	switch ext {
	case ".jpg", ".jpeg", ".tiff", ".tif", ".heic":
		return true
	}
	return false
}

// IsMedia verifies if the filename extension is accepted by the photosorter pipeline.
func IsMedia(filename string) bool {
	ext := strings.ToLower(filepath.Ext(filename))
	switch ext {
	case ".jpg", ".jpeg", ".png", ".gif", ".webp", ".heic", ".bmp", ".tiff", ".tif", ".mp4", ".mov", ".avi", ".mkv", ".webm", ".mts", ".3gp", ".m4v":
		return true
	}
	return false
}
