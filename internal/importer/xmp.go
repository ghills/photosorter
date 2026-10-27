package importer

import (
	"photosorter/internal/takeout"
	"photosorter/internal/xmp"
)

// xmpFields returns what an XMP sidecar should carry over from the Takeout sidecar
// meta for a file filed under date. It is empty unless the date came from meta: a file
// dated any other way kept its own metadata, or never had a sidecar to use.
//
// The capture time is written only when it is photoTakenTime. A creationTime is when
// the item was uploaded, which is a usable folder date but not a capture time.
func xmpFields(meta takeout.Metadata, date dated) xmp.Fields {
	if !date.fromSidecar() {
		return xmp.Fields{}
	}
	f := xmp.Fields{
		Description: meta.Description,
		People:      meta.PeopleNames(),
	}
	if date.src == DateFromJSONTaken {
		f.Taken = date.t
	}
	if g, ok := meta.Location(); ok {
		f.GPS = xmp.GPS{Latitude: g.Latitude, Longitude: g.Longitude, Altitude: g.Altitude}
		f.HasGPS = true
	}
	if meta.Favorited {
		f.Rating = 5
	}
	return f
}
