package media

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// HEIF (HEIC) images are ISO base media files. Their EXIF block is not a header at the
// start of the file, as in JPEG or TIFF, but an item of type "Exif":
//
//	ftyp
//	meta              (full box)
//	  iinf            item list: an infe per item gives its ID and type
//	  iloc            item locations: where each item's bytes are
//	  idat            optional: item data stored inside meta
//	mdat              the items' data, usually including the Exif item
//
// The Exif item's data starts with a 32-bit offset to the TIFF header, which is what
// goexif decodes.

var errTruncated = errors.New("heif: truncated box")

// isHEIF reports whether head, the start of a file, looks like an ISO base media file.
// Any file with a top-level meta box is handled the same way, so the brand isn't checked.
func isHEIF(head []byte) bool {
	return len(head) >= 8 && string(head[4:8]) == "ftyp"
}

// heifEXIF returns the TIFF-format EXIF data of the HEIF file whose first bytes are
// head; r continues where head ends. head must hold the whole meta box. The Exif item
// itself is read from head when it lies within it, and otherwise from r if r is an
// [io.Seeker] positioned at the start of the file plus len(head).
func heifEXIF(head []byte, r io.Reader) ([]byte, error) {
	meta, err := findBox(head, "meta")
	if err != nil {
		return nil, err
	}
	if len(meta) < 4 {
		return nil, errTruncated
	}
	meta = meta[4:] // version and flags

	iinf, err := findBox(meta, "iinf")
	if err != nil {
		return nil, err
	}
	id, err := exifItemID(iinf)
	if err != nil {
		return nil, err
	}
	iloc, err := findBox(meta, "iloc")
	if err != nil {
		return nil, err
	}
	loc, err := itemLocation(iloc, id)
	if err != nil {
		return nil, err
	}

	var data []byte
	switch loc.method {
	case 0: // offsets into the file
		data, err = readExtents(head, r, loc.extents)
	case 1: // offsets into idat
		var idat []byte
		if idat, err = findBox(meta, "idat"); err == nil {
			data, err = readExtents(idat, nil, loc.extents)
		}
	default:
		err = fmt.Errorf("heif: unsupported construction method %d", loc.method)
	}
	if err != nil {
		return nil, err
	}

	if len(data) < 4 {
		return nil, errTruncated
	}
	tiffStart := 4 + uint64(binary.BigEndian.Uint32(data))
	if tiffStart > uint64(len(data)) {
		return nil, fmt.Errorf("heif: EXIF header offset %d past end of item", tiffStart)
	}
	return data[tiffStart:], nil
}

// findBox returns the body of the first box of type typ in b, a sequence of boxes.
// A box that runs past the end of b ends the search, unless it is the one wanted.
func findBox(b []byte, typ string) ([]byte, error) {
	for len(b) > 0 {
		t, body, rest, err := nextBox(b)
		if t == typ {
			return body, err
		}
		if err != nil {
			return nil, fmt.Errorf("heif: no %q box before %q ended: %w", typ, t, err)
		}
		b = rest
	}
	return nil, fmt.Errorf("heif: no %q box", typ)
}

// nextBox splits the first box off b. If the box claims to run past the end of b,
// body is what there is of it and err is errTruncated.
func nextBox(b []byte) (typ string, body, rest []byte, err error) {
	if len(b) < 8 {
		return "", nil, nil, errTruncated
	}
	typ = string(b[4:8])
	size := uint64(binary.BigEndian.Uint32(b))
	hdr := uint64(8)
	switch size {
	case 0: // to the end of the enclosing data
		size = uint64(len(b))
	case 1: // 64-bit size follows the type
		if len(b) < 16 {
			return typ, nil, nil, errTruncated
		}
		size, hdr = binary.BigEndian.Uint64(b[8:16]), 16
	}
	if size < hdr {
		return typ, nil, nil, fmt.Errorf("heif: invalid %q box size %d", typ, size)
	}
	if size > uint64(len(b)) {
		return typ, b[hdr:], nil, errTruncated
	}
	return typ, b[hdr:size], b[size:], nil
}

