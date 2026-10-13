// Package source enumerates the media to import: files inside Takeout zip archives
// ([OpenZip]) or in a folder on disk ([OpenFolder]).
//
// Sources do all of their scanning when they are opened. That way every sidecar from
// every source is in the shared [takeout.SidecarIndex] before the first file is
// processed, so a sidecar in one zip part can describe media in another.
package source

import (
	"io"
)

// File is one media file from a source.
type File interface {
	// Name identifies the file: the entry path inside a zip, or the filesystem path
	// for a folder source. Album detection and filename date parsing use it.
	Name() string

	// Size is the file's size in bytes, or -1 if unknown. It comes from untrusted
	// metadata (the zip directory), so it is only a hint.
	Size() int64

	// Open returns the file's content.
	Open() (io.ReadCloser, error)

	// OpenSidecar returns the Takeout JSON metadata describing this file, if any.
	OpenSidecar() (io.ReadCloser, bool)
}

// Source is a collection of media files.
type Source interface {
	// Name is a short label for progress output and logs.
	Name() string

	// Files lists the media found when the source was opened.
	Files() []File

	// Warnings lists paths that could not be read while scanning. Their files are
	// missing from Files.
	Warnings() []string

	// Close releases the source's resources. Files must not be opened afterwards.
	Close() error
}

// CloseAll closes every source, ignoring errors. It is a convenience for cleanup.
func CloseAll(sources []Source) {
	for _, s := range sources {
		s.Close()
	}
}
