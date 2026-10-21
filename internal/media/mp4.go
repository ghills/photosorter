package media

import (
	"encoding/binary"
	"fmt"
	"io"
	"time"
)

// mvhdReadLimit bounds how much of an mvhd box is read. Versions 0 and 1 need 20 and 32
// bytes respectively, so anything past that is never touched.
const mvhdReadLimit = 32

// maxMediaSeconds caps plausible durations (~68 years) so corrupt headers can't overflow time.Duration.
const maxMediaSeconds = 1 << 31

// mp4Epoch is the MP4/QuickTime epoch, 1904-01-01 UTC, in Unix seconds.
const mp4Epoch = -2082844800

// maxMetaSize bounds the moov → meta box read into memory for its metadata items.
// QuickTime metadata is a few kilobytes; anything far larger is skipped.
const maxMetaSize = 1 << 20

// MovieHeader is what PhotoSorter uses from an MP4/QuickTime movie: its movie header
// (mvhd) box and, if present, Apple's creation date metadata.
type MovieHeader struct {
	created   uint64 // seconds since mp4Epoch; 0 if unknown
	timeScale uint32 // units per second of duration
	duration  uint64

	// recorded is the com.apple.quicktime.creationdate item: a wall clock with the
	// zone it was recorded in. Zero if the movie has none.
	recorded time.Time
}

// MP4Duration reads the duration of an MP4/QuickTime file. See [ReadMovieHeader].
func MP4Duration(r io.Reader) (time.Duration, error) {
	h, err := ReadMovieHeader(r)
	if err != nil {
		return 0, err
	}
	return h.Duration()
}

// ReadMovieHeader reads an MP4/QuickTime file's moov box: its mvhd box, and the
// metadata in moov → meta.
//
// It walks the top-level boxes to moov and skips everything else (seeking when r is
// an [io.Seeker]). Corrupt or truncated input returns an error rather than panicking
// or reading without bound. Once mvhd has been read, a problem later in moov only
// ends the walk, so the header is still returned.
func ReadMovieHeader(r io.Reader) (MovieHeader, error) {
	for {
		boxType, bodySize, _, err := readBoxHeader(r)
		if err != nil {
			return MovieHeader{}, err
		}
		if boxType == "moov" {
			return readMoov(r, bodySize)
		}
		if bodySize < 0 {
			return MovieHeader{}, fmt.Errorf("reached end-of-file box %q without finding moov", boxType)
		}
		if err := skipBytes(r, bodySize); err != nil {
			return MovieHeader{}, err
		}
	}
}

// readBoxHeader reads a box header and returns the box's type, the size of its body
// (-1 for a box that extends to the end of the file) and the header's own length.
func readBoxHeader(r io.Reader) (boxType string, bodySize, headerLen int64, err error) {
	var header [8]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return "", 0, 0, err
	}
	size := binary.BigEndian.Uint32(header[0:4])
	boxType = string(header[4:8])

	var boxSize int64
	headerLen = 8
	switch size {
	case 0:
		return boxType, -1, headerLen, nil
	case 1:
		// A 64-bit size follows the type.
		var ext [8]byte
		if _, err := io.ReadFull(r, ext[:]); err != nil {
			return "", 0, 0, err
		}
		boxSize = int64(binary.BigEndian.Uint64(ext[:])) // negative if the top bit is set
		headerLen = 16
	default:
		boxSize = int64(size)
	}
	if boxSize < headerLen {
		return "", 0, 0, fmt.Errorf("invalid %q box size %d", boxType, boxSize)
	}
	return boxType, boxSize - headerLen, headerLen, nil
}

// readMoov reads the children of a moov box whose body is size bytes long (-1: to the
// end of the file), stopping at the end of moov.
func readMoov(r io.Reader, size int64) (MovieHeader, error) {
	var h MovieHeader
	found := false
	// fail ends the walk: with an error until mvhd is found, quietly after.
	fail := func(err error) (MovieHeader, error) {
		if found {
			return h, nil
		}
		return MovieHeader{}, err
	}
	for size != 0 {
		boxType, bodySize, headerLen, err := readBoxHeader(r)
		if err == io.EOF && size < 0 {
			err = fmt.Errorf("no mvhd box in moov")
		}
		if err != nil {
			return fail(err)
		}
		if size >= 0 && size < headerLen {
			return fail(fmt.Errorf("%q box header overruns moov", boxType))
		}
		if bodySize < 0 {
			if size < 0 {
				return fail(fmt.Errorf("unbounded %q box in unbounded moov", boxType))
			}
			bodySize = size - headerLen // a child running to the end of moov
		}
		if size >= 0 {
			if bodySize > size-headerLen {
				return fail(fmt.Errorf("%q box overruns moov", boxType))
			}
			size -= headerLen + bodySize
		}

		switch {
		case boxType == "mvhd" && !found:
			var mvhd MovieHeader
			if mvhd, err = parseMvhd(r, bodySize); err != nil {
				return MovieHeader{}, err
			}
			mvhd.recorded = h.recorded // in case meta came first
			h, found = mvhd, true
			err = skipBytes(r, bodySize-min(bodySize, mvhdReadLimit))
		case boxType == "meta" && bodySize <= maxMetaSize:
			body := make([]byte, bodySize)
			if _, err = io.ReadFull(r, body); err == nil {
				h.recorded = appleCreationDate(body)
			}
		default:
			err = skipBytes(r, bodySize)
		}
		if err != nil {
			return fail(err)
		}
	}
	return fail(fmt.Errorf("no mvhd box in moov"))
}

