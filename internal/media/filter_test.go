package media

import "testing"

func TestDefaultFilter_Match(t *testing.T) {
	f := DefaultFilter()
	tests := []struct {
		name string
		want bool
	}{
		{"test.jpg", true},
		{"test.png", true},
		{"test.MP4", true},
		{"test.txt", false},
		{"test.json", false},
		{"._test.jpg", false},
		{"__MACOSX/._test.jpg", false},
		{"Takeout/__MACOSX/Trip/a.jpg", false},
		{"Takeout/NotMACOSX/a.jpg", true}, // only a whole path component counts
		{"a__MACOSX_b.jpg", true},
	}
	for _, tc := range tests {
		if got := f.Match(tc.name); got != tc.want {
			t.Errorf("Match(%q) = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestFilter_MatchInIgnoresRootName(t *testing.T) {
	root := "/photos/__MACOSX_backup/__MACOSX"
	if !DefaultFilter().MatchIn(root, root+"/a.jpg") {
		t.Error("the scan root's own name must not affect the result")
	}
}

func TestNewFilter_IncludeExclude(t *testing.T) {
	f, err := NewFilter([]string{"DNG", ".arw"}, []string{".gif", "MKV"})
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]bool{
		"raw.dng": true, "raw.ARW": true, "a.jpg": true,
		"anim.gif": false, "clip.mkv": false,
	} {
		if got := f.Match(name); got != want {
			t.Errorf("Match(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestNormalizeExt(t *testing.T) {
	for in, want := range map[string]string{"jpg": ".jpg", ".JPG": ".jpg", " heic ": ".heic"} {
		if got, err := NormalizeExt(in); err != nil || got != want {
			t.Errorf("NormalizeExt(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"", ".", "a.b", "a/b", `a\b`} {
		if _, err := NormalizeExt(bad); err == nil {
			t.Errorf("NormalizeExt(%q) should fail", bad)
		}
	}
}

func TestHasEXIF(t *testing.T) {
	for name, want := range map[string]bool{
		"test.jpg": true, "test.JPEG": true, "test.tiff": true, "test.heic": true,
		"test.png": false, "test.mp4": false,
	} {
		if got := HasEXIF(name); got != want {
			t.Errorf("HasEXIF(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestHasMP4Duration(t *testing.T) {
	for name, want := range map[string]bool{
		"video.mp4": true, "video.MP4": true, "video.mov": true, "video.MOV": true,
		"image.jpg": false, "video.avi": false,
	} {
		if got := HasMP4Duration(name); got != want {
			t.Errorf("HasMP4Duration(%q) = %v, want %v", name, got, want)
		}
	}
}
