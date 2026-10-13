package source

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"photosorter/internal/media"
	"photosorter/internal/takeout"
)

// folderSource reads media from a directory tree, such as an extracted Takeout.
type folderSource struct {
	root     string
	files    []File
	warnings []string
}

// OpenFolder walks the tree at root. Media accepted by filter become its files, every
// .json file is registered in sidecars, and unreadable paths are kept as warnings.
func OpenFolder(root string, filter media.Filter, sidecars *takeout.SidecarIndex) (Source, error) {
	if _, err := os.Stat(root); err != nil {
		return nil, err
	}
	s := &folderSource{root: root}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		switch {
		case err != nil:
			s.warnings = append(s.warnings, fmt.Sprintf("%s: %v", p, err))
		case d.IsDir():
		case takeout.IsSidecar(d.Name()):
			sidecars.Add(filepath.ToSlash(p), func() (io.ReadCloser, error) { return os.Open(p) })
		case filter.MatchIn(root, p):
			s.files = append(s.files, &folderFile{path: p, sidecars: sidecars})
		}
		return nil // keep going past unreadable paths
	})
	if err != nil {
		return nil, err
	}
	return s, nil
}

func (s *folderSource) Name() string       { return filepath.Base(s.root) }
func (s *folderSource) Files() []File      { return s.files }
func (s *folderSource) Warnings() []string { return s.warnings }
func (s *folderSource) Close() error       { return nil }

// folderFile is one media file on disk.
type folderFile struct {
	path     string
	sidecars *takeout.SidecarIndex
}

func (f *folderFile) Name() string                 { return f.path }
func (f *folderFile) Open() (io.ReadCloser, error) { return os.Open(f.path) }

func (f *folderFile) Size() int64 {
	if info, err := os.Stat(f.path); err == nil {
		return info.Size()
	}
	return -1
}

func (f *folderFile) OpenSidecar() (io.ReadCloser, bool) {
	return f.sidecars.Find(filepath.ToSlash(f.path))
}
