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

// MovieHeader is what PhotoSorter uses from an MP4/QuickTime movie header (mvhd) box.
type MovieHeader struct {
	created   uint64 // seconds since mp4Epoch; 0 if unknown
	timeScale uint32 // units per second of duration
	duration  uint64
}

// MP4Duration reads the duration of an MP4/QuickTime file. See [ReadMovieHeader].
func MP4Duration(r io.Reader) (time.Duration, error) {
	h, err := ReadMovieHeader(r)
	if err != nil {
		return 0, err
	}
	return h.Duration()
}

// ReadMovieHeader reads the moov → mvhd box of an MP4/QuickTime file.
//
// It walks the top-level boxes, descends into moov, and skips everything else (seeking
// when r is an [io.Seeker]). Corrupt or truncated input returns an error rather than
// panicking or reading without bound.
func ReadMovieHeader(r io.Reader) (MovieHeader, error) {
	var header [8]byte
	for {
		if _, err := io.ReadFull(r, header[:]); err != nil {
			return MovieHeader{}, err
		}

		size := binary.BigEndian.Uint32(header[0:4])
		boxType := string(header[4:8])

		var boxSize int64
		var headerLen int64 = 8
		switch size {
		case 0:
			// The box extends to the end of the file. Only a moov box is worth descending into.
			if boxType != "moov" {
				return MovieHeader{}, fmt.Errorf("reached end-of-file box %q without finding moov", boxType)
			}
			continue
		case 1:
			// A 64-bit size follows the type.
			var ext [8]byte
			if _, err := io.ReadFull(r, ext[:]); err != nil {
				return MovieHeader{}, err
			}
			boxSize = int64(binary.BigEndian.Uint64(ext[:])) // negative if the top bit is set
			headerLen = 16
		default:
			boxSize = int64(size)
		}
		if boxSize < headerLen {
			return MovieHeader{}, fmt.Errorf("invalid %q box size %d", boxType, boxSize)
		}

		switch boxType {
		case "moov":
			continue // its children follow immediately
		case "mvhd":
			return parseMvhd(r, boxSize-headerLen)
		}
		if err := skipBytes(r, boxSize-headerLen); err != nil {
			return MovieHeader{}, err
		}
	}
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

// Created returns when the movie was recorded. It reports false when the header has
// no creation time, or one before 1971: many encoders write 0, and some write Unix
// seconds where MP4 seconds belong, which reads as a date in the early 1900s.
//
// The value is an absolute instant (MP4 specifies UTC), so the day it falls on
// depends on the zone it is read in.
func (h MovieHeader) Created() (time.Time, bool) {
	t := time.Unix(mp4Epoch+int64(min(h.created, maxCreatedSeconds)), 0)
	if h.created > maxCreatedSeconds || t.Year() < 1971 {
		return time.Time{}, false
	}
	return t, true
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
