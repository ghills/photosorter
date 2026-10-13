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

// mvhdReadLimit bounds how much of an mvhd box we read. Versions 0 and 1 need 20 and 32
// bytes respectively, so anything past that is never touched.
const mvhdReadLimit = 32

// maxMediaSeconds caps plausible durations (~68 years) so corrupt headers can't overflow time.Duration.
const maxMediaSeconds = 1 << 31

// GetMediaDuration reads the moov -> mvhd atom from an MP4/MOV file to determine its exact duration.
// It tolerates corrupt or truncated input by returning an error rather than panicking.
func GetMediaDuration(f MediaFile) (time.Duration, error) {
	rc, err := f.Open()
	if err != nil {
		return 0, err
	}
	defer rc.Close()

	var header [8]byte
	for {
		if _, err := io.ReadFull(rc, header[:]); err != nil {
			return 0, err
		}

		size := binary.BigEndian.Uint32(header[0:4])
		boxType := string(header[4:8])

		var realSize int64
		var headerLen int64 = 8
		switch size {
		case 0:
			// Box extends to end of file. Only a moov box is worth descending into.
			if boxType != "moov" {
				return 0, fmt.Errorf("reached end of file box %q, moov not found", boxType)
			}
			continue
		case 1:
			var extSize [8]byte
			if _, err := io.ReadFull(rc, extSize[:]); err != nil {
				return 0, err
			}
			realSize = int64(binary.BigEndian.Uint64(extSize[:])) // negative if the top bit is set
			headerLen = 16
		default:
			realSize = int64(size)
		}
		if realSize < headerLen {
			return 0, fmt.Errorf("invalid %q box size %d", boxType, realSize)
		}

		if boxType == "moov" {
			continue // Check children
		}

		bodySize := realSize - headerLen

		if boxType == "mvhd" {
			return parseMvhd(rc, bodySize)
		}

		if err := skipBytes(rc, bodySize); err != nil {
			return 0, err
		}
	}
}

// parseMvhd decodes the duration from the start of an mvhd box body of bodySize bytes.
func parseMvhd(r io.Reader, bodySize int64) (time.Duration, error) {
	n := bodySize
	if n > mvhdReadLimit {
		n = mvhdReadLimit
	}
	body := make([]byte, n)
	if _, err := io.ReadFull(r, body); err != nil {
		return 0, err
	}
	if len(body) < 1 {
		return 0, fmt.Errorf("mvhd box empty")
	}

	var timeScale uint32
	var duration uint64
	switch version := body[0]; version {
	case 0:
		if len(body) < 20 {
			return 0, fmt.Errorf("mvhd box too small")
		}
		timeScale = binary.BigEndian.Uint32(body[12:16])
		duration = uint64(binary.BigEndian.Uint32(body[16:20]))
	case 1:
		if len(body) < 32 {
			return 0, fmt.Errorf("mvhd box too small")
		}
		timeScale = binary.BigEndian.Uint32(body[20:24])
		duration = binary.BigEndian.Uint64(body[24:32])
	default:
		return 0, fmt.Errorf("unsupported mvhd version %d", version)
	}
	if timeScale == 0 {
		return 0, fmt.Errorf("invalid timescale 0")
	}

	secs := duration / uint64(timeScale)
	if secs > maxMediaSeconds {
		return 0, fmt.Errorf("implausible duration: %d/%d", duration, timeScale)
	}
	rem := duration % uint64(timeScale)
	return time.Duration(secs)*time.Second + time.Duration(rem)*time.Second/time.Duration(timeScale), nil
}

// skipBytes advances r by n bytes, seeking when the reader supports it (local files)
// instead of reading and discarding. A skip past the end of the data is an error.
func skipBytes(r io.Reader, n int64) error {
	if n <= 0 {
		return nil
	}
	if s, ok := r.(io.Seeker); ok {
		cur, err := s.Seek(0, io.SeekCurrent)
		if err == nil {
			end, err := s.Seek(0, io.SeekEnd)
			if err == nil {
				if n > end-cur {
					return io.ErrUnexpectedEOF
				}
				_, err = s.Seek(cur+n, io.SeekStart)
				return err
			}
		}
		return err
	}
	copied, err := io.CopyN(io.Discard, r, n)
	if err == io.EOF && copied < n {
		return io.ErrUnexpectedEOF
	}
	return err
}
