package takeout

import "testing"

func TestAlbumName(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{"Takeout/Google Photos/Hawaii Trip 2023/IMG_1234.jpg", "Hawaii Trip 2023"},
		{"Takeout/Google Photos/Photos from 2023/IMG_1234.jpg", ""},
		{"Takeout/Google Photos/2023-01-01/IMG_1234.jpg", ""},
		{"Takeout/Google Photos/Untitled Album/video.mp4", "Untitled Album"},
		{"SomeOtherFolder/IMG_1234.jpg", ""},
		{"Google Photos/Album at Root/file.png", "Album at Root"},
		{"Google Photos/IMG_1234.jpg", ""}, // file directly in Google Photos
		{"Google Photos/../IMG_1234.jpg", ""},
	}
	for _, tc := range tests {
		if got := AlbumName(tc.path); got != tc.want {
			t.Errorf("AlbumName(%q) = %q, want %q", tc.path, got, tc.want)
		}
	}
}
