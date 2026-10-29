# PhotoSorter

Go CLI (module `photosorter`, Go 1.23) that imports Google Takeout zips/folders, or any folder of photos, into a dated, de-duplicated folder library. README.md is the user-facing spec. Keep it in sync with behaviour changes.

## Commands

```bash
go build -o photosorter .          # binary is gitignored
go test -race -count=1 ./...       # what CI runs
gofmt -l . && go vet ./... && go run honnef.co/go/tools/cmd/staticcheck@2024.1.1 ./...
```

CI (`.github/workflows/ci.yml`) runs gofmt, vet, staticcheck and race tests on ubuntu and macos. All of them must pass. Every push to `main` publishes Windows and macOS-universal release builds (`release.yml`).

## Layout

`main.go` only sets up signals and calls `cli.Run`. Everything else is under `internal/`. Each package has a `doc.go` that explains its design. Read it before changing the package.

- `cli`: flag parsing (`flags.go`, which parses twice so the config file can sit under flags), run orchestration, and the summary output. `Execute` runs already-resolved settings; with no arguments or `-ui`, `Run` starts `ui` instead.
- `ui`: browser interface. It's a local server on 127.0.0.1 with a token, and the page is `static/index.html` (embedded, no CDN). Runs go through `cli.Execute`, which it receives as a func so it never imports `cli`. File pickers shell out to osascript, PowerShell, zenity or kdialog (`dialog_*.go`, build-tagged). Its form shows only the common settings; everything else comes from the toml.
- `config`: settings struct. Layers are defaults, then TOML file (`-config` or `<library>/photosorter.toml`), then explicitly set flags.
- `importer`: per-file pipeline (hash → claim in DB → date → `Layout` folder → temp-file copy → mtime → XMP sidecar). The steps are listed in `doc.go`.
- `library`: SQLite hash DB (`.photosorter_cache.db`, pure-Go `glebarez/go-sqlite`, no cgo), incremental `Sync`, `Reorganize`, and `PlaceFile` (never overwrites; adds a `_N` suffix).
- `media`: EXIF (JPEG/TIFF/HEIC), MP4/QuickTime headers, dates from file names, the media filter. No library or Takeout knowledge here.
- `takeout`: pairs media with JSON sidecars across Takeout's name mangling and across zip parts, and finds albums.
- `source`: zip and folder sources. All scanning happens at open time, so the shared `SidecarIndex` is complete before processing starts.
- `xmp`: writes XMP sidecars. `parallel`: generic worker pool with a progress bar.

## Invariants (don't break these)

- Files are identified by the xxhash of their content. Never overwrite an existing library file.
- Writes go through `.photosorter-tmp/`, then a rename, so no partial file is ever left behind.
- Date precedence: EXIF → sidecar `photoTakenTime` → video header → sidecar `creationTime` → file name. `-date` forces a date and `-fallback-date` fills a missing one.
- One file's failure is logged and counted, and the run continues. Context cancellation or a DB write failure stops the run cleanly.
- XMP sidecars must stay paired with their media through renames, suffixes and reorganize.
- Folder and event names must be valid on Windows, macOS and Linux.

## Adding a setting

1. Add a field with a `toml` tag to `config.Config`, plus a default and validation.
2. Bind the flag in `cli/flags.go`. Help-text tests in `flags_test.go` check its content.
3. Document it in `photosorter.example.toml`. `TestExampleFileIsValid` parses this file, and unknown keys are rejected.
4. Update README.md.

Per-run flags (`-date`, `-fallback-date`, `-event`, `-album`, `-ui`) are CLI-only by design. If a setting should also be on the browser page, add it to `ui.form` (`formFrom` and `apply`) and to `index.html`.

## Tests

- Tests use `t.TempDir()` and synthetic media. `internal/media/mediatest` builds minimal valid JPEG/TIFF/HEIC/MP4/MOV files with given dates, so no binary fixtures are checked in.
- Importer helpers are in `importer/helpers_test.go` (`newMemFile`, `sourceOf`, `newTestImporter`, `run`, `writeZip`). CLI end-to-end tests use `runCLI` and `mediaIn` from `cli_test.go`.
- Imports run concurrently, so keep `-race` on.
- Untracked scratch items in the repo root (`out/`, `out_test/`, `test_env/`, `.bin/`, `fake.zip`) are the user's local leftovers. Don't commit them or rely on them.

## Conventions

- Conventional commits (`feat:`, `fix:`, `refactor:`, `ci:`, `test:`, `perf:`) on `feat/...` or `fix/...` branches, merged to `main` via PR.
- Doc comments are full sentences and explain *why*. Exported identifiers are referenced as `[Name]`. Match that density.
- Avoid new dependencies. The project stays pure Go so it cross-compiles without cgo.
