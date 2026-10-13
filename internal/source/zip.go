package source

import (
	"archive/zip"
	"io"
	"path/filepath"

	"photosorter/internal/media"
	"photosorter/internal/takeout"
)

// zipSource reads media straight out of a zip archive, without extracting it.
type zipSource struct {
	path  string
	r     *zip.ReadCloser
	files []File
}

// OpenZip opens the zip at path. Media entries accepted by filter become its files,
// and every .json entry is registered in sidecars.
func OpenZip(path string, filter media.Filter, sidecars *takeout.SidecarIndex) (Source, error) {
	r, err := zip.OpenReader(path)
	if err != nil {
		return nil, err
	}
	z := &zipSource{path: path, r: r}
	for _, f := range r.File {
		switch {
		case f.FileInfo().IsDir():
		case takeout.IsSidecar(f.Name):
			sidecars.Add(f.Name, f.Open)
		case filter.Match(f.Name):
			z.files = append(z.files, &zipFile{f: f, sidecars: sidecars})
		}
	}
	return z, nil
}

func (z *zipSource) Name() string       { return filepath.Base(z.path) }
func (z *zipSource) Files() []File      { return z.files }
func (z *zipSource) Warnings() []string { return nil }
func (z *zipSource) Close() error       { return z.r.Close() }

// zipFile is one media entry in a zip.
type zipFile struct {
	f        *zip.File
	sidecars *takeout.SidecarIndex
}

func (z *zipFile) Name() string                       { return z.f.Name }
func (z *zipFile) Size() int64                        { return int64(z.f.UncompressedSize64) }
func (z *zipFile) Open() (io.ReadCloser, error)       { return z.f.Open() }
func (z *zipFile) OpenSidecar() (io.ReadCloser, bool) { return z.sidecars.Find(z.f.Name) }
