package importer

import (
	"bytes"
	"context"
	"io"

	"photosorter/internal/library"
	"photosorter/internal/source"
)

// hashAndBuffer hashes f. When f is small enough it is read into memory and the
// returned File serves its content from there, so later steps cost no further reads
// of the source (which, for a zip, means no further decompression). The caller must
// call release when done with the returned File.
func (im *Importer) hashAndBuffer(ctx context.Context, f source.File) (hash string, out source.File, release func(), err error) {
	limit := im.opts.BufferLimit
	if size := f.Size(); limit > 0 && size >= 0 && size <= limit {
		hash, out, release, ok, err := im.bufferAndHash(ctx, f, size)
		if err != nil || ok {
			return hash, out, release, err
		}
		// Larger than advertised: fall through and stream it instead of holding it in memory.
	}

	rc, err := f.Open()
	if err != nil {
		return "", nil, nil, err
	}
	defer rc.Close()
	hash, err = library.Hash(contextReader{ctx, rc})
	return hash, f, func() {}, err
}

// bufferAndHash reads f into a pooled buffer and hashes it. ok is false if the file
// turned out to exceed BufferLimit (the size hint is untrusted), in which case nothing
// is kept.
func (im *Importer) bufferAndHash(ctx context.Context, f source.File, size int64) (hash string, out source.File, release func(), ok bool, err error) {
	rc, err := f.Open()
	if err != nil {
		return "", nil, nil, false, err
	}
	defer rc.Close()

	buf, _ := im.bufPool.Get().(*bytes.Buffer)
	if buf == nil {
		buf = new(bytes.Buffer)
	}
	buf.Reset()
	buf.Grow(int(size) + 1)
	release = func() { im.bufPool.Put(buf) }

	limit := im.opts.BufferLimit
	if _, err := buf.ReadFrom(io.LimitReader(contextReader{ctx, rc}, limit+1)); err != nil {
		release()
		return "", nil, nil, false, err
	}
	if int64(buf.Len()) > limit {
		release()
		return "", nil, nil, false, nil
	}
	return library.HashBytes(buf.Bytes()), &bufferedFile{File: f, data: buf.Bytes()}, release, true, nil
}

// bufferedFile serves an already-read file's content from memory.
type bufferedFile struct {
	source.File
	data []byte
}

func (b *bufferedFile) Size() int64 { return int64(len(b.data)) }

func (b *bufferedFile) Open() (io.ReadCloser, error) {
	return memReader{bytes.NewReader(b.data)}, nil
}

// memReader is a ReadCloser that keeps bytes.Reader's Seek method, so MP4 parsing can
// skip boxes cheaply.
type memReader struct{ *bytes.Reader }

func (memReader) Close() error { return nil }
