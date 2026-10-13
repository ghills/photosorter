package takeout

import (
	"io"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"
)

// How Takeout names sidecars
//
// For a media file "IMG_1234.jpg" the sidecar may be called any of:
//
//	IMG_1234.jpg.json                         older exports
//	IMG_1234.jpg.supplemental-metadata.json   newer exports
//	IMG_1234.json                             named after the stem
//	IMG_1234.jpg.supplem.json                 truncated to 51 characters total
//
// Duplicate names get a counter, which Takeout moves to the end of the sidecar name:
// "IMG_1234(1).jpg" is described by "IMG_1234.jpg(1).json" (or the supplemental and
// truncated forms with "(1)" before ".json").
//
// Edited copies ("IMG_1234-edited.jpg", with localized variants) usually have no
// sidecar of their own and share the original's.

const (
	supplementalSuffix = ".supplemental-metadata"

	// Takeout truncates sidecar names so they fit in 51 characters including ".json".
	sidecarStemLimit = 46

	// A truncated stem must be at least this long to be accepted as a prefix match,
	// which keeps short names from matching unrelated sidecars.
	minTruncatedStem = 40
)

// editedSuffixes are the (localized) markers Google adds to edited copies.
var editedSuffixes = []string{"-edited", "-bearbeitet", "-modifié", "-modificato", "-editado"}

// counterSuffix matches Takeout's duplicate counter, e.g. the "(1)" in "IMG_1(1)".
var counterSuffix = regexp.MustCompile(`\(\d+\)$`)

// Opener opens the content of a sidecar file.
type Opener func() (io.ReadCloser, error)

// IsSidecar reports whether name looks like a Takeout metadata file.
func IsSidecar(name string) bool {
	return strings.EqualFold(filepath.Ext(name), ".json")
}

// SidecarIndex maps directories to the sidecars inside them.
//
// Populate it with [SidecarIndex.Add] while scanning sources, then look sidecars up
// with [SidecarIndex.Find]. All Add calls must finish before concurrent Find calls
// begin; Find itself is safe for concurrent use.
type SidecarIndex struct {
	byDir map[string]*sidecarDir
}

type sidecarDir struct {
	open map[string]Opener

	mu    sync.Mutex
	names []string // sorted keys of open, built lazily for prefix matching
}

// NewSidecarIndex returns an empty index.
func NewSidecarIndex() *SidecarIndex {
	return &SidecarIndex{byDir: make(map[string]*sidecarDir)}
}

// Add registers the sidecar at p (forward slashes). If the same path is added twice,
// the first registration wins.
func (x *SidecarIndex) Add(p string, open Opener) {
	dir, name := path.Split(p)
	d := x.byDir[dir]
	if d == nil {
		d = &sidecarDir{open: make(map[string]Opener)}
		x.byDir[dir] = d
	}
	if _, exists := d.open[name]; !exists {
		d.open[name] = open
		d.mu.Lock()
		d.names = nil
		d.mu.Unlock()
	}
}

// Find opens the sidecar describing the media file at p (forward slashes), if any.
// The sidecar must be in the same directory as the media file.
func (x *SidecarIndex) Find(p string) (io.ReadCloser, bool) {
	if x == nil {
		return nil, false
	}
	dir, name := path.Split(p)
	d := x.byDir[dir]
	if d == nil {
		return nil, false
	}

	variants := mediaNameVariants(name)

	// Exact names first: cheap map lookups.
	for _, v := range variants {
		for _, cand := range sidecarCandidates(v) {
			if open, ok := d.open[cand]; ok {
				if rc, err := open(); err == nil {
					return rc, true
				}
			}
		}
	}

	// Then truncated names, which can't be predicted exactly.
	names := d.sortedNames()
	for _, v := range variants {
		if match, ok := findTruncated(names, v); ok {
			if rc, err := d.open[match](); err == nil {
				return rc, true
			}
		}
	}
	return nil, false
}

func (d *sidecarDir) sortedNames() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.names == nil {
		d.names = make([]string, 0, len(d.open))
		for n := range d.open {
			d.names = append(d.names, n)
		}
		sort.Strings(d.names)
	}
	return d.names
}

// mediaNameVariants returns the names whose sidecar may describe name: the name itself,
// then, for edited copies, the original it was derived from.
func mediaNameVariants(name string) []string {
	variants := []string{name}
	base, counter := splitCounter(name)
	ext := path.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	for _, s := range editedSuffixes {
		if strings.HasSuffix(stem, s) {
			variants = append(variants, strings.TrimSuffix(stem, s)+counter+ext)
			break
		}
	}
	return variants
}

// splitCounter separates Takeout's duplicate counter from a media file name:
// "IMG_1(1).jpg" -> ("IMG_1.jpg", "(1)").
func splitCounter(name string) (base, counter string) {
	ext := path.Ext(name)
	stem, counter := cutCounter(strings.TrimSuffix(name, ext))
	return stem + ext, counter
}

// cutCounter removes a trailing duplicate counter from s: "IMG_1(1)" -> ("IMG_1", "(1)").
func cutCounter(s string) (rest, counter string) {
	if m := counterSuffix.FindString(s); m != "" {
		return strings.TrimSuffix(s, m), m
	}
	return s, ""
}

// sidecarCandidates lists the exact sidecar file names Takeout may use for a media file name.
func sidecarCandidates(name string) []string {
	base, counter := splitCounter(name)
	stem := strings.TrimSuffix(base, path.Ext(base))

	cands := []string{
		base + counter + ".json",
		base + supplementalSuffix + counter + ".json",
		truncateRunes(base+supplementalSuffix, sidecarStemLimit) + counter + ".json",
		truncateRunes(base, sidecarStemLimit) + counter + ".json",
	}
	if counter == "" {
		// Sidecars named after the media stem rather than the full file name.
		cands = append(cands, stem+".json", stem+supplementalSuffix+".json")
	}

	seen := make(map[string]bool, len(cands))
	out := cands[:0]
	for _, c := range cands {
		if !seen[c] {
			seen[c] = true
			out = append(out, c)
		}
	}
	return out
}

// findTruncated looks for a sidecar whose name is a truncated form of the name Takeout
// would have generated for the media file. It requires the same duplicate counter and
// a long shared prefix, and prefers the longest match.
func findTruncated(names []string, name string) (string, bool) {
	base, counter := splitCounter(name)
	full := base + supplementalSuffix
	if utf8.RuneCountInString(full) <= sidecarStemLimit {
		return "", false // short enough that no truncation would have happened
	}

	best, bestLen := "", 0
	for _, sn := range names {
		if !strings.HasSuffix(sn, ".json") {
			continue
		}
		stem, sCounter := cutCounter(strings.TrimSuffix(sn, ".json"))
		if sCounter != counter {
			continue
		}
		n := utf8.RuneCountInString(stem)
		if n < minTruncatedStem || n <= bestLen {
			continue
		}
		if strings.HasPrefix(full, stem) { // also covers stems that cut into the media name itself
			best, bestLen = sn, n
		}
	}
	return best, best != ""
}

func truncateRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}
