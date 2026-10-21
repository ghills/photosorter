// Package mediatest builds minimal, valid media files for tests: a JPEG, TIFF or HEIC
// image carrying an EXIF capture date, and an MP4 with a movie header.
package mediatest

import (
	"bytes"
	"encoding/binary"
	"time"
)

// mp4Epoch is the MP4/QuickTime epoch, 1904-01-01 UTC, in Unix seconds.
const mp4Epoch = -2082844800

// TIFF returns big-endian TIFF-format EXIF data whose DateTimeOriginal is taken's wall clock.
func TIFF(taken time.Time) []byte {
	value := append([]byte(taken.Format("2006:01:02 15:04:05")), 0) // 20 bytes

	// Header (8), IFD0 with one entry (2+12+4) at 8, Exif IFD with one entry at 26,
	// then the date string at 44.
	var b bytes.Buffer
	b.WriteString("MM\x00\x2a")
	put32(&b, 8)

	put16(&b, 1)
	put16(&b, 0x8769) // ExifIFDPointer
	put16(&b, 4)      // LONG
	put32(&b, 1)
	put32(&b, 26)
	put32(&b, 0) // no next IFD

	put16(&b, 1)
	put16(&b, 0x9003) // DateTimeOriginal
	put16(&b, 2)      // ASCII
	put32(&b, uint32(len(value)))
	put32(&b, 44)
	put32(&b, 0)

	b.Write(value)
	return b.Bytes()
}

// JPEG returns a JPEG whose EXIF DateTimeOriginal is taken's wall clock. It has no
// image data; EXIF parsing never looks for any.
func JPEG(taken time.Time) []byte {
	app1 := append([]byte("Exif\x00\x00"), TIFF(taken)...)
	var b bytes.Buffer
	b.Write([]byte{0xFF, 0xD8, 0xFF, 0xE1})
	put16(&b, uint16(2+len(app1)))
	b.Write(app1)
	b.Write([]byte{0xFF, 0xD9})
	return b.Bytes()
}

// HEICOptions vary where [HEIC] puts the Exif item.
type HEICOptions struct {
	// Gap is the size of a free box placed between meta and mdat, to push the Exif
	// item further into the file.
	Gap int
	// InIdat stores the Exif item inside meta's idat box (iloc construction method 1)
	// instead of in mdat.
	InIdat bool
}

// HEIC returns a HEIF file whose Exif item carries taken's wall clock as
// DateTimeOriginal. Like a real iPhone photo it is laid out ftyp, meta, mdat, but it
// has no image item.
func HEIC(taken time.Time, opts HEICOptions) []byte {
	// The item data: a 4-byte offset to the TIFF header, here past "Exif\0\0".
	var item bytes.Buffer
	put32(&item, 6)
	item.WriteString("Exif\x00\x00")
	item.Write(TIFF(taken))

	ftyp := Box("ftyp", []byte("heic\x00\x00\x00\x00mif1heic"))

	var infe bytes.Buffer
	infe.Write([]byte{2, 0, 0, 0}) // version 2
	put16(&infe, 1)                // item ID
	put16(&infe, 0)                // protection index
	infe.WriteString("Exif\x00")   // item type, empty name
	var iinf bytes.Buffer
	iinf.Write([]byte{0, 0, 0, 0})
	put16(&iinf, 1)
	iinf.Write(Box("infe", infe.Bytes()))

	// iloc version 1 (so it has a construction method) with 4-byte offsets and lengths.
	iloc := func(offset uint32) []byte {
		var b bytes.Buffer
		b.Write([]byte{1, 0, 0, 0})
		b.Write([]byte{0x44, 0x00}) // offset and length sizes 4; base offset and index sizes 0
		put16(&b, 1)                // item count
		put16(&b, 1)                // item ID
		if opts.InIdat {
			put16(&b, 1)
		} else {
			put16(&b, 0)
		}
		put16(&b, 0) // data reference index
		put16(&b, 1) // extent count
		put32(&b, offset)
		put32(&b, uint32(item.Len()))
		return Box("iloc", b.Bytes())
	}
	hdlr := Box("hdlr", append(make([]byte, 8), []byte("pict\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00")...))
	meta := func(offset uint32) []byte {
		body := append([]byte{0, 0, 0, 0}, hdlr...)
		body = append(body, Box("iinf", iinf.Bytes())...)
		body = append(body, iloc(offset)...)
		if opts.InIdat {
			body = append(body, Box("idat", item.Bytes())...)
		}
		return Box("meta", body)
	}

	var gap []byte
	if opts.Gap > 0 {
		gap = Box("free", make([]byte, opts.Gap))
	}
	if opts.InIdat {
		out := append(ftyp, meta(0)...)
		return append(out, gap...)
	}
	// The meta box's size doesn't depend on the offset, so build it once to measure.
	offset := uint32(len(ftyp) + len(meta(0)) + len(gap) + 8)
	out := append(ftyp, meta(offset)...)
	out = append(out, gap...)
	return append(out, Box("mdat", item.Bytes())...)
}

// MP4 returns an MP4 with a version-0 movie header recording created (zero for
// none) and duration, followed by an mdat of mdatSize bytes.
func MP4(created time.Time, duration time.Duration, mdatSize int) []byte {
	var mvhd bytes.Buffer
	mvhd.Write([]byte{0, 0, 0, 0})
	if created.IsZero() {
		put32(&mvhd, 0)
	} else {
		put32(&mvhd, uint32(created.Unix()-mp4Epoch))
	}
	put32(&mvhd, 0)    // modification time
	put32(&mvhd, 1000) // timescale
	put32(&mvhd, uint32(duration.Milliseconds()))
	out := Box("ftyp", []byte("isom\x00\x00\x02\x00"))
	out = append(out, Box("moov", Box("mvhd", mvhd.Bytes()))...)
	return append(out, Box("mdat", make([]byte, mdatSize))...)
}

// Box builds an ISO base media box with a 32-bit size.
func Box(typ string, body []byte) []byte {
	b := make([]byte, 8, 8+len(body))
	binary.BigEndian.PutUint32(b, uint32(8+len(body)))
	copy(b[4:], typ)
	return append(b, body...)
}

func put16(b *bytes.Buffer, v uint16) { b.Write(binary.BigEndian.AppendUint16(nil, v)) }
func put32(b *bytes.Buffer, v uint32) { b.Write(binary.BigEndian.AppendUint32(nil, v)) }
