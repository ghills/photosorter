// Package media knows about media files themselves, independent of where they come
// from or where they end up:
//
//   - which file names count as media ([Filter]),
//   - reading a capture date out of EXIF data in JPEG, TIFF or HEIC files
//     ([EXIFDate]) or a file name ([DateFromFilename]),
//   - reading a video's duration and recording time from its MP4/QuickTime
//     container ([ReadMovieHeader]).
//
// Nothing here touches the photo library or knows about Google Takeout.
package media
