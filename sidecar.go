package main

import (
	"io"
	"path"
	"regexp"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"
)

const (
	supplementalSuffix = ".supplemental-metadata"

	// Takeout truncates sidecar names so they fit in 51 characters including ".json".
	sidecarStemLimit = 46

	// A truncated stem must be at least this long to be accepted as a prefix match,
	// which keeps short names from matching unrelated sidecars.
	minTruncatedStem = 40
)

// editedSuffixes are the (localized) markers Google adds to edited copies, e.g.
// IMG_1234-edited.jpg. Edited copies usually have no sidecar of their own.
var editedSuffixes = []string{"-edited", "-bearbeitet", "-modifié", "-modificato", "-editado"}

var dupSuffixRegex = regexp.MustCompile(`\(\d+\)$`)

// SidecarIndex maps directories to the Takeout metadata (.json) files inside them. It is
// shared by every input source, so a sidecar in one zip part can describe media in another.
// It must be fully populated before concurrent Find calls begin.
type SidecarIndex struct {
	byDir map[string]*sidecarDir
}

type sidecarDir struct {
	open map[string]func() (io.ReadCloser, error)

	mu    sync.Mutex
	names []string // sorted, built lazily for prefix matching
}

func NewSidecarIndex() *SidecarIndex {
	return &SidecarIndex{byDir: make(map[string]*sidecarDir)}
}

// Add registers a sidecar. p must use forward slashes.
func (x *SidecarIndex) Add(p string, open func() (io.ReadCloser, error)) {
	dir, name := path.Split(p)
	d := x.byDir[dir]
	if d == nil {
		d = &sidecarDir{open: make(map[string]func() (io.ReadCloser, error))}
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
	ext := path.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	dup := ""
	if m := dupSuffixRegex.FindString(stem); m != "" {
		dup = m
		stem = strings.TrimSuffix(stem, m)
	}
	for _, s := range editedSuffixes {
		if strings.HasSuffix(stem, s) {
			variants = append(variants, strings.TrimSuffix(stem, s)+dup+ext)
			break
		}
	}
	return variants
}

// splitDup separates Takeout's duplicate counter: "IMG_1(1).jpg" -> ("IMG_1.jpg", "(1)").
func splitDup(name string) (base, dup string) {
	ext := path.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	if m := dupSuffixRegex.FindString(stem); m != "" {
		return strings.TrimSuffix(stem, m) + ext, m
	}
	return name, ""
}

// sidecarCandidates lists the exact sidecar file names Takeout may use for a media file name.
func sidecarCandidates(name string) []string {
	base, dup := splitDup(name)
	stem := strings.TrimSuffix(base, path.Ext(base))

	cands := []string{
		base + dup + ".json",
		base + supplementalSuffix + dup + ".json",
		truncateRunes(base+supplementalSuffix, sidecarStemLimit) + dup + ".json",
		truncateRunes(base, sidecarStemLimit) + dup + ".json",
	}
	if dup == "" {
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

// findTruncated looks for a sidecar whose name is a truncated form of one of the names
// Takeout would have generated for the media file. It requires the same duplicate counter
// and a long shared prefix, and prefers the longest match.
func findTruncated(names []string, name string) (string, bool) {
	base, dup := splitDup(name)
	if utf8.RuneCountInString(base+supplementalSuffix) <= sidecarStemLimit {
		return "", false // short enough that no truncation would have happened
	}
	full := base + supplementalSuffix

	best, bestLen := "", 0
	for _, sn := range names {
		if !strings.HasSuffix(sn, ".json") {
			continue
		}
		stem := strings.TrimSuffix(sn, ".json")
		sDup := ""
		if m := dupSuffixRegex.FindString(stem); m != "" {
			sDup = m
			stem = strings.TrimSuffix(stem, m)
		}
		if sDup != dup {
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
