package main

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/rwcarlsen/goexif/exif"
)

var dateRegex = regexp.MustCompile(`(?i)(?:^|IMG[-_]?|VID[-_]?|Screenshot[-_]?|WhatsApp Image )?((?:19|20)\d{2})[-_]?(\d{2})[-_]?(\d{2})`)
var dateFolderRegex = regexp.MustCompile(`^(?:19|20)\d{2}-\d{2}-\d{2}$`)

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
	Title        string `json:"title"`
	Description  string `json:"description"`
	CreationTime struct {
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
func FindCaptureDate(f MediaFile) (time.Time, string) {
	// First: Try reading Exif directly from zip stream
	if IsImage(f.Name()) {
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
	base := strings.TrimSuffix(f.Name(), filepath.Ext(f.Name()))
	jsonFilesToTry := []string{
		f.Name() + ".json",
		f.Name() + ".supplemental-metadata.json",
		base + ".json",
		base + ".supplemental-metadata.json",
	}

	if jrc, ok := f.OpenSidecar(jsonFilesToTry); ok {
		var metadata GooglePhotoMetadata
		err := json.NewDecoder(jrc).Decode(&metadata)
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

	// Third: Fallback to reading the date from filename
	if tm, found := parseDateFromFilename(f.Name()); found {
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
	// Ignore macOS hidden system files and resource forks
	if strings.Contains(filename, "__MACOSX") || strings.HasPrefix(filepath.Base(filename), "._") {
		return false
	}

	ext := strings.ToLower(filepath.Ext(filename))
	switch ext {
	case ".jpg", ".jpeg", ".png", ".gif", ".webp", ".heic", ".bmp", ".tiff", ".tif", ".mp4", ".mov", ".avi", ".mkv", ".webm", ".mts", ".3gp", ".m4v":
		return true
	}
	return false
}

// ExtractAlbumName tries to extract a specific album name from a file path.
// Takeout structure usually places albums in: "Takeout/Google Photos/Album Name/file.jpg"
// Generic folders like "Photos from 2023" or dates like "2023-01-01" are ignored.
func ExtractAlbumName(filePath string) string {
	parts := strings.Split(filepath.ToSlash(filePath), "/")

	// We need at least 'Google Photos', an album name, and a file name
	for i := 0; i < len(parts)-2; i++ {
		if parts[i] == "Google Photos" {
			folderName := parts[i+1]

			// Ignore standard date-based or generic folders that Google Photos creates
			if strings.HasPrefix(folderName, "Photos from {") || strings.HasPrefix(folderName, "Photos from ") {
				continue
			}

			// Ignore exactly YYYY-MM-DD
			if dateFolderRegex.MatchString(folderName) {
				continue
			}

			return folderName
		}
	}

	return ""
}

// IsMP4Video checks if the file is an mp4 or mov wrapper which we can parse for duration.
func IsMP4Video(filename string) bool {
	ext := strings.ToLower(filepath.Ext(filename))
	switch ext {
	case ".mp4", ".mov":
		return true
	}
	return false
}

// GetMediaDuration reads the moov -> mvhd atom from an MP4/MOV file to determine its exact duration.
func GetMediaDuration(f MediaFile) (time.Duration, error) {
	rc, err := f.Open()
	if err != nil {
		return 0, err
	}
	defer rc.Close()

	var header [8]byte
	for {
		_, err := io.ReadFull(rc, header[:])
		if err != nil {
			return 0, err
		}
		
		size := binary.BigEndian.Uint32(header[0:4])
		boxType := string(header[4:8])
		
		var realSize int64
		var headerLen int64 = 8
		if size == 1 {
			var extSize [8]byte
			if _, err := io.ReadFull(rc, extSize[:]); err != nil {
				return 0, err
			}
			realSize = int64(binary.BigEndian.Uint64(extSize[:]))
			headerLen = 16
		} else if size == 0 {
			// Extends to end of file
			return 0, fmt.Errorf("reached end of file box, moov not found")
		} else {
			realSize = int64(size)
		}
		
		if boxType == "moov" {
			continue // Check children
		}
		
		if boxType == "mvhd" {
			bodySize := realSize - headerLen
			body := make([]byte, bodySize)
			if _, err := io.ReadFull(rc, body); err != nil {
				return 0, err
			}
			
			version := body[0]
			if version == 0 {
				if len(body) < 20 {
					return 0, fmt.Errorf("mvhd box too small")
				}
				timeScale := binary.BigEndian.Uint32(body[12:16])
				duration := binary.BigEndian.Uint32(body[16:20])
				if timeScale == 0 {
					return 0, fmt.Errorf("invalid timescale 0")
				}
				return time.Duration(duration) * time.Second / time.Duration(timeScale), nil
			} else if version == 1 {
				if len(body) < 28 {
					return 0, fmt.Errorf("mvhd box too small")
				}
				timeScale := binary.BigEndian.Uint32(body[20:24])
				duration := binary.BigEndian.Uint64(body[24:32])
				if timeScale == 0 {
					return 0, fmt.Errorf("invalid timescale 0")
				}
				return time.Duration(duration) * time.Second / time.Duration(timeScale), nil
			}
			return 0, fmt.Errorf("unsupported mvhd version %d", version)
		}
		
		skipBytes := realSize - headerLen
		if skipBytes > 0 {
			_, err = io.CopyN(io.Discard, rc, skipBytes)
			if err != nil {
				return 0, err
			}
		}
	}
}
