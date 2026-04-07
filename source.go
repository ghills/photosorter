package main

import (
	"archive/zip"
	"io"
	"os"
	"path/filepath"
)

// MediaFile represents an abstract file containing media data,
// decoupled from its original source (zip archive, local dir, etc.).
type MediaFile interface {
	Name() string // Name of the file, relative to source or absolute
	Open() (io.ReadCloser, error)
	OpenSidecar(names []string) (io.ReadCloser, bool)
}

// InputSource implies a place where media files reside.
type InputSource interface {
	Name() string
	MediaFiles() ([]MediaFile, error)
	Close() error
}

// --- ZipSource Implementation ---

type ZipSource struct {
	path string
	r    *zip.ReadCloser
}

func NewZipSource(path string) (*ZipSource, error) {
	r, err := zip.OpenReader(path)
	if err != nil {
		return nil, err
	}
	return &ZipSource{
		path: path,
		r:    r,
	}, nil
}

func (z *ZipSource) Name() string {
	return filepath.Base(z.path)
}

func (z *ZipSource) MediaFiles() ([]MediaFile, error) {
	zipIndex := make(map[string]*zip.File)
	var mediaFiles []MediaFile

	for _, f := range z.r.File {
		if f.FileInfo().IsDir() {
			continue
		}
		zipIndex[f.Name] = f
	}

	for _, f := range z.r.File {
		if !f.FileInfo().IsDir() && IsMedia(f.Name) {
			mediaFiles = append(mediaFiles, &ZipMediaFile{
				f:        f,
				zipIndex: zipIndex,
			})
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
	zipIndex map[string]*zip.File
}

func (z *ZipMediaFile) Name() string {
	return z.f.Name
}

func (z *ZipMediaFile) Open() (io.ReadCloser, error) {
	return z.f.Open()
}

func (z *ZipMediaFile) OpenSidecar(names []string) (io.ReadCloser, bool) {
	for _, n := range names {
		if jf, found := z.zipIndex[n]; found {
			if rc, err := jf.Open(); err == nil {
				return rc, true
			}
		}
	}
	return nil, false
}

// --- LocalFolderSource Implementation ---

type LocalFolderSource struct {
	path string
}

func NewLocalFolderSource(path string) *LocalFolderSource {
	return &LocalFolderSource{path: path}
}

func (l *LocalFolderSource) Name() string {
	return filepath.Base(l.path)
}

func (l *LocalFolderSource) MediaFiles() ([]MediaFile, error) {
	var mediaFiles []MediaFile
	err := filepath.WalkDir(l.path, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // Skip paths we can't access
		}
		if !d.IsDir() && IsMedia(d.Name()) {
			mediaFiles = append(mediaFiles, &LocalMediaFile{
				path: path,
			})
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
	path string
}

func (l *LocalMediaFile) Name() string {
	return l.path
}

func (l *LocalMediaFile) Open() (io.ReadCloser, error) {
	return os.Open(l.path)
}

func (l *LocalMediaFile) OpenSidecar(names []string) (io.ReadCloser, bool) {
	for _, n := range names {
		// Calculate the exact path for the sidecar
		// Ensure that the paths being checked translate to actual filesystem locations if absolute.
		// If names are relative to the file's directory (like `metadata.go` makes them based off f.Name which is absolute)
		if f, err := os.Open(n); err == nil {
			return f, true
		}
	}
	return nil, false
}
