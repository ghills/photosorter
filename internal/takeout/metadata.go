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
//	  "creationTime":   {"timestamp": "1672617600", "formatted": "..."},
//	  "geoData":        {"latitude": 48.8584, "longitude": 2.2945, "altitude": 35.0},
//	  "geoDataExif":    {"latitude": 48.8584, "longitude": 2.2945, "altitude": 35.0},
//	  "people":         [{"name": "Alice"}],
//	  "favorited":      true
//	}
//
// photoTakenTime is when the photo was captured (Google's best knowledge, often from
// EXIF before it was stripped). creationTime is when the item was uploaded. geoData is
// the location as shown in Google Photos (possibly edited there); geoDataExif is the
// one the camera recorded. Unknown locations are written as 0, 0.
type Metadata struct {
	Title          string    `json:"title"`
	Description    string    `json:"description"`
	PhotoTakenTime Timestamp `json:"photoTakenTime"`
	CreationTime   Timestamp `json:"creationTime"`
	GeoData        GeoData   `json:"geoData"`
	GeoDataExif    GeoData   `json:"geoDataExif"`
	People         []Person  `json:"people"`
	Favorited      bool      `json:"favorited"`
}

// GeoData is a location in a sidecar, in decimal degrees and metres above sea level.
type GeoData struct {
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
	Altitude  float64 `json:"altitude"`
}

// Known reports whether g holds a location: Takeout writes 0, 0 for none.
func (g GeoData) Known() bool { return g.Latitude != 0 || g.Longitude != 0 }

// Person is someone Google Photos recognised in the item.
type Person struct {
	Name string `json:"name"`
}

// Location returns geoData, or geoDataExif if geoData is unknown. It reports false
// when neither is known.
func (m Metadata) Location() (GeoData, bool) {
	for _, g := range []GeoData{m.GeoData, m.GeoDataExif} {
		if g.Known() {
			return g, true
		}
	}
	return GeoData{}, false
}

// PeopleNames returns the non-empty names in People.
func (m Metadata) PeopleNames() []string {
	var names []string
	for _, p := range m.People {
		if p.Name != "" {
			names = append(names, p.Name)
		}
	}
	return names
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
