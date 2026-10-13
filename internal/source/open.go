package source

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"photosorter/internal/media"
	"photosorter/internal/takeout"
)

// Inputs describes everything to import in one run.
type Inputs struct {
	// Folders are directory trees to import, such as extracted Takeout exports.
	Folders []string

	// Zips are zip archives to import. Each entry is a path, or a glob pattern such as
	// "takeout-*.zip". A path that exists is taken literally even if it contains glob
	// characters.
	Zips []string

	// Library is the output folder. No folder input may contain it or lie inside it.
	Library string

	// Filter decides which files are media.
	Filter media.Filter
}

// Open opens every input, sharing one sidecar index between them. On error, any
// sources already opened are closed and nil is returned.
func Open(in Inputs) (sources []Source, err error) {
	defer func() {
		if err != nil {
			CloseAll(sources)
			sources = nil
		}
	}()
	sidecars := takeout.NewSidecarIndex()

	for _, dir := range in.Folders {
		if pathsOverlap(dir, in.Library) {
			return sources, fmt.Errorf("input folder %s and library %s must not contain one another", dir, in.Library)
		}
		src, err := OpenFolder(dir, in.Filter, sidecars)
		if err != nil {
			return sources, fmt.Errorf("cannot read input folder: %w", err)
		}
		sources = append(sources, src)
	}

	for _, pattern := range in.Zips {
		paths, err := expandZipPattern(pattern)
		if err != nil {
			return sources, err
		}
		for _, p := range paths {
			src, err := OpenZip(p, in.Filter, sidecars)
			if err != nil {
				return sources, fmt.Errorf("cannot open zip %s: %w", p, err)
			}
			sources = append(sources, src)
		}
	}
	return sources, nil
}

// expandZipPattern turns a -zip argument into the paths it names.
func expandZipPattern(pattern string) ([]string, error) {
	if _, err := os.Stat(pattern); err == nil || !strings.ContainsAny(pattern, "*?[") {
		return []string{pattern}, nil
	}
	matches, err := filepath.Glob(pattern)
	if err != nil || len(matches) == 0 {
		return nil, fmt.Errorf("no zip files match %s", pattern)
	}
	return matches, nil
}
