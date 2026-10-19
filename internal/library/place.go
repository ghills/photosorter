package library

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// PlaceFile gives the file src the name path, or path with a _N suffix if that name
// is taken, and returns the name used. It never replaces an existing file. src must
// be on the same filesystem as path; it is gone afterwards unless the hard link that
// replaces it could be made, in which case the caller removes src.
//
// The file is hard-linked into place, which fails rather than overwriting if the
// name is taken, so concurrent callers never overwrite each other.
func PlaceFile(src, path string) (string, error) {
	ext := filepath.Ext(path)
	stem := strings.TrimSuffix(path, ext)
	candidate := path
	for n := 1; ; n++ {
		err := os.Link(src, candidate)
		if err == nil {
			return candidate, nil
		}
		if !os.IsExist(err) {
			// No hard links here (e.g. exFAT, some network shares): reserve the name, then
			// rename over the reservation.
			return reserveAndRename(src, candidate, stem, ext, n)
		}
		candidate = fmt.Sprintf("%s_%d%s", stem, n, ext)
	}
}

// reserveAndRename is PlaceFile for filesystems without hard links. A crash between
// the reservation and the rename leaves an empty file under the final name.
func reserveAndRename(src, candidate, stem, ext string, n int) (string, error) {
	for ; ; n++ {
		f, err := os.OpenFile(candidate, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
		if err == nil {
			if err := f.Close(); err != nil {
				return "", err
			}
			if err := os.Rename(src, candidate); err != nil {
				os.Remove(candidate)
				return "", err
			}
			return candidate, nil
		}
		if !os.IsExist(err) {
			return "", err
		}
		candidate = fmt.Sprintf("%s_%d%s", stem, n, ext)
	}
}
