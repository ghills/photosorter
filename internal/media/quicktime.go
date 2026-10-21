package media

import (
	"encoding/binary"
	"strings"
	"time"
)

// QuickTime metadata, as iPhones and other Apple devices write it, lives in moov → meta:
//
//	meta
//	  hdlr        handler type "mdta"
//	  keys        the item names, e.g. "com.apple.quicktime.creationdate"
//	  ilst        one box per item, whose type is the item's 1-based index in keys,
//	    data      holding a type code, a locale and the value
//
// The creation date is an ISO 8601 local time with its UTC offset, such as
// "2023-05-14T12:34:56-0700", so it gives the day the video was recorded wherever that
// was, the way an EXIF date does for a photo.

// creationDateKey is the metadata item holding the recording time.
const creationDateKey = "com.apple.quicktime.creationdate"

// creationDateLayouts are the forms the creation date is written in. Fractional
// seconds after the seconds are accepted by any of them.
var creationDateLayouts = []string{"2006-01-02T15:04:05-0700", time.RFC3339}

// appleCreationDate returns the creation date in the body of a moov → meta box, or
// the zero time if it has none that parses.
func appleCreationDate(meta []byte) time.Time {
	// QuickTime's meta is a plain box; the ISO form is a full box with 4 bytes of
	// version and flags first. Either way, hdlr is the first child.
	if len(meta) >= 12 && string(meta[4:8]) != "hdlr" && string(meta[8:12]) == "hdlr" {
		meta = meta[4:]
	}
	keys, err := findBox(meta, "keys")
	if err != nil {
		return time.Time{}
	}
	index := keyIndex(keys, creationDateKey)
	if index == 0 {
		return time.Time{}
	}
	ilst, err := findBox(meta, "ilst")
	if err != nil {
		return time.Time{}
	}
	for items := ilst; len(items) > 0; {
		typ, item, rest, err := nextBox(items)
		if err != nil {
			return time.Time{}
		}
		items = rest
		if binary.BigEndian.Uint32([]byte(typ)) != index {
			continue
		}
		data, err := findBox(item, "data")
		if err != nil || len(data) < 8 {
			return time.Time{}
		}
		return parseCreationDate(string(data[8:])) // after the type code and locale
	}
	return time.Time{}
}

// keyIndex returns the 1-based index of name in a keys box body, or 0 if absent.
func keyIndex(keys []byte, name string) uint32 {
	p := parser{b: keys}
	p.uint(4) // version and flags
	count := p.uint(4)
	for i := uint64(1); i <= count && p.err == nil; i++ {
		size := p.uint(4)
		if size < 8 {
			return 0
		}
		p.uint(4) // namespace, normally "mdta"
		if size-8 > uint64(len(p.b)) {
			return 0
		}
		if key := p.bytes(int(size - 8)); string(key) == name {
			return uint32(i)
		}
	}
	return 0
}

// parseCreationDate parses a creation date value, keeping its UTC offset as the
// zone. It reports the zero time for an unparseable or implausible value.
func parseCreationDate(s string) time.Time {
	s = strings.TrimRight(s, "\x00 ")
	for _, layout := range creationDateLayouts {
		if t, err := time.Parse(layout, s); err == nil && t.Year() >= 1971 && t.Year() < 2100 {
			return t
		}
	}
	return time.Time{}
}
