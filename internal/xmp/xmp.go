// Package xmp writes XMP sidecar files: metadata stored next to a media file, as
// IMG_1234.jpg.xmp, instead of inside it. digiKam, darktable, ExifTool and others read
// sidecars named this way, and the media file itself is never modified.
package xmp

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"math"
	"os"
	"strconv"
	"time"
)

// Ext is appended to a media file's full name to name its sidecar.
const Ext = ".xmp"

// SidecarPath returns the sidecar path for the media file at path.
func SidecarPath(path string) string { return path + Ext }

// Fields are the values a sidecar can carry. Zero values are left out.
type Fields struct {
	// Taken is the capture time, written as exif:DateTimeOriginal and
	// photoshop:DateCreated with its UTC offset.
	Taken time.Time

	// GPS is the capture location, if HasGPS is set.
	GPS    GPS
	HasGPS bool

	// Description is written as dc:description.
	Description string

	// People are written as Iptc4xmpExt:PersonInImage.
	People []string

	// Rating is written as xmp:Rating, from 1 to 5.
	Rating int
}

// GPS is a location in decimal degrees and metres above sea level. An altitude of 0
// is treated as unknown and left out.
type GPS struct {
	Latitude, Longitude, Altitude float64
}

// Empty reports whether f has nothing to write.
func (f Fields) Empty() bool {
	return f.Taken.IsZero() && !f.HasGPS && f.Description == "" && len(f.People) == 0 && f.Rating == 0
}

// Encode returns f as an XMP document.
func Encode(f Fields) []byte {
	var b bytes.Buffer
	b.WriteString(`<x:xmpmeta xmlns:x="adobe:ns:meta/" x:xmptk="PhotoSorter">
 <rdf:RDF xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#">
  <rdf:Description rdf:about=""
    xmlns:dc="http://purl.org/dc/elements/1.1/"
    xmlns:exif="http://ns.adobe.com/exif/1.0/"
    xmlns:photoshop="http://ns.adobe.com/photoshop/1.0/"
    xmlns:xmp="http://ns.adobe.com/xap/1.0/"
    xmlns:Iptc4xmpExt="http://iptc.org/std/Iptc4xmpExt/2008-02-29/">
`)
	prop := func(name, value string) {
		fmt.Fprintf(&b, "   <%s>%s</%s>\n", name, escape(value), name)
	}
	if !f.Taken.IsZero() {
		t := f.Taken.Format(time.RFC3339)
		prop("exif:DateTimeOriginal", t)
		prop("photoshop:DateCreated", t)
	}
	if f.HasGPS {
		prop("exif:GPSVersionID", "2.3.0.0")
		prop("exif:GPSLatitude", coordinate(f.GPS.Latitude, 'N', 'S'))
		prop("exif:GPSLongitude", coordinate(f.GPS.Longitude, 'E', 'W'))
		if alt := f.GPS.Altitude; alt != 0 {
			ref := "0" // above sea level
			if alt < 0 {
				ref = "1"
			}
			prop("exif:GPSAltitudeRef", ref)
			prop("exif:GPSAltitude", strconv.FormatInt(int64(math.Round(math.Abs(alt)*100)), 10)+"/100")
		}
	}
	if f.Rating > 0 {
		prop("xmp:Rating", strconv.Itoa(min(f.Rating, 5)))
	}
	if f.Description != "" {
		fmt.Fprintf(&b, "   <dc:description>\n    <rdf:Alt>\n     <rdf:li xml:lang=\"x-default\">%s</rdf:li>\n    </rdf:Alt>\n   </dc:description>\n", escape(f.Description))
	}
	if len(f.People) > 0 {
		b.WriteString("   <Iptc4xmpExt:PersonInImage>\n    <rdf:Bag>\n")
		for _, p := range f.People {
			fmt.Fprintf(&b, "     <rdf:li>%s</rdf:li>\n", escape(p))
		}
		b.WriteString("    </rdf:Bag>\n   </Iptc4xmpExt:PersonInImage>\n")
	}
	b.WriteString("  </rdf:Description>\n </rdf:RDF>\n</x:xmpmeta>\n")
	return b.Bytes()
}

// coordinate formats a decimal-degree value the way XMP stores GPS coordinates:
// "DDD,MM.mmmmmmK", where K is pos for values ≥ 0 and neg otherwise.
func coordinate(deg float64, pos, neg byte) string {
	ref := pos
	if deg < 0 {
		ref, deg = neg, -deg
	}
	whole := math.Floor(deg)
	return fmt.Sprintf("%d,%.6f%c", int(whole), (deg-whole)*60, ref)
}

func escape(s string) string {
	var b bytes.Buffer
	xml.EscapeText(&b, []byte(s)) // only fails if the writer does
	return b.String()
}

// WriteSidecar writes f as the sidecar for the media file at path and returns the
// sidecar's path. An existing sidecar is never replaced: that is an error.
func WriteSidecar(path string, f Fields) (string, error) {
	sc := SidecarPath(path)
	out, err := os.OpenFile(sc, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		return "", err
	}
	_, err = out.Write(Encode(f))
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(sc)
		return "", err
	}
	return sc, nil
}
