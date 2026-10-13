package takeout

import (
	"fmt"
	"io"
	"strings"
	"testing"
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

func TestIsSidecar(t *testing.T) {
	for name, want := range map[string]bool{"a.jpg.json": true, "a.JSON": true, "a.jpg": false, "json": false} {
		if got := IsSidecar(name); got != want {
			t.Errorf("IsSidecar(%q) = %v, want %v", name, got, want)
		}
	}
}
