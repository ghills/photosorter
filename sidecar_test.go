package main

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// indexOf builds an index whose sidecars read back as their own path.
func indexOf(paths ...string) *SidecarIndex {
	idx := NewSidecarIndex()
	for _, p := range paths {
		p := p
		idx.Add(p, func() (io.ReadCloser, error) { return io.NopCloser(strings.NewReader(p)), nil })
	}
	return idx
}

func findContent(idx *SidecarIndex, media string) (string, bool) {
	rc, ok := idx.Find(media)
	if !ok {
		return "", false
	}
	defer rc.Close()
	b, _ := io.ReadAll(rc)
	return string(b), true
}

func TestSidecarIndex_Find(t *testing.T) {
	long := "IMG_20190427_173449_something_long.jpg" // 38 chars: sidecar name gets truncated
	veryLong := strings.Repeat("a", 47) + ".jpg"     // media name itself exceeds the limit

	tests := []struct {
		name    string
		sidecar []string
		media   string
		want    string // sidecar path expected, "" for none
	}{
		{"legacy name", []string{"d/IMG_1.jpg.json"}, "d/IMG_1.jpg", "d/IMG_1.jpg.json"},
		{"supplemental name", []string{"d/IMG_1.jpg.supplemental-metadata.json"}, "d/IMG_1.jpg", "d/IMG_1.jpg.supplemental-metadata.json"},
		{"stem name", []string{"d/IMG_1.json"}, "d/IMG_1.jpg", "d/IMG_1.json"},
		{"case-sensitive dir only", []string{"other/IMG_1.jpg.json"}, "d/IMG_1.jpg", ""},

		{"truncated supplemental", []string{"d/" + long + ".supplem.json"}, "d/" + long, "d/" + long + ".supplem.json"},
		{"truncated media name", []string{"d/" + veryLong[:46] + ".json"}, "d/" + veryLong, "d/" + veryLong[:46] + ".json"},

		{"duplicate counter legacy", []string{"d/IMG_1234.jpg(1).json"}, "d/IMG_1234(1).jpg", "d/IMG_1234.jpg(1).json"},
		{"duplicate counter supplemental", []string{"d/IMG_1234.jpg.supplemental-metadata(2).json"}, "d/IMG_1234(2).jpg", "d/IMG_1234.jpg.supplemental-metadata(2).json"},
		{"duplicate counter truncated", []string{"d/IMG_20190427_173449_something_long.jpg.supplem(1).json"}, "d/IMG_20190427_173449_something_long(1).jpg", "d/IMG_20190427_173449_something_long.jpg.supplem(1).json"},
		{"plain media must not take duplicate's sidecar", []string{"d/IMG_1234.jpg(1).json"}, "d/IMG_1234.jpg", ""},
		{"duplicate must not take the original's sidecar", []string{"d/IMG_1234.jpg.json"}, "d/IMG_1234(1).jpg", ""},

		{"edited uses original's sidecar", []string{"d/IMG_1234.jpg.supplemental-metadata.json"}, "d/IMG_1234-edited.jpg", "d/IMG_1234.jpg.supplemental-metadata.json"},
		{"edited prefers its own sidecar", []string{"d/IMG_1234.jpg.json", "d/IMG_1234-edited.jpg.json"}, "d/IMG_1234-edited.jpg", "d/IMG_1234-edited.jpg.json"},
		{"localized edited suffix", []string{"d/IMG_1234.jpg.json"}, "d/IMG_1234-bearbeitet.jpg", "d/IMG_1234.jpg.json"},

		{"similar names do not match", []string{"d/IMG_10.jpg.json"}, "d/IMG_1.jpg", ""},
		{"short prefix is not accepted as truncation", []string{"d/IMG_1.jp.json"}, "d/" + long, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := findContent(indexOf(tc.sidecar...), tc.media)
			if tc.want == "" {
				if ok {
					t.Fatalf("unexpected match %q", got)
				}
				return
			}
			if !ok || got != tc.want {
				t.Fatalf("got %q (found=%v), want %q", got, ok, tc.want)
			}
		})
	}
}

// With several truncated candidates, the longest shared prefix wins deterministically.
func TestSidecarIndex_TruncatedPrefersLongest(t *testing.T) {
	media := "d/IMG_20190427_173449_something_long.jpg"
	idx := indexOf(
		"d/IMG_20190427_173449_something_long.jpg.supp.json",
		"d/IMG_20190427_173449_something_long.jpg.supplem.json",
	)
	got, ok := findContent(idx, media)
	if !ok || got != "d/IMG_20190427_173449_something_long.jpg.supplem.json" {
		t.Errorf("got %q, found=%v", got, ok)
	}
}

func writeZip(t *testing.T, path string, files map[string]string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for name, content := range files {
		w, _ := zw.Create(name)
		w.Write([]byte(content))
	}
	zw.Close()
	f.Close()
}

// A sidecar that landed in a different Takeout part than its media must still be used.
func TestSidecarIndex_AcrossZipParts(t *testing.T) {
	dir := t.TempDir()
	part1 := filepath.Join(dir, "takeout-001.zip")
	part2 := filepath.Join(dir, "takeout-002.zip")
	album := "Takeout/Google Photos/Trip/"
	writeZip(t, part1, map[string]string{album + "DSC0001.jpg": "photo bytes"})
	writeZip(t, part2, map[string]string{
		album + "DSC0001.jpg.supplemental-metadata.json": `{"photoTakenTime":{"timestamp":"1672531200"}}`,
	})

	idx := NewSidecarIndex()
	s1, err := NewZipSource(part1, idx)
	if err != nil {
		t.Fatal(err)
	}
	defer s1.Close()
	s2, err := NewZipSource(part2, idx)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()

	files, _ := s1.MediaFiles()
	if len(files) != 1 {
		t.Fatalf("got %d media files", len(files))
	}
	got, origin := FindCaptureDate(files[0])
	if origin != "json_taken" || !got.Equal(time.Unix(1672531200, 0)) {
		t.Errorf("FindCaptureDate = %v, %q; want json_taken for the sidecar in the other zip", got, origin)
	}
}

func TestSidecarIndex_LocalFolder(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "IMG_1(1).jpg"), []byte("x"), 0644)
	os.WriteFile(filepath.Join(dir, "IMG_1.jpg(1).json"), []byte(`{"photoTakenTime":{"timestamp":"1672531200"}}`), 0644)

	src := NewLocalFolderSource(dir, nil)
	files, err := src.MediaFiles()
	if err != nil || len(files) != 1 {
		t.Fatalf("MediaFiles = %d files, %v", len(files), err)
	}
	if _, origin := FindCaptureDate(files[0]); origin != "json_taken" {
		t.Errorf("origin = %q, want json_taken", origin)
	}
}

func TestSidecarIndex_ConcurrentFind(t *testing.T) {
	var paths []string
	for i := 0; i < 50; i++ {
		paths = append(paths, fmt.Sprintf("d/IMG_20190427_173449_something_long_%02d.jpg.supplem.json", i))
	}
	idx := indexOf(paths...)
	done := make(chan struct{})
	for g := 0; g < 8; g++ {
		go func() {
			defer func() { done <- struct{}{} }()
			for i := 0; i < 50; i++ {
				if _, ok := findContent(idx, fmt.Sprintf("d/IMG_20190427_173449_something_long_%02d.jpg", i)); !ok {
					t.Errorf("missing sidecar %d", i)
				}
			}
		}()
	}
	for g := 0; g < 8; g++ {
		<-done
	}
}
