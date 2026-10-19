package importer

import (
	"context"
	"io"
	"os"
	"path/filepath"

	"photosorter/internal/library"
	"photosorter/internal/source"
)

// tempDirName is the folder, at the library root, that holds in-progress copies. A
// copy left there by a crash is removed when the next import starts. Only one import
// may run on a library at a time.
const tempDirName = ".photosorter-tmp"

// tempPattern names in-progress copies. The .tmp extension keeps them from being
// mistaken for media.
const tempPattern = "copy-*.tmp"

// tempDir is where in-progress copies go.
func (im *Importer) tempDir() string { return filepath.Join(im.opts.Library, tempDirName) }

// cleanTempDir removes copies left by an interrupted earlier run and recreates the
// folder.
func (im *Importer) cleanTempDir() error {
	if err := os.RemoveAll(im.tempDir()); err != nil {
		return err
	}
	return os.MkdirAll(im.tempDir(), 0755)
}

// writeFile copies f to path, or to a "_N"-suffixed variant if path is taken, and
// returns the path written.
//
// The data goes to a temp file in tmpDir first, so a failed or interrupted copy never
// leaves a file under a final name. The finished copy is then hard-linked into place,
// which fails rather than overwriting if the name is taken, so concurrent workers
// never overwrite each other.
func writeFile(ctx context.Context, f source.File, path, tmpDir string) (string, error) {
	tmp, err := copyToTemp(ctx, f, tmpDir)
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp) // the extra link, or the temp file if placing it failed
	return library.PlaceFile(tmp, path)
}

// copyToTemp copies f into a new temp file in dir and returns its path.
func copyToTemp(ctx context.Context, f source.File, dir string) (path string, err error) {
	rc, err := f.Open()
	if err != nil {
		return "", err
	}
	defer rc.Close()

	tmp, err := os.CreateTemp(dir, tempPattern)
	if err != nil {
		return "", err
	}
	defer func() {
		if err != nil {
			os.Remove(tmp.Name())
		}
	}()

	if _, err = io.Copy(tmp, contextReader{ctx, rc}); err != nil {
		tmp.Close()
		return "", err
	}
	if err = tmp.Close(); err != nil {
		return "", err
	}
	// CreateTemp uses 0600; match the 0666-minus-umask mode os.Create would have used.
	if err = os.Chmod(tmp.Name(), 0644); err != nil {
		return "", err
	}
	return tmp.Name(), nil
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