// parseMvhd decodes the start of an mvhd box body of bodySize bytes.
func parseMvhd(r io.Reader, bodySize int64) (MovieHeader, error) {
	body := make([]byte, min(bodySize, mvhdReadLimit))
	if _, err := io.ReadFull(r, body); err != nil {
		return MovieHeader{}, err
	}
	if len(body) < 1 {
		return MovieHeader{}, fmt.Errorf("mvhd box empty")
	}

	var h MovieHeader
	switch version := body[0]; version {
	case 0:
		if len(body) < 20 {
			return MovieHeader{}, fmt.Errorf("mvhd box too small")
		}
		h.created = uint64(binary.BigEndian.Uint32(body[4:8]))
		h.timeScale = binary.BigEndian.Uint32(body[12:16])
		h.duration = uint64(binary.BigEndian.Uint32(body[16:20]))
	case 1:
		if len(body) < 32 {
			return MovieHeader{}, fmt.Errorf("mvhd box too small")
		}
		h.created = binary.BigEndian.Uint64(body[4:12])
		h.timeScale = binary.BigEndian.Uint32(body[20:24])
		h.duration = binary.BigEndian.Uint64(body[24:32])
	default:
		return MovieHeader{}, fmt.Errorf("unsupported mvhd version %d", version)
	}
	return h, nil
}

// Duration returns the movie's duration.
func (h MovieHeader) Duration() (time.Duration, error) {
	if h.timeScale == 0 {
		return 0, fmt.Errorf("invalid timescale 0")
	}
	secs := h.duration / uint64(h.timeScale)
	if secs > maxMediaSeconds {
		return 0, fmt.Errorf("implausible duration: %d/%d", h.duration, h.timeScale)
	}
	rem := h.duration % uint64(h.timeScale)
	return time.Duration(secs)*time.Second + time.Duration(rem)*time.Second/time.Duration(h.timeScale), nil
}

// maxCreatedSeconds caps plausible creation times (about the year 2100 in MP4 seconds).
const maxCreatedSeconds = 6_250_000_000

// Created returns when the movie was recorded, in the zone its date should be read in.
//
// Apple's creation date metadata comes first: it is a local time with its UTC offset,
// and is returned in that offset's zone, so its date is the one where it was recorded.
// Otherwise the movie header's creation time is used. That is an absolute instant (MP4
// specifies UTC) with no zone of its own, so it is returned in loc.
//
// It reports false when neither is there. A header time before 1971 counts as none:
// many encoders write 0, and some write Unix seconds where MP4 seconds belong, which
// reads as a date in the early 1900s.
func (h MovieHeader) Created(loc *time.Location) (time.Time, bool) {
	if !h.recorded.IsZero() {
		return h.recorded, true
	}
	t := time.Unix(mp4Epoch+int64(min(h.created, maxCreatedSeconds)), 0)
	if h.created > maxCreatedSeconds || t.Year() < 1971 {
		return time.Time{}, false
	}
	return t.In(loc), true
}

// skipBytes advances r by n bytes, seeking when r supports it instead of reading and
// discarding. Skipping past the end of the data is an error.
func skipBytes(r io.Reader, n int64) error {
	if n <= 0 {
		return nil
	}
	if s, ok := r.(io.Seeker); ok {
		cur, err := s.Seek(0, io.SeekCurrent)
		if err != nil {
			return err
		}
		end, err := s.Seek(0, io.SeekEnd)
		if err != nil {
			return err
		}
		if n > end-cur {
			return io.ErrUnexpectedEOF
		}
		_, err = s.Seek(cur+n, io.SeekStart)
		return err
	}
	copied, err := io.CopyN(io.Discard, r, n)
	if err == io.EOF && copied < n {
		return io.ErrUnexpectedEOF
	}
	return err
}
