# PhotoSorter

A CLI tool for organizing Google Photos Takeout archives.

PhotoSorter is a Go application designed to extract, deduplicate, and chronologically organize media files from Google Photos Takeout `.zip` archives. 

Instead of dealing with directories containing JSON sidecars, duplicate images, and disorganized files, PhotoSorter processes data directly from the Takeout archive and organizes it chronologically into folders.

## Features

- **Multiple Input Sources**: Processes files directly from Takeout zip archives without requiring manual extraction to disk, or processes standard media folders on your local filesystem.
- **Date Detection Fallbacks**: Determines the capture date of media using a fallback strategy:
  1. **EXIF Metadata**: Reads EXIF headers directly from the zip stream, reading only the first 256KB of the file.
  2. **JSON Sidecars**: Parses Google Photos metadata sidecar files (`.json`) for `photoTakenTime` or `creationTime` timestamps if EXIF is not found. Matching understands Takeout's naming quirks: `.supplemental-metadata.json`, names truncated to fit Takeout's length limit, duplicate counters (`IMG_1(1).jpg` → `IMG_1.jpg(1).json`), `-edited` copies, and sidecars stored in a different zip part than their media.
  3. **Filename Parsing**: Extracts dates via regex from common camera filename patterns (e.g., `IMG_20230514_...`).
- **Deduplication**: Uses a persistent SQLite database (`.photosorter_cache.db`) and `xxhash` to detect and skip duplicates to minimize disk writes. The database is optimized with WAL mode and prepared statements for better performance.
- **Smart Library Synchronization**: Can gracefully reconcile with an existing output folder that contains organized media by hashing existing files before processing a new Takeout zip.
- **Chronological Output**: Routes files into `YYYY-MM-DD` folders (e.g., `2023-05-14/`). Files lacking detectable dates are placed in an `Unknown_Date/` folder.
- **Album Preservation**: Detects Google Photos Album organization within the Takeout zip and preserves it by routing those files to an `Albums/<Album Name>/` directory instead of generic date folders.
- **Concurrent Processing**: Configurable goroutine worker pool (`-workers`) for rapid multi-threaded file hashing and extraction.
- **Dry-Run Mode**: Safely test an extraction pass with `-dry-run` to view what actions would be taken without writing files to disk.
- **Broad Media Support**: Supports common image and video formats. Automatically ignores macOS hidden system files and resource forks.
- **Progress Reporting & Logging**: Provides a terminal progress bar, a final summary report, and outputs detailed logs to a file (`photosorter_*.log`) alongside the database.

## Installation

Ensure you have [Go](https://go.dev/) installed.

```bash
git clone https://github.com/ghills/photosorter.git
cd photosorter
go build -o photosorter .
```

## Usage

Provide the path to your Takeout Zip (or a local media directory) and the destination folder. You can use glob patterns to process multiple zip files sequentially:

```bash
./photosorter -zip "takeout-*.zip" -out <destination_folder>
```

Alternatively, to process a local directory of media files instead of a zip file:

```bash
./photosorter -in /path/to/my/media -out <destination_folder>
```

### Advanced Flags

- `-in <path>`: Process an existing folder on your local filesystem instead of a zip file.

- `-workers <number>`: Number of concurrent workers for processing (default 8).
- `-dry-run`: Run a simulation of extraction without writing any files or database records. Duplicates within the run are still detected.
- `-tz <name>`: IANA timezone (e.g. `America/New_York`) used to turn capture timestamps into `YYYY-MM-DD` folders. Defaults to this machine's timezone. Set it when running somewhere other than where the photos were taken (a server or container in UTC, for example), so photos taken in the evening don't land in the next day's folder.
- `-nosync`: Prevent automatic library sync from running.
- `-skip-short-videos <seconds>`: Skip importing `mp4` and `mov` videos shorter than or equal to this duration (default 3). This is useful for ignoring small 1-2s "motion photos" without importing them as standalone videos.

### Exit Codes

| Code | Meaning |
| --- | --- |
| `0` | Everything was processed |
| `1` | A fatal error, or the run finished but some files could not be processed (see the log) |
| `130` | Interrupted with Ctrl-C or SIGTERM. Finished files were recorded, so re-run to continue |

`-in` and `-out` must not contain one another; PhotoSorter refuses to start if they do.

### Syncing an Existing Library

If you already have a folder organized with photos and videos, you can synchronize its contents with the database using the `-sync` flag. This prevents duplicates if you process a new Takeout zip containing overlap:

```bash
./photosorter -out <destination_folder> -sync
```

*(Note: If you run the standard zip processing command, PhotoSorter will automatically run a sync check if it detects more physical media files in the destination than are recorded in the database. You can disable this automatic check by passing the `-nosync` flag.)*

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
