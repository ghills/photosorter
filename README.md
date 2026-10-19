# PhotoSorter

PhotoSorter imports [Google Takeout](https://takeout.google.com/) exports of Google Photos into a plain folder-based photo library on your own disk.

- **No duplicates.** Every file in the library is identified by a hash of its content. Anything already in the library is skipped, whether it was imported before, came from an earlier Takeout, or was put there by hand.
- **Correct dates.** Each file goes into a dated folder based on its EXIF data, Google's JSON sidecar files, or failing those, the date in its file name. Google often strips EXIF from exported media, so the sidecars matter.
- **Albums kept.** Media from your Google Photos albums can stay grouped as albums.
- **Works on the zips directly.** No need to extract multi-gigabyte archives first. Extracted folders work too.

## Download

Ready-to-run builds of the latest `main` are on the [releases page](https://github.com/ghills/photosorter/releases/latest). No Go install needed.

| Platform | File |
|---|---|
| Windows (most PCs) | [`photosorter-windows-amd64.zip`](https://github.com/ghills/photosorter/releases/latest/download/photosorter-windows-amd64.zip) |
| Windows on ARM | [`photosorter-windows-arm64.zip`](https://github.com/ghills/photosorter/releases/latest/download/photosorter-windows-arm64.zip) |
| macOS (Intel and Apple silicon) | [`photosorter-macos-universal.tar.gz`](https://github.com/ghills/photosorter/releases/latest/download/photosorter-macos-universal.tar.gz) |

PhotoSorter is a command-line program: unpack the download and run it from a terminal.

- **Windows:** extract the zip, open PowerShell in that folder, and run `.\photosorter.exe -h`. The builds aren't code-signed, so SmartScreen may warn the first time; choose **More info → Run anyway**.
- **macOS:** double-click the `.tar.gz` to unpack it, then in Terminal, from that folder:

  ```bash
  xattr -d com.apple.quarantine photosorter
  ./photosorter -h
  ```

  The `xattr` step clears the "downloaded from the internet" flag; without it macOS refuses to open an unsigned program.

Each merge to `main` publishes a new build, with a `SHA256SUMS.txt` to verify downloads.

## Quick start

To build from source instead, you need [Go](https://go.dev/) 1.23 or newer.

```bash
git clone https://github.com/ghills/photosorter.git
cd photosorter
go build -o photosorter .
```

Import every part of a Takeout export into `~/Pictures/Library`:

```bash
./photosorter -out ~/Pictures/Library ~/Downloads/takeout-*.zip
```

See what would happen first, without writing anything:

```bash
./photosorter -out ~/Pictures/Library -dry-run ~/Downloads/takeout-*.zip
```

Pass every part of a multi-part export in the same run. A sidecar sometimes ends up in a different zip from the photo it describes, and PhotoSorter can only pair them when it sees both.

## The resulting library

With the default settings:

```
Library/
├── 2023/
│   ├── 2023-05-14/            IMG_1234.jpg, VID_5678.mp4, ...
│   └── 2023-05-15/
├── Albums/
│   └── Hawaii Trip/           media from a Google Photos album
├── Unknown_Date/              files whose date could not be determined
├── .photosorter_cache.db      hash database (see "Duplicate detection")
├── .photosorter-tmp/          in-progress copies; cleared at the start of each import
└── photosorter_20231012_120000.log   per-run detail log (dry runs log to the system temp folder)
```

If a name is already taken in the destination folder, the new file gets a suffix (`IMG_1234_1.jpg`). Existing files are never overwritten. The folder names and the album behaviour are configurable (see [Configuration](#configuration)).

### Reorganizing an existing library

Older versions put every dated folder at the top of the library (`2023-05-14/`). To move them into year folders, run a **reorganize**:

```bash
./photosorter -out ~/Pictures/Library -reorganize
```

Each folder at the top of the library goes into the folder for its year, along with everything inside it. Folders you renamed keep their names: `2025-10-02 foo bar/` becomes `2025/2025-10-02 foo bar/`.

- **Folders whose name starts with a year** (`2023-05-14`, `2023 Christmas`) go to that year.
- **Other folders** (`Road trip/`) are dated by the media inside them: EXIF first, then the date in the file name. A folder is moved only if every media file in it has a date and they all fall in the same year. Otherwise it stays where it is, and the run says why.
- **Left alone:** year folders, the unknown-date and albums folders, hidden folders, and loose files.

If the target folder already exists, for example because an import already used the new layout, the two are merged. Nothing is overwritten. A file whose name and content are both already in the target is removed. A file whose name is taken by different content gets a `_N` suffix.

The hash database tracks content, not folders, so moving files doesn't invalidate it. Renamed files get their database entry updated, and the library's file count drops by the number of duplicates removed.

Reorganizing is safe to repeat. Folders already moved are not moved again, and running it again finishes a merge that was interrupted. Add `-dry-run` to see what would move first. Every move is written to the run's log.

## How dates are found

For each file, PhotoSorter uses the first of these that works:

1. **EXIF**: `DateTimeOriginal` from JPEG, HEIC and TIFF headers. Only the first 256 KB is read.
2. **JSON sidecar `photoTakenTime`**: the capture time Google recorded.
3. **JSON sidecar `creationTime`**: the upload time. This is a last resort before guessing.
4. **File name**: patterns such as `IMG_20230514_…`, `VID-2023-05-14…`, `Screenshot_2023-05-14…` and `WhatsApp Image 2023-05-14…`.

If none of them works, the file goes to `Unknown_Date/`.

Sidecar timestamps are absolute instants, so the day they fall on depends on the time zone. PhotoSorter uses the machine's zone unless you pass `-tz`. EXIF times are wall-clock times and are used exactly as recorded.

### Sidecar matching

Takeout is inconsistent about naming sidecars. For `IMG_1234.jpg`, PhotoSorter looks for each of these:

| Sidecar name | Why it exists |
| --- | --- |
| `IMG_1234.jpg.json` | Older exports |
| `IMG_1234.jpg.supplemental-metadata.json` | Newer exports |
| `IMG_1234.json` | Named after the stem |
| `IMG_1234.jpg.supplem.json` | Truncated to Takeout's 51-character limit |
| `IMG_1234.jpg(1).json` | Describes the duplicate `IMG_1234(1).jpg`; the counter moves to the end |

Edited copies (`IMG_1234-edited.jpg`, plus the German, French, Italian and Spanish variants) use the original's sidecar when they have none of their own. Sidecars are matched across every zip in the run.

## Duplicate detection

PhotoSorter hashes each file's bytes with [xxHash](https://github.com/cespare/xxhash). It then checks the hash against `.photosorter_cache.db`, an SQLite database at the library root. Duplicates are detected before anything is written, so they cost no disk writes.

The database only knows about files PhotoSorter imported. If you add files to the library some other way, run a **sync** so PhotoSorter hashes everything already there:

```bash
./photosorter -out ~/Pictures/Library -sync
```

A sync also runs on its own when the library contains media but the database is empty, for example on the first run against an existing library. If the library has more media files than it held after the last sync or import, PhotoSorter warns you. Pass `-no-sync` to turn off the automatic sync.

Deleting a file from the library does not remove its hash, so the same content is still skipped by later imports. That keeps photos you deleted on purpose from coming back. To make PhotoSorter forget files that are gone, run a **prune**. It syncs the library, then removes every cached hash whose content is no longer in it:

```bash
./photosorter -out ~/Pictures/Library -prune
```

Add `-dry-run` to list what would be pruned without changing the database. As a safeguard, a prune is skipped if the library has no media files or any file in it could not be read.

## Command-line reference

```
photosorter -out <library> [flags] [takeout.zip | folder ...]
```

Positional arguments are inputs: folders are imported like `-in`, and anything else like `-zip`.

| Flag | Default | Description |
| --- | --- | --- |
| `-out <folder>` | *(required)* | Library folder to import into |
| `-in <folder>` | | Import from a folder, such as an extracted Takeout. Repeatable |
| `-zip <file or glob>` | | Import from a Takeout zip, e.g. `"takeout-*.zip"`. Repeatable |
| `-config <file>` | `<out>/photosorter.toml` | Load settings from this TOML file |
| `-sync` | off | Hash the existing library before importing |
| `-prune` | off | Sync, then forget cached hashes of files no longer in the library so they can be imported again |
| `-reorganize` | off | Move dated folders at the top of the library into year folders (see [Reorganizing](#reorganizing-an-existing-library)) |
| `-no-sync` | off | Never sync automatically (`-nosync` is still accepted) |
| `-dry-run` | off | Go through the import without writing anything to the library (files, database or log) |
| `-workers <n>` | 8 | Files processed concurrently |
| `-max-buffer-mb <MB>` | 16 | Files up to this size are read once and kept in memory for hashing, dating and copying. 0 disables this. Peak memory is about `workers × MB` |
| `-tz <zone>` | machine's zone | IANA zone, e.g. `America/New_York`, used to date sidecar timestamps |
| `-skip-short-videos <s>` | 3 | Skip MP4/MOV videos this many seconds long or shorter, such as motion-photo clips. 0 imports all videos. Skipped clips aren't recorded, so a later run with a lower value imports them |
| `-date-format <layout>` | `2006/2006-01-02` | Go time layout for dated folders. Slashes nest: `2006/2006-01-02` → `2023/2023-05-14/`, `2006-01-02` → `2023-05-14/` (the old flat layout) |
| `-unknown-dir <folder>` | `Unknown_Date` | Folder for undated files |
| `-albums folder\|ignore` | `folder` | Keep albums together, or file their media by date |
| `-albums-dir <folder>` | `Albums` | Parent folder for albums |
| `-include-ext <list>` | | Extra extensions to import, e.g. `dng,arw` |
| `-exclude-ext <list>` | | Extensions to skip, e.g. `gif` |

The built-in extensions are `jpg jpeg png gif webp heic bmp tiff tif mp4 mov avi mkv webm mts 3gp m4v`. macOS metadata (`._*` files, `__MACOSX/` folders) is always ignored. The input folder and the library must not contain one another.

### Exit codes

| Code | Meaning |
| --- | --- |
| `0` | Everything was processed |
| `1` | A fatal error, or some files could not be imported (see the log) |
| `130` | Interrupted by Ctrl-C or SIGTERM. Finished files were recorded, so run again to continue. A second Ctrl-C exits immediately |

## Configuration

Every flag can also be set in a TOML file. Put it in the library as `photosorter.toml` to have it picked up automatically, or point to it with `-config`. Settings resolve in this order, with later ones winning:

**built-in defaults → config file → flags given on the command line**

```toml
tz = "Europe/London"

[layout]
date_format = "2006/01"      # 2023/05/
unknown_dir = "Undated"
albums = "ignore"

[media]
include_ext = ["dng"]
exclude_ext = ["gif"]
```

[`photosorter.example.toml`](photosorter.example.toml) documents every key. Unknown keys are reported as errors, so typos don't get silently ignored.

## How it works

```
 inputs ──► source ──────────► importer ─────────────────────────────► library
 (zip /     enumerate media,   per file, in parallel:                  dated / album
  folder)   index sidecars     1 hash (small files kept in memory)     folders
                               2 claim hash in DB ── duplicate? skip
                               3 skip short videos (optional)
                               4 date: EXIF → sidecar → file name
                               5 choose folder (layout)
                               6 copy via temp file, link into place, record hash
```

Copies are written to a temporary file and then hard-linked into place under a free name (or, on filesystems without hard links, renamed over a reserved name), so an interrupted run never leaves a half-written photo. With album folders on, album files from every input are imported before the rest, so a photo Takeout also placed in a year folder always lands in its album. Only one import should run on a library at a time. Hashes are committed to the database in batches by a background writer. If the database stops accepting writes, the run stops, because otherwise files would be imported but not remembered.

## Development

```bash
go test -race ./...    # all tests, including an end-to-end run on a synthetic Takeout
go vet ./...
gofmt -l .             # should print nothing
```

CI runs these steps plus [staticcheck](https://staticcheck.dev/) on Linux and macOS.

### Project layout

| Path | Responsibility |
| --- | --- |
| [`main.go`](main.go) | Entry point: signal handling, then `cli.Run` |
| [`internal/cli`](internal/cli) | Flag parsing, config loading, orchestrating a run, exit codes, log file |
| [`internal/config`](internal/config) | The `Config` struct, defaults, TOML loading, validation |
| [`internal/source`](internal/source) | Zip and folder inputs, behind the `Source`/`File` interfaces |
| [`internal/takeout`](internal/takeout) | Takeout specifics: sidecar matching, sidecar JSON, album folders |
| [`internal/media`](internal/media) | Media file types, EXIF and file-name dates, MP4 duration |
| [`internal/library`](internal/library) | The hash database, content hashing, library sync, reorganizing into year folders |
| [`internal/importer`](internal/importer) | The per-file import pipeline, folder layout, atomic writes, run report |
| [`internal/parallel`](internal/parallel) | Shared worker pool with a progress bar |

Each package has a doc comment that explains its role in more detail (`go doc ./internal/importer`).

## Dependencies

- [cespare/xxhash](https://github.com/cespare/xxhash): content hashing
- [glebarez/go-sqlite](https://github.com/glebarez/go-sqlite): pure-Go SQLite driver (no cgo)
- [rwcarlsen/goexif](https://github.com/rwcarlsen/goexif): EXIF decoding
- [BurntSushi/toml](https://github.com/BurntSushi/toml): config file parsing
- [schollz/progressbar](https://github.com/schollz/progressbar): terminal progress bars
