# PhotoSorter

PhotoSorter imports [Google Takeout](https://takeout.google.com/) exports of Google Photos into a plain folder-based photo library on your own disk. It also takes any other folder or zip of photos, and lets you give a date or a name to batches you add by hand.

- **No duplicates.** Every file in the library is identified by a hash of its content. Anything already in the library is skipped, whether it was imported before, came from an earlier Takeout, or was put there by hand.
- **Correct dates.** Each file goes into a dated folder based on its EXIF data, Google's JSON sidecar files, or failing those, the date in its file name. Google often strips EXIF from exported media, so the sidecars matter. Each file's modification time is set to that date too, for viewers that sort by it.
- **Google's metadata kept.** When a file's date had to come from its sidecar, PhotoSorter writes an XMP sidecar next to it with the capture time, location, description, people and favourite flag that Google stripped from the file.
- **Albums kept.** Media from your Google Photos albums can stay grouped as albums.
- **Works on the zips directly.** No need to extract multi-gigabyte archives first. Extracted folders work too.

## Download

Ready-to-run builds of the latest `main` are on the [releases page](https://github.com/ghills/photosorter/releases/latest). No Go install needed.

| Platform | File |
|---|---|
| Windows (most PCs) | [`photosorter-windows-amd64.zip`](https://github.com/ghills/photosorter/releases/latest/download/photosorter-windows-amd64.zip) |
| Windows on ARM | [`photosorter-windows-arm64.zip`](https://github.com/ghills/photosorter/releases/latest/download/photosorter-windows-arm64.zip) |
| macOS (Intel and Apple silicon) | [`photosorter-macos-universal.tar.gz`](https://github.com/ghills/photosorter/releases/latest/download/photosorter-macos-universal.tar.gz) |

Unpack the download. Then either double-click `photosorter` to use it from your web browser (see [Using it from your browser](#using-it-from-your-browser)), or run it from a terminal.

- **Windows:** extract the zip, then double-click `photosorter.exe`, or open PowerShell in that folder and run `.\photosorter.exe -h`. The builds aren't code-signed, so SmartScreen may warn the first time; choose **More info → Run anyway**.
- **macOS:** double-click the `.tar.gz` to unpack it, then in Terminal, from that folder:

  ```bash
  xattr -d com.apple.quarantine photosorter
  ./photosorter -h
  ```

  The `xattr` step clears the "downloaded from the internet" flag; without it macOS refuses to open an unsigned program. After that, double-clicking `photosorter` in Finder works too.

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

## Using it from your browser

Run PhotoSorter with no arguments (or double-click it), or with `-ui`, and it opens a page in your web browser instead of working on the command line:

```bash
./photosorter
./photosorter -ui -out ~/Pictures/Library   # flags given with -ui prefill the page
```

The page starts by asking what you want to do: **Import photos** or **Maintain the library**. It then shows only that task's choices, which are the same as the common flags:

| Task | On the page | Flag |
| --- | --- | --- |
| Both | Library | `-out` |
| Both | Dry run | `-dry-run` |
| Import photos | Import from (folders and zips, picked with your system's file dialog or typed) | positional inputs, `-in`, `-zip` |
| Import photos | Give every file this date / Only date the files that have no date | `-date` / `-fallback-date` |
| Import photos | Add an event name / Put everything in one album | `-event` / `-album` |
| Import photos | Google Photos albums | `-albums` |
| Maintain the library | Sync, Rehash, Prune, Reorganize (one or more) | `-sync`, `-rehash`, `-prune`, `-reorganize` |

A run does only the task you chose. An import runs no maintenance you didn't see, even if `photosorter.toml` turns it on (the [automatic sync](#duplicate-detection) still happens), and maintenance never imports anything. On the command line you can still combine them in one run. If PhotoSorter was started with maintenance flags and no inputs, such as `-ui -sync`, the page opens on **Maintain the library**.

Every other setting comes from the library's `photosorter.toml` (see [Configuration](#configuration)), and the page says which file it used. The file is read again for every run, so you can edit it while the page is open. Paths typed into the page must be full paths, such as `/Users/you/Pictures` or `C:\Photos`; a leading `~` means your home folder. A run prints the same output and writes the same log file as the command line. **Stop** stops it the way Ctrl-C does, so it's safe to run again.

The program keeps running while the page is open. Close it with **Quit PhotoSorter** on the page, or with Ctrl-C or by closing its terminal window. The page is served only to this computer (on `127.0.0.1`), from inside the program, and is protected by a random token in its address.

## Common recipes

`photosorter -h` prints these too. Add `-dry-run` to any of them to see what would happen first.

| To | Run |
|---|---|
| Import a Google Takeout export (all parts together) | `photosorter -out ~/Photos ~/Downloads/takeout-*.zip` |
| Add a folder or zip, dated from the files themselves | `photosorter -out ~/Photos ~/Desktop/phone-backup` |
| Add photos you know the date of | `photosorter -out ~/Photos -date 2019-07-04 ~/Desktop/fireworks` |
| ...and name their folder | `photosorter -out ~/Photos -date 2019-07-04 -event Fireworks ~/Desktop/fireworks` |
| Add photos when you only know the year or month | `photosorter -out ~/Photos -date 2004-08 -event "Lake house" ~/Scans/box2` |
| Date only the files that have no date of their own | `photosorter -out ~/Photos -fallback-date 1998 ~/Scans/mixed` |
| Keep a batch together as an album | `photosorter -out ~/Photos -album Wedding ~/Downloads/wedding-photos.zip` |
| Register files you copied into the library by hand | `photosorter -out ~/Photos -sync` |
| Re-hash every file, e.g. after editing photos in place | `photosorter -out ~/Photos -rehash` |
| Let deleted files be imported again | `photosorter -out ~/Photos -prune` |
| Move old `2023-05-14/` folders into year folders | `photosorter -out ~/Photos -reorganize` |

## Adding photos by hand

Any folder or zip works as an input, not just Takeout. Files are dated and de-duplicated the same way. Four flags help with batches you put together yourself, such as scans, a friend's photos, or a camera whose clock was wrong. They apply to one run only, so they can't be set in the config file.

| Flag | Effect |
|---|---|
| `-date YYYY[-MM[-DD]]` | Every file in the run gets this date, even one that has its own. The run reports how many files had a different date of their own, and the log lists them |
| `-fallback-date YYYY[-MM[-DD]]` | Only files with no date of their own get this date, instead of going to `Unknown_Date/` |
| `-event <name>` | Adds a name to the dated folders |
| `-album <name>` | Puts everything in `Albums/<name>/`, whatever its date |

Use either `-date` or `-fallback-date`, and either `-event` or `-album`. You can give just a year, or a year and month. With the default layout, files land here:

| Flags | Folder |
|---|---|
| `-date 2019-07-04` | `2019/2019-07-04/` |
| `-date 2019-07` | `2019/2019-07/` |
| `-date 2019` | `2019/` |
| `-date 2019-07-04 -event Fireworks` | `2019/2019-07-04 Fireworks/` |
| `-date 2019-07 -event "Road trip"` | `2019/2019-07 Road trip/` |
| `-date 2019 -event Scans` | `2019/2019 Scans/` |
| `-event Hawaii` | each file's own date folder plus the name, e.g. `2023/2023-05-14 Hawaii/` |
| `-fallback-date 1998` | dated files as usual; undated files in `1998/` |
| `-album "Grandma's box"` | `Albums/Grandma's box/` |

With a custom `-date-format`, a partial date uses the parts of the format it can fill in, then the date itself. For example, `2006/01` with `-date 2019-07` gives `2019/07/`. Event and album names must work as folder names on Windows, macOS and Linux, so they can't contain `/ \ : * ? " < > |`.

Files already in the library are still skipped. Importing the same photos again with a different `-date` won't move them.

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
- **Other folders** (`Road trip/`) are dated by the media inside them: EXIF first, then a video's recording time, then the date in the file name. A folder is moved only if every media file in it has a date and they all fall in the same year. Otherwise it stays where it is, and the run says why.
- **Left alone:** year folders, the unknown-date and albums folders, hidden folders, and loose files.

If the target folder already exists, for example because an import already used the new layout, the two are merged. Nothing is overwritten. A file whose name and content are both already in the target is removed. A file whose name is taken by different content gets a `_N` suffix.

The hash database tracks content, not folders, so moving files doesn't invalidate it. Renamed files get their database entry updated, and the library's file count drops by the number of duplicates removed.

Reorganizing is safe to repeat. Folders already moved are not moved again, and running it again finishes a merge that was interrupted. Add `-dry-run` to see what would move first. Every move is written to the run's log.

## How dates are found

For each file, PhotoSorter uses the first of these that works:

1. **EXIF**: `DateTimeOriginal` from JPEG, TIFF and HEIC files. JPEG and TIFF keep it in a header in the first 256 KB, which is all that is read. HEIC (iPhone photos) keeps it in a separate block that the file's index points to.
2. **JSON sidecar `photoTakenTime`**: the capture time Google recorded.
3. **Video recording time**, from MP4, MOV, M4V and 3GP files. Videos from Apple devices carry a creation date in local time with its UTC offset, which is used as recorded, like EXIF. Otherwise it is the creation time in the movie header. Many encoders leave that empty, and those files skip this step. It comes after the sidecar because apps that re-encode a video often reset it.
4. **JSON sidecar `creationTime`**: the upload time. This is a last resort before guessing.
5. **File name**: patterns such as `IMG_20230514_…`, `VID-2023-05-14…`, `Screenshot_2023-05-14…` and `WhatsApp Image 2023-05-14…`.

If none of them works, the file goes to `Unknown_Date/`, or gets the `-fallback-date` if you gave one. A `-date` replaces all of these (see [Adding photos by hand](#adding-photos-by-hand)).

The imported copy's modification time is set to the date it was filed under, so file browsers and viewers that sort by it show photos in the order they were taken. EXIF times, `-date` and `-fallback-date` have no time zone, so they are read in the `-tz` zone; a `-date 2019-07` file gets midnight on July 1st. Files with no date keep the time they were copied. Files imported before this feature keep their old modification times.

Sidecar timestamps and movie header times are absolute instants, so the day they fall on depends on the time zone. PhotoSorter uses the machine's zone unless you pass `-tz`. EXIF times and Apple video creation dates are local times and are used exactly as recorded. A few cameras write their local time into the movie header instead of UTC; their videos can land a day off around midnight.

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

## XMP sidecars

Takeout removes EXIF from many files and keeps their metadata only in the JSON sidecar. When PhotoSorter has to date a file from that sidecar, it also writes an XMP sidecar next to the imported copy, so other photo software can read the metadata. The sidecar is named after the full file name, `IMG_1234.jpg.xmp`, which digiKam, darktable and ExifTool read. The media file itself is never changed.

| Takeout JSON | XMP |
| --- | --- |
| `photoTakenTime` | `exif:DateTimeOriginal` and `photoshop:DateCreated`, in the `-tz` zone with its UTC offset |
| `geoData`, or `geoDataExif` if `geoData` is empty | `exif:GPSLatitude`, `exif:GPSLongitude`, `exif:GPSAltitude` |
| `description` | `dc:description` |
| `people` | `Iptc4xmpExt:PersonInImage` |
| `favorited` | `xmp:Rating` of 5 |

An XMP sidecar is written only for files dated from the JSON ("dated from JSON" in the summary). Files dated from EXIF, the video header, the file name or `-date` keep their own metadata and get none. A file dated from `creationTime` gets no capture time in its XMP, because that is the upload time; if it has none of the other fields either, no XMP is written. An existing `.xmp` file is never replaced; this is logged and counted as a failed sidecar, and the file is still imported. `-reorganize` moves sidecars along with their folders. Pass `-no-xmp` to turn sidecars off.

## Duplicate detection

PhotoSorter hashes each file's bytes with [xxHash](https://github.com/cespare/xxhash). It then checks the hash against `.photosorter_cache.db`, an SQLite database at the library root. Duplicates are detected before anything is written, so they cost no disk writes.

The database only knows about files PhotoSorter imported. If you add files to the library some other way, run a **sync** so PhotoSorter hashes them too:

```bash
./photosorter -out ~/Pictures/Library -sync
```

A sync is incremental. The database remembers the path, size and modification time of every file it has hashed, and files that still match are not read again. So a sync of a large library only costs a directory walk plus hashing whatever is new or changed. Files PhotoSorter imports or moves with `-reorganize` are remembered the same way.

If you edit photos with a tool that keeps their size and timestamp the same, the change won't be noticed. Run a **rehash** to hash every file regardless:

```bash
./photosorter -out ~/Pictures/Library -rehash
```

A sync also runs on its own when the library contains media but the database is empty, for example on the first run against an existing library. It also runs when the library has more media files than it held after the last sync or import, which usually means files were copied in by hand. Without that sync, the same photo could be imported a second time. Pass `-no-sync` to turn off the automatic sync.

Deleting a file from the library does not remove its hash, so the same content is still skipped by later imports. That keeps photos you deleted on purpose from coming back. To make PhotoSorter forget files that are gone, run a **prune**. It syncs the library, then removes every cached hash whose content is no longer in it (incrementally, like `-sync`):

```bash
./photosorter -out ~/Pictures/Library -prune
```

Add `-dry-run` to list what would be pruned without changing the database. As a safeguard, a prune is skipped if the library has no media files or any file in it could not be read.

## Command-line reference

```
photosorter -out <library> [flags] [takeout.zip | folder ...]
photosorter [-ui] [flags]          # browser interface
```

Positional arguments are inputs: folders are imported like `-in`, and anything else like `-zip`.

| Flag | Default | Description |
| --- | --- | --- |
| `-out <folder>` | *(required)* | Library folder to import into |
| `-in <folder>` | | Import from a folder, such as an extracted Takeout. Repeatable |
| `-zip <file or glob>` | | Import from a zip, e.g. `"takeout-*.zip"`. Repeatable |
| `-date <YYYY[-MM[-DD]]>` | | Give every file in this run this date (see [Adding photos by hand](#adding-photos-by-hand)). Command line only |
| `-fallback-date <YYYY[-MM[-DD]]>` | | Give this date only to files with no date of their own. Command line only |
| `-event <name>` | | Add a name to the dated folders: `2019/2019-07-04 Fireworks/`. Command line only |
| `-album <name>` | | Put everything from this run in `Albums/<name>/`. Command line only |
| `-config <file>` | `<out>/photosorter.toml` | Load settings from this TOML file |
| `-ui` | on with no arguments | Open the [browser interface](#using-it-from-your-browser), prefilled from any other flags. Command line only |
| `-sync` | off | Hash files in the library that are new or changed since the last sync |
| `-rehash` | off | Like `-sync`, but hash every file even if unchanged |
| `-prune` | off | Sync, then forget cached hashes of files no longer in the library so they can be imported again |
| `-reorganize` | off | Move dated folders at the top of the library into year folders (see [Reorganizing](#reorganizing-an-existing-library)) |
| `-no-sync` | off | Never sync automatically (`-nosync` is still accepted) |
| `-no-xmp` | off | Don't write [XMP sidecars](#xmp-sidecars) for files dated from their Takeout JSON |
| `-dry-run` | off | Go through the import without writing anything to the library (files, database or log) |
| `-workers <n>` | 8 | Files processed concurrently |
| `-max-buffer-mb <MB>` | 16 | Files up to this size are read once and kept in memory for hashing, dating and copying. 0 disables this. Peak memory is about `workers × MB` |
| `-tz <zone>` | machine's zone | IANA zone, e.g. `America/New_York`, used to date sidecar timestamps |
| `-skip-short-videos <s>` | 3 | Skip MP4, MOV, M4V and 3GP videos this many seconds long or shorter, such as motion-photo clips. 0 imports all videos. Skipped clips aren't recorded, so a later run with a lower value imports them |
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

Every flag except `-date`, `-fallback-date`, `-event` and `-album` can also be set in a TOML file. Put it in the library as `photosorter.toml` to have it picked up automatically, or point to it with `-config`. Settings resolve in this order, with later ones winning:

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
                               4 date: EXIF → sidecar → video header → file name
                               5 choose folder (layout)
                               6 copy via temp file, link into place, set mtime, record hash
                               7 XMP sidecar, if dated from the JSON sidecar
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
| [`internal/cli`](internal/cli) | Flag parsing, config loading, orchestrating a run, exit codes, log file; starts `ui` with no arguments or `-ui` |
| [`internal/config`](internal/config) | The `Config` struct, defaults, TOML loading, validation |
| [`internal/source`](internal/source) | Zip and folder inputs, behind the `Source`/`File` interfaces |
| [`internal/takeout`](internal/takeout) | Takeout specifics: sidecar matching, sidecar JSON, album folders |
| [`internal/media`](internal/media) | Media file types, EXIF (JPEG, TIFF, HEIC) and file-name dates, MP4 duration and recording time |
| [`internal/library`](internal/library) | The hash database, content hashing, library sync, reorganizing into year folders |
| [`internal/xmp`](internal/xmp) | Encoding and writing XMP sidecar files |
| [`internal/importer`](internal/importer) | The per-file import pipeline, folder layout, atomic writes, run report |
| [`internal/parallel`](internal/parallel) | Shared worker pool with a progress bar |
| [`internal/ui`](internal/ui) | The browser interface: local web server, embedded page, native file pickers |

Each package has a doc comment that explains its role in more detail (`go doc ./internal/importer`).

## Dependencies

- [cespare/xxhash](https://github.com/cespare/xxhash): content hashing
- [glebarez/go-sqlite](https://github.com/glebarez/go-sqlite): pure-Go SQLite driver (no cgo)
- [rwcarlsen/goexif](https://github.com/rwcarlsen/goexif): EXIF decoding
- [BurntSushi/toml](https://github.com/BurntSushi/toml): config file parsing
- [schollz/progressbar](https://github.com/schollz/progressbar): terminal progress bars
