package takeout

import (
	"encoding/json"
	"io"
	"strconv"
	"time"
)

// Metadata is the subset of a Takeout sidecar that PhotoSorter uses. A sidecar looks like:
//
//	{
//	  "title": "IMG_1234.jpg",
//	  "photoTakenTime": {"timestamp": "1672531200", "formatted": "Jan 1, 2023, 12:00:00 AM UTC"},
//	  "creationTime":   {"timestamp": "1672617600", "formatted": "..."}
//	}
//
// photoTakenTime is when the photo was captured (Google's best knowledge, often from
// EXIF before it was stripped). creationTime is when the item was uploaded.
type Metadata struct {
	Title          string    `json:"title"`
	Description    string    `json:"description"`
	PhotoTakenTime Timestamp `json:"photoTakenTime"`
	CreationTime   Timestamp `json:"creationTime"`
}

// Timestamp is a Takeout time value: Unix seconds stored as a decimal string.
type Timestamp struct {
	Timestamp string `json:"timestamp"`
	Formatted string `json:"formatted"`
}

// Time returns the timestamp as a time.Time. It reports false when the value is
// missing, malformed, or not after the Unix epoch (Takeout writes "0" for unknown).
func (t Timestamp) Time() (time.Time, bool) {
	secs, err := strconv.ParseInt(t.Timestamp, 10, 64)
	if err != nil || secs <= 0 {
		return time.Time{}, false
	}
	return time.Unix(secs, 0), true
}

// ReadMetadata decodes a sidecar.
func ReadMetadata(r io.Reader) (Metadata, error) {
	var m Metadata
	err := json.NewDecoder(r).Decode(&m)
	return m, err
}
