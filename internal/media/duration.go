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

// MP4Duration reads the duration of an MP4/QuickTime file from its moov → mvhd box.
//
// It walks the top-level boxes, descends into moov, and skips everything else (seeking
// when r is an [io.Seeker]). Corrupt or truncated input returns an error rather than
// panicking or reading without bound.
func MP4Duration(r io.Reader) (time.Duration, error) {
	var header [8]byte
	for {
		if _, err := io.ReadFull(r, header[:]); err != nil {
			return 0, err
		}

		size := binary.BigEndian.Uint32(header[0:4])
		boxType := string(header[4:8])

		var boxSize int64
		var headerLen int64 = 8
		switch size {
		case 0:
			// The box extends to the end of the file. Only a moov box is worth descending into.
			if boxType != "moov" {
				return 0, fmt.Errorf("reached end-of-file box %q without finding moov", boxType)
			}
			continue
		case 1:
			// A 64-bit size follows the type.
			var ext [8]byte
			if _, err := io.ReadFull(r, ext[:]); err != nil {
				return 0, err
			}
			boxSize = int64(binary.BigEndian.Uint64(ext[:])) // negative if the top bit is set
			headerLen = 16
		default:
			boxSize = int64(size)
		}
		if boxSize < headerLen {
			return 0, fmt.Errorf("invalid %q box size %d", boxType, boxSize)
		}

		switch boxType {
		case "moov":
			continue // its children follow immediately
		case "mvhd":
			return parseMvhd(r, boxSize-headerLen)
		}
		if err := skipBytes(r, boxSize-headerLen); err != nil {
			return 0, err
		}
	}
}

// parseMvhd decodes the duration from the start of an mvhd box body of bodySize bytes.
func parseMvhd(r io.Reader, bodySize int64) (time.Duration, error) {
	body := make([]byte, min(bodySize, mvhdReadLimit))
	if _, err := io.ReadFull(r, body); err != nil {
		return 0, err
	}
	if len(body) < 1 {
		return 0, fmt.Errorf("mvhd box empty")
	}

	var timeScale uint32
	var duration uint64
	switch version := body[0]; version {
	case 0:
		if len(body) < 20 {
			return 0, fmt.Errorf("mvhd box too small")
		}
		timeScale = binary.BigEndian.Uint32(body[12:16])
		duration = uint64(binary.BigEndian.Uint32(body[16:20]))
	case 1:
		if len(body) < 32 {
			return 0, fmt.Errorf("mvhd box too small")
		}
		timeScale = binary.BigEndian.Uint32(body[20:24])
		duration = binary.BigEndian.Uint64(body[24:32])
	default:
		return 0, fmt.Errorf("unsupported mvhd version %d", version)
	}
	if timeScale == 0 {
		return 0, fmt.Errorf("invalid timescale 0")
	}

	secs := duration / uint64(timeScale)
	if secs > maxMediaSeconds {
		return 0, fmt.Errorf("implausible duration: %d/%d", duration, timeScale)
	}
	rem := duration % uint64(timeScale)
	return time.Duration(secs)*time.Second + time.Duration(rem)*time.Second/time.Duration(timeScale), nil
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
