package importer

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"photosorter/internal/source"
)

// tempPattern names in-progress copies. The leading dot keeps them out of the way
// and the .tmp extension keeps them from being mistaken for media.
const tempPattern = ".photosorter-*.tmp"

// writeFile copies f to path, or to a "_N"-suffixed variant if path is taken, and
// returns the path written.
//
// The name is reserved atomically (O_EXCL) so concurrent workers never overwrite each
// other, and the data goes through a temp file so a failed copy never leaves a
// partial file under its final name.
func writeFile(ctx context.Context, f source.File, path string) (string, error) {
	dest, err := reservePath(path)
	if err != nil {
		return "", err
	}
	if err := copyAtomically(ctx, f, dest); err != nil {
		os.Remove(dest) // release the empty reservation
		return "", err
	}
	return dest, nil
}

// copyAtomically copies f into a temp file next to dest, then renames it over dest.
func copyAtomically(ctx context.Context, f source.File, dest string) (err error) {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()

	tmp, err := os.CreateTemp(filepath.Dir(dest), tempPattern)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			os.Remove(tmp.Name())
		}
	}()

	if _, err = io.Copy(tmp, contextReader{ctx, rc}); err != nil {
		tmp.Close()
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	// CreateTemp uses 0600; match the 0666-minus-umask mode os.Create would have used.
	if err = os.Chmod(tmp.Name(), 0644); err != nil {
		return err
	}
	// Rename replaces the empty reservation atomically.
	return os.Rename(tmp.Name(), dest)
}

// reservePath creates an empty file at path, or at path with a _N suffix if that name
// is taken, and returns the path it created. O_EXCL makes the check and the creation
// a single atomic step.
func reservePath(path string) (string, error) {
	ext := filepath.Ext(path)
	stem := strings.TrimSuffix(path, ext)
	candidate := path
	for n := 1; ; n++ {
		f, err := os.OpenFile(candidate, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
		if err == nil {
			return candidate, f.Close()
		}
		if !os.IsExist(err) {
			return "", err
		}
		candidate = fmt.Sprintf("%s_%d%s", stem, n, ext)
	}
}

// contextReader makes long reads abortable: once ctx is cancelled, reads fail with its error.
type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (c contextReader) Read(b []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(b)
}
