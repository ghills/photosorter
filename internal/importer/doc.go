// Package importer copies media from sources into the library, skipping content the
// library already has and filing everything else into dated or album folders.
//
// For each file, [Importer.Run] does the following (see importFile):
//
//  1. Hash the content. Small files are read into memory once, and the later steps
//     reuse that copy (see BufferLimit).
//  2. Claim the hash in the library database. If it is already there, the file is a
//     duplicate and nothing is written. If another worker is importing the same
//     content, wait for it: if that import fails, this file takes over.
//  3. Optionally skip very short videos, such as the clips behind motion photos.
//     They are not recorded, so a lower threshold later can still import them.
//  4. Work out when it was taken: EXIF, then the Takeout sidecar's photoTakenTime,
//     then a video's own recording time, then the sidecar's creationTime, then a
//     date in the file name. For a manual import, a forced date replaces all of
//     these and a fallback date fills in when they fail.
//  5. Choose a folder with [Layout]: an album folder, a date folder (optionally
//     named after an event), or the unknown-date folder.
//  6. Copy the file under a unique name, through a temp file so a failure never
//     leaves a partial file behind, then record the hash.
//
// Files are processed concurrently. One file's failure is logged and counted but does
// not stop the run. Cancelling the context, or a database write failure, stops the
// run cleanly.
package importer
