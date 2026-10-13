// Package media knows about media files themselves, independent of where they come
// from or where they end up:
//
//   - which file names count as media ([Filter]),
//   - reading a capture date out of EXIF headers ([EXIFDate]) or a file name
//     ([DateFromFilename]),
//   - reading a video's duration from its MP4/QuickTime container ([MP4Duration]).
//
// Nothing here touches the photo library or knows about Google Takeout.
package media
