package media

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

// DefaultExtensions are the file extensions imported when no filter is configured.
var DefaultExtensions = []string{
	// Images
	".jpg", ".jpeg", ".png", ".gif", ".webp", ".heic", ".bmp", ".tiff", ".tif",
	// Videos
	".mp4", ".mov", ".avi", ".mkv", ".webm", ".mts", ".3gp", ".m4v",
}

// exifExtensions are formats whose EXIF header goexif can decode.
var exifExtensions = map[string]bool{".jpg": true, ".jpeg": true, ".tiff": true, ".tif": true, ".heic": true}

// mp4Extensions are ISO base media (MP4/QuickTime) containers whose duration MP4Duration can read.
var mp4Extensions = map[string]bool{".mp4": true, ".mov": true}

// Filter decides which file names are media. The zero value matches nothing; use
// [DefaultFilter] or [NewFilter].
type Filter struct {
	exts map[string]bool
}

// DefaultFilter matches [DefaultExtensions].
func DefaultFilter() Filter {
	f, _ := NewFilter(nil, nil) // the built-in list is always valid
	return f
}

// NewFilter matches [DefaultExtensions] plus include, minus exclude. Extensions are
// case-insensitive and the leading dot is optional ("dng" and ".DNG" are the same).
func NewFilter(include, exclude []string) (Filter, error) {
	exts := make(map[string]bool, len(DefaultExtensions)+len(include))
	for _, e := range DefaultExtensions {
		exts[e] = true
	}
	for _, e := range include {
		n, err := NormalizeExt(e)
		if err != nil {
			return Filter{}, err
		}
		exts[n] = true
	}
	for _, e := range exclude {
		n, err := NormalizeExt(e)
		if err != nil {
			return Filter{}, err
		}
		delete(exts, n)
	}
	return Filter{exts: exts}, nil
}

// NormalizeExt lower-cases ext and gives it a leading dot. It rejects values that
// cannot be a file extension.
func NormalizeExt(ext string) (string, error) {
	e := strings.ToLower(strings.TrimSpace(ext))
	if !strings.HasPrefix(e, ".") {
		e = "." + e
	}
	if e == "." || strings.ContainsAny(e[1:], `./\`) {
		return "", fmt.Errorf("invalid file extension %q", ext)
	}
	return e, nil
}

// Extensions lists the matched extensions in sorted order.
func (f Filter) Extensions() []string {
	out := make([]string, 0, len(f.exts))
	for e := range f.exts {
		out = append(out, e)
	}
	sort.Strings(out)
	return out
}

// Match reports whether the file at name should be treated as media. name may be a
// bare file name or a slash- or OS-separated relative path. macOS metadata is always
// rejected: AppleDouble "._" files and anything inside a "__MACOSX" folder.
//
// Pass paths relative to the folder being scanned (see [Filter.MatchIn]) so that the
// location of that folder itself cannot affect the result.
func (f Filter) Match(name string) bool {
	if strings.HasPrefix(filepath.Base(name), "._") {
		return false
	}
	for _, part := range strings.Split(filepath.ToSlash(name), "/") {
		if part == "__MACOSX" {
			return false
		}
	}
	return f.exts[strings.ToLower(filepath.Ext(name))]
}

// MatchIn is [Filter.Match] for a path found while walking root: only the part of
// path below root is considered.
func (f Filter) MatchIn(root, path string) bool {
	if rel, err := filepath.Rel(root, path); err == nil {
		path = rel
	}
	return f.Match(path)
}

// HasEXIF reports whether name is a format [EXIFDate] can read.
func HasEXIF(name string) bool {
	return exifExtensions[strings.ToLower(filepath.Ext(name))]
}

// HasMP4Duration reports whether name is a container [MP4Duration] can read.
func HasMP4Duration(name string) bool {
	return mp4Extensions[strings.ToLower(filepath.Ext(name))]
}
