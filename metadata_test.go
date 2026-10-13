package main

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"io"
	"os"
	"path/filepath"
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

func TestIsMP4Video(t *testing.T) {
	tests := []struct {
		filename string
		want     bool
	}{
		{"video.mp4", true},
		{"video.MP4", true},
		{"video.mov", true},
		{"video.MOV", true},
		{"image.jpg", false},
		{"video.avi", false},
	}

	for _, tc := range tests {
		t.Run(tc.filename, func(t *testing.T) {
			if got := IsMP4Video(tc.filename); got != tc.want {
				t.Errorf("IsMP4Video(%q) = %v, want %v", tc.filename, got, tc.want)
			}
		})
	}
}

type mockMedia struct {
	content []byte
}

func (m *mockMedia) Name() string { return "test.mp4" }
func (m *mockMedia) Open() (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(m.content)), nil
}
func (m *mockMedia) OpenSidecar(names []string) (io.ReadCloser, bool) {
	return nil, false
}

func TestGetMediaDuration(t *testing.T) {
	// Build a mock mp4 file
	// mvhd box (v0): size 28
	// 4 bytes size, 4 bytes "mvhd"
	// 1 byte version (0), 3 bytes flags (0)
	// 4 bytes creation_time, 4 bytes modification_time
	// 4 bytes timescale, 4 bytes duration

	mvhdBody := make([]byte, 20)
	mvhdBody[0] = 0                                   // version 0
	binary.BigEndian.PutUint32(mvhdBody[12:16], 1000) // timescale 1000
	binary.BigEndian.PutUint32(mvhdBody[16:20], 2500) // duration 2500 -> 2.5s

	mvhdBox := make([]byte, 28)
	binary.BigEndian.PutUint32(mvhdBox[0:4], 28)
	copy(mvhdBox[4:8], "mvhd")
	copy(mvhdBox[8:], mvhdBody)

	moovBox := make([]byte, 8+len(mvhdBox))
	binary.BigEndian.PutUint32(moovBox[0:4], uint32(len(moovBox)))
	copy(moovBox[4:8], "moov")
	copy(moovBox[8:], mvhdBox)

	// Prepend a dummy ftyp box to ensure we skip correctly
	ftypBox := make([]byte, 16)
	binary.BigEndian.PutUint32(ftypBox[0:4], 16)
	copy(ftypBox[4:8], "ftyp")
	copy(ftypBox[8:], "isom\x00\x00\x02\x00")

	fullContent := append(ftypBox, moovBox...)

	media := &mockMedia{content: fullContent}

	duration, err := GetMediaDuration(media)
	if err != nil {
		t.Fatalf("GetMediaDuration failed: %v", err)
	}

	expected := 2500 * time.Millisecond
	if duration != expected {
		t.Errorf("Expected duration %v, got %v", expected, duration)
	}
}

func mp4Box(boxType string, body []byte) []byte {
	b := make([]byte, 8+len(body))
	binary.BigEndian.PutUint32(b[0:4], uint32(len(b)))
	copy(b[4:8], boxType)
	copy(b[8:], body)
	return b
}

func TestGetMediaDuration_Hardening(t *testing.T) {
	mvhdV1 := make([]byte, 32)
	mvhdV1[0] = 1
	binary.BigEndian.PutUint32(mvhdV1[20:24], 1000)
	binary.BigEndian.PutUint64(mvhdV1[24:32], 5500)

	mvhdV1Huge := make([]byte, 32)
	mvhdV1Huge[0] = 1
	binary.BigEndian.PutUint32(mvhdV1Huge[20:24], 1)
	binary.BigEndian.PutUint64(mvhdV1Huge[24:32], 1<<62)

	hdr := func(size uint32, boxType string) []byte {
		b := make([]byte, 8)
		binary.BigEndian.PutUint32(b[0:4], size)
		copy(b[4:8], boxType)
		return b
	}
	ext := func(size uint64, boxType string) []byte {
		b := hdr(1, boxType)
		e := make([]byte, 8)
		binary.BigEndian.PutUint64(e, size)
		return append(b, e...)
	}

	tests := []struct {
		name    string
		content []byte
		want    time.Duration
		wantErr bool
	}{
		{"v1 mvhd", mp4Box("moov", mp4Box("mvhd", mvhdV1)), 5500 * time.Millisecond, false},
		{"v1 mvhd too short for duration", mp4Box("moov", mp4Box("mvhd", mvhdV1[:28])), 0, true},
		{"v1 implausible duration", mp4Box("moov", mp4Box("mvhd", mvhdV1Huge)), 0, true},
		{"empty mvhd body", mp4Box("moov", mp4Box("mvhd", nil)), 0, true},
		{"box size smaller than header", append(hdr(4, "free"), make([]byte, 16)...), 0, true},
		{"extended size smaller than header", ext(3, "free"), 0, true},
		{"extended size has top bit set", ext(1<<63, "free"), 0, true},
		{"mvhd claims huge size", append(hdr(0xFFFFFFF0, "mvhd"), make([]byte, 40)...), 0, true},
		{"skip past end of data", append(hdr(0xFFFFFFF0, "free"), make([]byte, 16)...), 0, true},
		{"unbounded non-moov box", hdr(0, "mdat"), 0, true},
		{"unbounded moov box", append(hdr(0, "moov"), mp4Box("mvhd", mvhdV1)...), 5500 * time.Millisecond, false},
		{"truncated", mp4Box("moov", nil)[:6], 0, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := GetMediaDuration(&mockMedia{content: tc.content})
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if got != tc.want {
				t.Errorf("duration = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestGetMediaDuration_SeekableSkip(t *testing.T) {
	mvhd := make([]byte, 20)
	binary.BigEndian.PutUint32(mvhd[12:16], 1000)
	binary.BigEndian.PutUint32(mvhd[16:20], 2000)
	content := append(mp4Box("mdat", make([]byte, 4096)), mp4Box("moov", mp4Box("mvhd", mvhd))...)

	path := filepath.Join(t.TempDir(), "v.mp4")
	if err := os.WriteFile(path, content, 0644); err != nil {
		t.Fatal(err)
	}
	got, err := GetMediaDuration(&LocalMediaFile{path: path})
	if err != nil || got != 2*time.Second {
		t.Errorf("got %v, %v; want 2s", got, err)
	}

	// mdat claiming to extend past EOF on a real file must error, not hang or succeed.
	bad := append(hdr32(0xFFFFFFF0, "mdat"), make([]byte, 16)...)
	os.WriteFile(path, bad, 0644)
	if _, err := GetMediaDuration(&LocalMediaFile{path: path}); err == nil {
		t.Error("expected error for box extending past EOF")
	}
}

func hdr32(size uint32, boxType string) []byte {
	b := make([]byte, 8)
	binary.BigEndian.PutUint32(b[0:4], size)
	copy(b[4:8], boxType)
	return b
}
