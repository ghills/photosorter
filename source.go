package main

import (
	"archive/zip"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// MediaFile represents an abstract file containing media data,
// decoupled from its original source (zip archive, local dir, etc.).
type MediaFile interface {
	Name() string // Name of the file, relative to source or absolute
	Open() (io.ReadCloser, error)
	OpenSidecar() (io.ReadCloser, bool) // Takeout metadata JSON describing this file, if any
}

// InputSource implies a place where media files reside.
type InputSource interface {
	Name() string
	MediaFiles() ([]MediaFile, error)
	Close() error
}

// --- ZipSource Implementation ---

// ZipSource reads media from a Takeout zip. Every .json entry is registered in the shared
// SidecarIndex up front, so media can find sidecars that landed in a different zip part.
type ZipSource struct {
	path     string
	r        *zip.ReadCloser
	sidecars *SidecarIndex
}

// NewZipSource opens a zip and registers its sidecars in idx (a private index is used if nil).
func NewZipSource(path string, idx *SidecarIndex) (*ZipSource, error) {
	r, err := zip.OpenReader(path)
	if err != nil {
		return nil, err
	}
	if idx == nil {
		idx = NewSidecarIndex()
	}
	for _, f := range r.File {
		if !f.FileInfo().IsDir() && isSidecarFile(f.Name) {
			f := f
			idx.Add(f.Name, f.Open)
		}
	}
	return &ZipSource{path: path, r: r, sidecars: idx}, nil
}

func (z *ZipSource) Name() string {
	return filepath.Base(z.path)
}

func (z *ZipSource) MediaFiles() ([]MediaFile, error) {
	var mediaFiles []MediaFile
	for _, f := range z.r.File {
		if !f.FileInfo().IsDir() && IsMedia(f.Name) {
			mediaFiles = append(mediaFiles, &ZipMediaFile{f: f, sidecars: z.sidecars})
		}
	}
	return mediaFiles, nil
}

func (z *ZipSource) Close() error {
	if z.r != nil {
		return z.r.Close()
	}
	return nil
}

type ZipMediaFile struct {
	f        *zip.File
	sidecars *SidecarIndex
}

func (z *ZipMediaFile) Name() string {
	return z.f.Name
}

func (z *ZipMediaFile) Open() (io.ReadCloser, error) {
	return z.f.Open()
}

func (z *ZipMediaFile) OpenSidecar() (io.ReadCloser, bool) {
	return z.sidecars.Find(z.f.Name)
}

// --- LocalFolderSource Implementation ---

type LocalFolderSource struct {
	path     string
	sidecars *SidecarIndex
}

// NewLocalFolderSource reads media from a directory tree. Sidecars found during the walk are
// registered in idx (a private index is used if nil).
func NewLocalFolderSource(path string, idx *SidecarIndex) *LocalFolderSource {
	if idx == nil {
		idx = NewSidecarIndex()
	}
	return &LocalFolderSource{path: path, sidecars: idx}
}

func (l *LocalFolderSource) Name() string {
	return filepath.Base(l.path)
}

func (l *LocalFolderSource) MediaFiles() ([]MediaFile, error) {
	var mediaFiles []MediaFile
	err := filepath.WalkDir(l.path, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // Skip paths we can't access
		}
		if d.IsDir() {
			return nil
		}
		if isSidecarFile(d.Name()) {
			p := p
			l.sidecars.Add(filepath.ToSlash(p), func() (io.ReadCloser, error) { return os.Open(p) })
		} else if IsMedia(d.Name()) {
			mediaFiles = append(mediaFiles, &LocalMediaFile{path: p, sidecars: l.sidecars})
		}
		return nil
	})
	return mediaFiles, err
}

func (l *LocalFolderSource) Close() error {
	// Nothing to close for a local directory enumeration
	return nil
}

type LocalMediaFile struct {
	path     string
	sidecars *SidecarIndex
}

func (l *LocalMediaFile) Name() string {
	return l.path
}

func (l *LocalMediaFile) Open() (io.ReadCloser, error) {
	return os.Open(l.path)
}

func (l *LocalMediaFile) OpenSidecar() (io.ReadCloser, bool) {
	return l.sidecars.Find(filepath.ToSlash(l.path))
}

func isSidecarFile(name string) bool {
	return strings.EqualFold(filepath.Ext(name), ".json")
}
