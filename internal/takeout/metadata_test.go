package takeout

import (
	"strings"
	"testing"
	"time"
)

func TestReadMetadata(t *testing.T) {
	m, err := ReadMetadata(strings.NewReader(`{
		"title": "test.mp4",
		"photoTakenTime": {"timestamp": "1672531200", "formatted": "Jan 1, 2023, 12:00:00 AM UTC"},
		"creationTime": {"timestamp": "0"}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if m.Title != "test.mp4" {
		t.Errorf("Title = %q", m.Title)
	}
	if got, ok := m.PhotoTakenTime.Time(); !ok || !got.Equal(time.Unix(1672531200, 0)) {
		t.Errorf("PhotoTakenTime = %v, %v", got, ok)
	}
	if _, ok := m.CreationTime.Time(); ok {
		t.Error("a zero timestamp means unknown")
	}
}

func TestTimestamp_Invalid(t *testing.T) {
	for _, v := range []string{"", "abc", "-5", "0"} {
		if _, ok := (Timestamp{Timestamp: v}).Time(); ok {
			t.Errorf("Timestamp %q should be rejected", v)
		}
	}
}

func TestReadMetadata_BadJSON(t *testing.T) {
	if _, err := ReadMetadata(strings.NewReader("{not json")); err == nil {
		t.Error("expected a decode error")
	}
}

func TestReadMetadata_Extras(t *testing.T) {
	m, err := ReadMetadata(strings.NewReader(`{
		"description": "Eiffel Tower",
		"geoData": {"latitude": 0.0, "longitude": 0.0, "altitude": 0.0},
		"geoDataExif": {"latitude": 48.8584, "longitude": 2.2945, "altitude": 35.5},
		"people": [{"name": "Alice"}, {"name": ""}, {"name": "Bob"}],
		"favorited": true
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if g, ok := m.Location(); !ok || g.Latitude != 48.8584 || g.Longitude != 2.2945 || g.Altitude != 35.5 {
		t.Errorf("Location() = %+v, %v; want geoDataExif when geoData is 0, 0", g, ok)
	}
	if got := m.PeopleNames(); len(got) != 2 || got[0] != "Alice" || got[1] != "Bob" {
		t.Errorf("PeopleNames() = %q", got)
	}
	if !m.Favorited || m.Description != "Eiffel Tower" {
		t.Errorf("Favorited = %v, Description = %q", m.Favorited, m.Description)
	}
}

func TestMetadata_LocationPrefersGeoData(t *testing.T) {
	m := Metadata{GeoData: GeoData{Latitude: 1, Longitude: 2}, GeoDataExif: GeoData{Latitude: 3, Longitude: 4}}
	if g, ok := m.Location(); !ok || g.Latitude != 1 {
		t.Errorf("Location() = %+v, %v", g, ok)
	}
	if _, ok := (Metadata{}).Location(); ok {
		t.Error("no location should be reported for 0, 0")
	}
}
