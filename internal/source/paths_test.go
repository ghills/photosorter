package source

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPathsOverlap(t *testing.T) {
	root := t.TempDir()
	mk := func(rel string) string {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(p, 0755); err != nil {
			t.Fatal(err)
		}
		return p
	}
	lib := mk("lib")
	inner := mk("lib/2023")
	sibling := mk("libx") // shares a string prefix with "lib" but is a different folder
	other := mk("other")

	link := filepath.Join(root, "link-to-inner")
	if err := os.Symlink(inner, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	tests := []struct {
		name string
		a, b string
		want bool
	}{
		{"same folder", lib, lib, true},
		{"child inside parent", lib, inner, true},
		{"parent inside child (argument order)", inner, lib, true},
		{"not-yet-created child inside parent", lib, filepath.Join(lib, "new", "deeper"), true},
		{"not-yet-created folder elsewhere", lib, filepath.Join(other, "new"), false},
		{"string-prefix sibling", lib, sibling, false},
		{"unrelated folders", lib, other, false},
		{"symlink into the folder", lib, link, true},
		{"relative spelling of same folder", lib, filepath.Join(lib, "2023", ".."), true},
		{"neither exists", filepath.Join(root, "x"), filepath.Join(root, "y"), false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := pathsOverlap(tc.a, tc.b); got != tc.want {
				t.Errorf("pathsOverlap(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
			}
		})
	}
}