// exifItemID returns the ID of the item of type "Exif" listed in iinf.
func exifItemID(iinf []byte) (uint32, error) {
	p := parser{b: iinf}
	version := p.uint(1)
	p.uint(3) // flags
	if version == 0 {
		p.uint(2) // entry count
	} else {
		p.uint(4)
	}
	if p.err != nil {
		return 0, p.err
	}
	for entries := p.b; len(entries) > 0; {
		typ, infe, rest, err := nextBox(entries)
		if err != nil {
			return 0, err
		}
		entries = rest
		if typ != "infe" {
			continue
		}
		e := parser{b: infe}
		v := e.uint(1)
		e.uint(3) // flags
		var id uint64
		switch v {
		case 2:
			id = e.uint(2)
		case 3:
			id = e.uint(4)
		default:
			continue // versions 0 and 1 carry no item type
		}
		e.uint(2) // protection index
		itemType := e.bytes(4)
		if e.err == nil && string(itemType) == "Exif" {
			return uint32(id), nil
		}
	}
	return 0, errors.New("heif: no Exif item")
}

// location is where an item's data is, as recorded in iloc.
type location struct {
	method  uint64 // 0: offsets into the file, 1: offsets into idat
	extents []extent
}

type extent struct{ offset, length uint64 }

// itemLocation returns the location iloc records for item id.
func itemLocation(iloc []byte, id uint32) (location, error) {
	p := parser{b: iloc}
	version := p.uint(1)
	p.uint(3) // flags
	sizes := p.uint(1)
	offsetSize, lengthSize := int(sizes>>4), int(sizes&0xF)
	sizes = p.uint(1)
	baseOffsetSize, indexSize := int(sizes>>4), int(sizes&0xF)
	if version == 0 {
		indexSize = 0 // reserved
	}
	if max(offsetSize, lengthSize, baseOffsetSize, indexSize) > 8 {
		return location{}, errors.New("heif: invalid iloc field size")
	}
	idSize := 2
	if version >= 2 {
		idSize = 4
	}
	count := p.uint(idSize)
	for i := uint64(0); i < count && p.err == nil; i++ {
		var loc location
		itemID := p.uint(idSize)
		if version >= 1 {
			loc.method = p.uint(2) & 0xF
		}
		p.uint(2) // data reference index
		base := p.uint(baseOffsetSize)
		for n := p.uint(2); n > 0 && p.err == nil; n-- {
			p.uint(indexSize)
			e := extent{offset: p.uint(offsetSize), length: p.uint(lengthSize)}
			if e.offset > ^uint64(0)-base {
				return location{}, errors.New("heif: extent offset overflows")
			}
			e.offset += base
			loc.extents = append(loc.extents, e)
		}
		if p.err == nil && itemID == uint64(id) {
			return loc, nil
		}
	}
	if p.err != nil {
		return location{}, p.err
	}
	return location{}, fmt.Errorf("heif: no location for item %d", id)
}

// readExtents concatenates the extents, reading each from data when it lies within it
// and otherwise from r, which must then be an [io.Seeker] whose offset 0 is the start
// of data. At most exifReadLimit bytes are returned in total.
func readExtents(data []byte, r io.Reader, extents []extent) ([]byte, error) {
	var out []byte
	for _, e := range extents {
		if e.length == 0 {
			return nil, errors.New("heif: unbounded Exif extent")
		}
		if e.length > exifReadLimit-uint64(len(out)) {
			return nil, fmt.Errorf("heif: Exif item larger than %d bytes", exifReadLimit)
		}
		if e.offset <= uint64(len(data)) && e.length <= uint64(len(data))-e.offset {
			out = append(out, data[e.offset:e.offset+e.length]...)
			continue
		}
		s, ok := r.(io.Seeker)
		if !ok || e.offset > 1<<62 {
			return nil, errors.New("heif: Exif item beyond the readable part of the file")
		}
		if _, err := s.Seek(int64(e.offset), io.SeekStart); err != nil {
			return nil, err
		}
		buf := make([]byte, e.length)
		if _, err := io.ReadFull(r, buf); err != nil {
			return nil, err
		}
		out = append(out, buf...)
	}
	return out, nil
}

// parser reads big-endian fields from a byte slice. After the first read past the
// end, err is set and every later read returns zero.
type parser struct {
	b   []byte
	err error
}

// uint reads an n-byte unsigned integer; n may be 0 (giving 0) up to 8.
func (p *parser) uint(n int) uint64 {
	var v uint64
	for _, c := range p.bytes(n) {
		v = v<<8 | uint64(c)
	}
	return v
}

func (p *parser) bytes(n int) []byte {
	if p.err != nil {
		return nil
	}
	if n < 0 || n > len(p.b) {
		p.err = errTruncated
		p.b = nil
		return nil
	}
	v := p.b[:n]
	p.b = p.b[n:]
	return v
}
