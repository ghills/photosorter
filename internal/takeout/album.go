package takeout

import (
	"path/filepath"
	"regexp"
	"strings"
)

// photosFolder is the Takeout folder that holds the Google Photos export.
const photosFolder = "Google Photos"

// dateFolder matches the YYYY-MM-DD folders older Takeout exports use instead of albums.
var dateFolder = regexp.MustCompile(`^(?:19|20)\d{2}-\d{2}-\d{2}$`)

// AlbumName returns the user-created album a Takeout file belongs to, or "" if it
// is not in one.
//
// Takeout places media at "Takeout/Google Photos/<folder>/<file>". <folder> is an
// album unless it is one of the folders Google generates itself: "Photos from 2023"
// style year folders and YYYY-MM-DD date folders. Names that are not usable as a
// single folder name (".", "..") are ignored.
func AlbumName(path string) string {
	parts := strings.Split(filepath.ToSlash(path), "/")
	// Need "Google Photos", the folder, and the file name.
	for i := 0; i < len(parts)-2; i++ {
		if parts[i] != photosFolder {
			continue
		}
		folder := parts[i+1]
		switch {
		case strings.HasPrefix(folder, "Photos from "),
			dateFolder.MatchString(folder),
			folder == "", folder == ".", folder == "..":
			continue
		}
		return folder
	}
	return ""
}
