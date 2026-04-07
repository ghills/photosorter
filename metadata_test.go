package main

import (
	"archive/zip"
	"bytes"
	"testing"
	"time"
)

func TestParseDateFromFilename(t *testing.T) {
	tests := []struct {
		filename  string
		wantYear  int
		wantMonth time.Month
		wantDay   int
		wantFound bool
	}{
		{"IMG_20231015_123456.jpg", 2023, 10, 15, true},
		{"VID_2022-12-05.mp4", 2022, 12, 5, true},
		{"WhatsApp Image 2021-01-01 at 12.00.00.jpg", 2021, 1, 1, true},
		{"20201122_123.jpg", 2020, 11, 22, true},
		{"Screenshot_2019-05-16_123.png", 2019, 5, 16, true},
		{"random_file_name.jpg", 0, 0, 0, false},
		{"20231345_invalid_month.jpg", 0, 0, 0, false},
	}

	for _, tc := range tests {
		t.Run(tc.filename, func(t *testing.T) {
			gotTime, gotFound := parseDateFromFilename(tc.filename)
			if gotFound != tc.wantFound {
				t.Fatalf("parseDateFromFilename(%q) found = %v, want %v", tc.filename, gotFound, tc.wantFound)
			}
			if tc.wantFound {
				if gotTime.Year() != tc.wantYear || gotTime.Month() != tc.wantMonth || gotTime.Day() != tc.wantDay {
					t.Errorf("parseDateFromFilename(%q) time = %v, want %d-%d-%d", tc.filename, gotTime, tc.wantYear, tc.wantMonth, tc.wantDay)
				}
			}
		})
	}
}

func TestIsImage(t *testing.T) {
	tests := []struct {
		filename string
		want     bool
	}{
		{"test.jpg", true},
		{"test.JPEG", true},
		{"test.tiff", true},
		{"test.heic", true},
		{"test.png", false}, // In IsImage, png is not listed for Exif reading phase.
		{"test.mp4", false},
	}

	for _, tc := range tests {
		t.Run(tc.filename, func(t *testing.T) {
			if got := IsImage(tc.filename); got != tc.want {
				t.Errorf("IsImage(%q) = %v, want %v", tc.filename, got, tc.want)
			}
		})
	}
}

func TestIsMedia(t *testing.T) {
	tests := []struct {
		filename string
		want     bool
	}{
		{"test.jpg", true},
		{"test.png", true},
		{"test.MP4", true},
		{"__MACOSX/._test.jpg", false},
		{"._test.jpg", false},
		{"test.txt", false},
	}

	for _, tc := range tests {
		t.Run(tc.filename, func(t *testing.T) {
			if got := IsMedia(tc.filename); got != tc.want {
				t.Errorf("IsMedia(%q) = %v, want %v", tc.filename, got, tc.want)
			}
		})
	}
}

func TestFindCaptureDate_JSONFallback(t *testing.T) {
	// Create an in-memory zip file
	buf := new(bytes.Buffer)
	w := zip.NewWriter(buf)

	// Add media file (we only need the directory entry for this test, not the content)
	_, err := w.Create("test.mp4")
	if err != nil {
		t.Fatalf("Failed to create mock file in zip: %v", err)
	}

	// Add JSON sidecar
	jsonContent := `{
  "title": "test.mp4",
  "photoTakenTime": {
    "timestamp": "1672531200", 
    "formatted": "Jan 1, 2023, 12:00:00 AM UTC"
  }
}`
	f, err := w.Create("test.mp4.json")
	if err != nil {
		t.Fatalf("Failed to create mock json in zip: %v", err)
	}
	_, err = f.Write([]byte(jsonContent))
	if err != nil {
		t.Fatalf("Failed to write mock json in zip: %v", err)
	}

	err = w.Close()
	if err != nil {
		t.Fatalf("Failed to close zip writer: %v", err)
	}

	// Open the in-memory zip
	r, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("Failed to open zip reader: %v", err)
	}

	zipIndex := make(map[string]*zip.File)
	var mediaFile *zip.File
	for _, f := range r.File {
		zipIndex[f.Name] = f
		if f.Name == "test.mp4" {
			mediaFile = f
		}
	}

	if mediaFile == nil {
		t.Fatalf("Media file not found in in-memory zip")
	}

	zmf := &ZipMediaFile{
		f:        mediaFile,
		zipIndex: zipIndex,
	}

	captureDate, origin := FindCaptureDate(zmf)
	if origin != "json_taken" {
		t.Errorf("Expected origin 'json_taken', got %q", origin)
	}

	// 1672531200 is 1970-01-20 or something? No, it's 2023-01-01 00:00:00 UTC
	expectedTime := time.Unix(1672531200, 0)
	if !captureDate.Equal(expectedTime) {
		t.Errorf("Expected time %v, got %v", expectedTime, captureDate)
	}
}

func TestExtractAlbumName(t *testing.T) {
	tests := []struct {
		path     string
		expected string
	}{
		{"Takeout/Google Photos/Hawaii Trip 2023/IMG_1234.jpg", "Hawaii Trip 2023"},
		{"Takeout/Google Photos/Photos from 2023/IMG_1234.jpg", ""},
		{"Takeout/Google Photos/2023-01-01/IMG_1234.jpg", ""},
		{"Takeout/Google Photos/Untitled Album/video.mp4", "Untitled Album"},
		{"SomeOtherFolder/IMG_1234.jpg", ""},
		{"Google Photos/Album at Root/file.png", "Album at Root"},
	}

	for _, tt := range tests {
		result := ExtractAlbumName(tt.path)
		if result != tt.expected {
			t.Errorf("ExtractAlbumName(%q) = %q; want %q", tt.path, result, tt.expected)
		}
	}
}
