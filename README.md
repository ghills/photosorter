# PhotoSorter

*A high-performance Google Photos Takeout Organizer.*

PhotoSorter is a Go CLI application securely designed to extract, deduplicate, and chronically organize media files directly from Google Photos Takeout `.zip` archives. 

Instead of dealing with messy directories riddled with JSON sidecars, duplicate images, and disorganized files, PhotoSorter streams data from your Takeout archive and organizes everything chronologically into folders, saving you significant time and disk space.

## Features

- **Direct ZIP Processing**: Streams files straight from your Takeout zip—no need to manually extract massive gigabyte archives to disk first.
- **Intelligent Date Detection**: Automatically determines the exact capture date of media utilizing a robust fallback strategy:
  1. **EXIF Metadata**: Reads native EXIF headers directly from the zip stream. It's optimized for speed, actively reading only the first 256KB of the file to locate headers.
  2. **JSON Sidecars**: Parses Google Photos metadata sidecar files (`.json`) for `photoTakenTime` or `creationTime` timestamps if EXIF is missing.
  3. **Filename Parsing**: If all else fails, it cleanly extracts dates via regex from common camera filename patterns (e.g., `IMG_20230514_...`).
- **Zero-Write Deduplication**: Implements a persistent SQLite database (`.photosorter_cache.db`) and blazingly fast `xxhash` hashing to detect and skip duplicates *before* allocating disk IO to write them.
- **Clean Chronological Output**: Routes files beautifully into `YYYY-MM-DD` folders (e.g., `2023-05-14/`). Tricky files lacking detectable dates safely funnel into an `Unknown_Date/` folder.
- **Broad Media Support**: Supports common images (.jpg, .heic, .png, etc.) and wide video architectures (.mp4, .mov, .mkv, .webm, etc.).

## Installation

Ensure you have [Go](https://go.dev/) installed. Clone the repository, navigate inside, and build the runnable binary:

```bash
git clone https://github.com/ghills/photosorter.git
cd photosorter
go build -o photosorter .
```

## Usage

Provide the required inputs pointing to your Takeout Zip, and where you want your media to go:

```bash
./photosorter -zip <path_to_takeout.zip> -out <destination_folder>
```

### Example

Organize an exported Takeout archive explicitly into a fresh `MyPhotos` directory:

```bash
./photosorter -zip takeout-20231012T000000Z-001.zip -out ./MyPhotos
```

PhotoSorter features a rich, real-time terminal progress bar, finishing its execution with a deeply detailed extraction summary and processing report.

## How It Works (Two-Pass Engine)

PhotoSorter's core design goal optimizes heavily to reduce unnecessary disk IO operations:
- **Pass 1 (Hash Verification)**: The tool streams the target file from the zip archive actively into memory to deduce its `xxhash`. It verifies this hash against the persistent SQLite cache layer. If marked as a duplicate, the file avoids disk extraction altogether, generating exactly zero disk writes.
- **Pass 2 (Extraction)**: If the file is deemed unique, the ZIP streams the specific file data a second time, directly funneling it gracefully into its proper chronologically organized destination path.

## Core Dependencies

- [cespare/xxhash](https://github.com/cespare/xxhash) - Highly performant hashing
- [schollz/progressbar](https://github.com/schollz/progressbar) - Terminal UI and Progress
- [rwcarlsen/goexif](https://github.com/rwcarlsen/goexif) - EXIF header extraction
- [glebarez/go-sqlite](https://github.com/glebarez/go-sqlite) - CGO-Free SQLite database driver
