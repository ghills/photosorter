package xmp

import (
	"encoding/xml"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestEncode(t *testing.T) {
	ny, _ := time.LoadLocation("America/New_York")
	doc := string(Encode(Fields{
		Taken:       time.Date(2023, 5, 14, 18, 30, 0, 0, ny),
		GPS:         GPS{Latitude: 40.6892, Longitude: -74.0445, Altitude: 93.5},
		HasGPS:      true,
		Description: `Liberty & "friends" <3`,
		People:      []string{"Alice", "Bob"},
		Rating:      5,
	}))
	for _, want := range []string{
		"<exif:DateTimeOriginal>2023-05-14T18:30:00-04:00</exif:DateTimeOriginal>",
		"<photoshop:DateCreated>2023-05-14T18:30:00-04:00</photoshop:DateCreated>",
		"<exif:GPSLatitude>40,41.352000N</exif:GPSLatitude>",
		"<exif:GPSLongitude>74,2.670000W</exif:GPSLongitude>",
		"<exif:GPSAltitudeRef>0</exif:GPSAltitudeRef>",
		"<exif:GPSAltitude>9350/100</exif:GPSAltitude>",
		"<xmp:Rating>5</xmp:Rating>",
		`<rdf:li xml:lang="x-default">Liberty &amp; &#34;friends&#34; &lt;3</rdf:li>`,
		"<rdf:li>Alice</rdf:li>",
		"<rdf:li>Bob</rdf:li>",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("missing %s in\n%s", want, doc)
		}
	}
	if err := xml.Unmarshal([]byte(doc), new(struct{})); err != nil {
		t.Errorf("not well-formed XML: %v\n%s", err, doc)
	}
}

func TestEncode_LeavesOutZeroValues(t *testing.T) {
	doc := string(Encode(Fields{GPS: GPS{Latitude: -33.8568, Longitude: 151.2153}, HasGPS: true}))
	if !strings.Contains(doc, "<exif:GPSLatitude>33,51.408000S</exif:GPSLatitude>") {
		t.Errorf("southern latitude:\n%s", doc)
	}
	for _, absent := range []string{"DateTimeOriginal", "GPSAltitude", "Rating", "dc:description>", "PersonInImage>"} {
		if strings.Contains(doc, absent) {
			t.Errorf("%s should be left out:\n%s", absent, doc)
		}
	}
}

func TestFields_Empty(t *testing.T) {
	if !(Fields{}).Empty() {
		t.Error("zero Fields should be empty")
	}
	if (Fields{Rating: 5}).Empty() {
		t.Error("a rating is something to write")
	}
}

func TestWriteSidecar(t *testing.T) {
	media := filepath.Join(t.TempDir(), "IMG_1234.jpg")
	sc, err := WriteSidecar(media, Fields{Description: "first"})
	if err != nil {
		t.Fatal(err)
	}
	if sc != media+".xmp" {
		t.Errorf("sidecar path = %s", sc)
	}
	if _, err := WriteSidecar(media, Fields{Description: "second"}); !os.IsExist(err) {
		t.Errorf("second write: err = %v, want an exists error", err)
	}
	if b, _ := os.ReadFile(sc); !strings.Contains(string(b), "first") {
		t.Errorf("existing sidecar was replaced:\n%s", b)
	}
}

// Minutes that round up to 60 carry into the degrees.
func TestEncode_CoordinateCarriesRoundedMinutes(t *testing.T) {
	doc := string(Encode(Fields{GPS: GPS{Latitude: 48.999999999, Longitude: -2.999999999}, HasGPS: true}))
	for _, want := range []string{
		"<exif:GPSLatitude>49,0.000000N</exif:GPSLatitude>",
		"<exif:GPSLongitude>3,0.000000W</exif:GPSLongitude>",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("missing %s in\n%s", want, doc)
		}
	}
}
