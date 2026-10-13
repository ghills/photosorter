// Package takeout holds what PhotoSorter knows about the layout of Google Takeout
// exports:
//
//   - [SidecarIndex] pairs each media file with the JSON metadata file ("sidecar")
//     Google writes next to it, despite the many ways Takeout mangles sidecar names.
//   - [Metadata] decodes a sidecar, exposing when the photo was taken.
//   - [AlbumName] recognises user-created album folders inside "Google Photos/".
//
// A Takeout export may be split into several zip parts, and a sidecar does not always
// land in the same part as its media. One SidecarIndex is therefore shared by every
// input source in a run.
package takeout
