# PhotoSorter

A CLI tool for organizing Google Photos Takeout archives.

PhotoSorter is a Go application designed to extract, deduplicate, and chronologically organize media files from Google Photos Takeout `.zip` archives. 

Instead of dealing with directories containing JSON sidecars, duplicate images, and disorganized files, PhotoSorter processes data directly from the Takeout archive and organizes it chronologically into folders.

## Features

- **Direct ZIP Processing**: Processes files from the Takeout zip archive without requiring manual extraction to disk.
- **Date Detection Fallbacks**: Determines the capture date of media using a fallback strategy:
  1. **EXIF Metadata**: Reads EXIF headers directly from the zip stream, reading only the first 256KB of the file.
  2. **JSON Sidecars**: Parses Google Photos metadata sidecar files (`.json`) for `photoTakenTime` or `creationTime` timestamps if EXIF is not found.
  3. **Filename Parsing**: Extracts dates via regex from common camera filename patterns (e.g., `IMG_20230514_...`).
- **Deduplication**: Uses a persistent SQLite database (`.photosorter_cache.db`) and `xxhash` to detect and skip duplicates to minimize disk writes. The database is optimized with WAL mode and prepared statements for better performance.
- **Smart Library Synchronization**: Can gracefully reconcile with an existing output folder that contains organized media by hashing existing files before processing a new Takeout zip.
- **Chronological Output**: Routes files into `YYYY-MM-DD` folders (e.g., `2023-05-14/`). Files lacking detectable dates are placed in an `Unknown_Date/` folder.
- **Broad Media Support**: Supports common image and video formats. Automatically ignores macOS hidden system files and resource forks.
- **Progress Reporting**: Provides a terminal progress bar and a final summary report of processed files, as well as the total number of tracked files in the database.

## Installation

Ensure you have [Go](https://go.dev/) installed.

```bash
git clone https://github.com/ghills/photosorter.git
cd photosorter
go build -o photosorter .
```

## Usage

Provide the paths to your Takeout Zip and the destination folder:

```bash
./photosorter -zip <path_to_takeout.zip> -out <destination_folder>
```

### Syncing an Existing Library

If you already have a folder organized with photos and videos, you can synchronize its contents with the database using the `-sync` flag. This prevents duplicates if you process a new Takeout zip containing overlap:

```bash
./photosorter -out <destination_folder> -sync
```

*(Note: If you run the standard zip processing command, PhotoSorter will automatically run a sync check if it detects more physical media files in the destination than are recorded in the database.)*

### Example

Organize an exported Takeout archive into a `MyPhotos` directory:

```bash
./photosorter -zip takeout-20231012T000000Z-001.zip -out ./MyPhotos
```

## How It Works

PhotoSorter processes the archive in two parts to reduce disk IO operations:
- **Pass 1 (Hash Verification)**: The tool streams the target file from the zip archive into memory to compute its `xxhash`. It verifies this hash against the SQLite database. If the file is a duplicate, no disk extraction occurs.
- **Pass 2 (Extraction)**: If the file is unique, the ZIP stream is read again, and the file is written to its proper chronological destination path.

## Development and Testing

The project includes thoroughly written unit tests covering database operations, metadata parsing, and file processing logic. You can run the test suite using standard Go commands:

```bash
go test ./...
```

## Core Dependencies

- [cespare/xxhash](https://github.com/cespare/xxhash) - Hashing
- [schollz/progressbar](https://github.com/schollz/progressbar) - Terminal progress interface
- [rwcarlsen/goexif](https://github.com/rwcarlsen/goexif) - EXIF header extraction
- [glebarez/go-sqlite](https://github.com/glebarez/go-sqlite) - CGO-Free SQLite database driver
