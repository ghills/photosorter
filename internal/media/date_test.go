package media

import (
	"bytes"
	"testing"
	"time"
)

func TestDateFromFilename(t *testing.T) {
	tests := []struct {
		filename string
		want     string // YYYY-MM-DD, "" for no match
	}{
		{"IMG_20231015_123456.jpg", "2023-10-15"},
		{"VID_2022-12-05.mp4", "2022-12-05"},
		{"WhatsApp Image 2021-01-01 at 12.00.00.jpg", "2021-01-01"},
		{"20201122_123.jpg", "2020-11-22"},
		{"Screenshot_2019-05-16_123.png", "2019-05-16"},
		{"some/dir/IMG_20231015.jpg", "2023-10-15"},
		{"random_file_name.jpg", ""},
		{"20231345_invalid_month.jpg", ""},
	}
	loc, _ := time.LoadLocation("Pacific/Honolulu")
	for _, tc := range tests {
		t.Run(tc.filename, func(t *testing.T) {
			got, ok := DateFromFilename(tc.filename, loc)
			if ok != (tc.want != "") {
				t.Fatalf("found = %v, want %v", ok, tc.want != "")
			}
			if ok && (got.Format("2006-01-02") != tc.want || got.Location() != loc) {
				t.Errorf("got %v, want %s in %v", got, tc.want, loc)
			}
		})
	}
}

func TestEXIFDate_NotEXIF(t *testing.T) {
	if _, ok := EXIFDate(bytes.NewReader([]byte("not a jpeg"))); ok {
		t.Error("expected no date from non-EXIF data")
	}
}
