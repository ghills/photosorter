// Package config defines PhotoSorter's settings and loads them from an optional TOML
// file.
//
// Settings are resolved in three layers, each overriding the one before:
//
//  1. built-in defaults ([Default]),
//  2. a config file ([LoadFile]): the -config path, or photosorter.toml in the
//     library folder if it exists,
//  3. command-line flags that were explicitly set.
//
// Every setting can be given in the file. See photosorter.example.toml at the
// repository root for a commented example.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/BurntSushi/toml"

	"photosorter/internal/importer"
	"photosorter/internal/media"
)

// FileName is the config file looked for in the library folder.
const FileName = "photosorter.toml"

// Album handling modes for [Layout.Albums].
const (
	AlbumsFolder = "folder" // file album media under AlbumsDir/<album>
	AlbumsIgnore = "ignore" // file album media by date like everything else
)

// Config holds every PhotoSorter setting. The TOML key of each field is in its tag.
type Config struct {
	// Library is the folder to import into (-out).
	Library string `toml:"out"`

	// Folders are directories to import from (-in).
	Folders []string `toml:"in"`

	// Zips are Takeout zip files or glob patterns to import from (-zip).
	Zips []string `toml:"zip"`

	// Sync forces a sync of the library before importing: files that are new or changed
	// since the last sync are hashed (-sync). It overrides NoSync.
	Sync bool `toml:"sync"`

	// Rehash syncs the library hashing every file, even unchanged ones (-rehash). It
	// implies Sync.
	Rehash bool `toml:"rehash"`

	// Prune syncs the library, then forgets cached hashes of content no longer in it,
	// so that content can be imported again (-prune). It implies Sync.
	Prune bool `toml:"prune"`

	// Reorganize moves dated folders at the top of the library into year folders
	// before anything else is done (-reorganize).
	Reorganize bool `toml:"reorganize"`

	// NoSync disables the automatic sync that runs when the library holds files the
	// database does not know about (-no-sync).
	NoSync bool `toml:"no_sync"`

	// DryRun goes through the import without writing anything (-dry-run).
	DryRun bool `toml:"dry_run"`

	// Workers is the number of files processed concurrently (-workers).
	Workers int `toml:"workers"`

	// MaxBufferMB: files up to this size are read into memory once instead of several
	// times; 0 disables (-max-buffer-mb). Peak memory is about Workers × MaxBufferMB.
	MaxBufferMB int `toml:"max_buffer_mb"`

	// TimeZone is the IANA zone used to turn timestamps into dates; empty means this
	// machine's zone (-tz).
	TimeZone string `toml:"tz"`

	// SkipShortVideos skips MP4, MOV, M4V and 3GP videos no longer than this many
	// seconds, such as motion-photo clips; 0 disables (-skip-short-videos).
	SkipShortVideos int `toml:"skip_short_videos"`

	Layout Layout `toml:"layout"`
	Media  Media  `toml:"media"`

	// Manual describes the batch being imported. It can only be set on the command
	// line, so a value left in the config file can't mis-file a later import.
	Manual Manual `toml:"-"`
}

// Manual holds the settings for importing a hand-picked batch of media.
type Manual struct {
	// Date files every media file under this date, YYYY[-MM[-DD]] (-date).
	Date string

	// FallbackDate dates files whose own date can't be found (-fallback-date).
	FallbackDate string

	// Event is appended to the name of every dated folder (-event).
	Event string

	// Album files everything under Layout.AlbumsDir/<Album> (-album).
	Album string
}

// manualKeys are the command-line-only settings, for a helpful error if they appear in
// a config file.
var manualKeys = map[string]bool{"date": true, "fallback_date": true, "event": true, "album": true}

// IsSet reports whether any manual-import setting is given.
func (m Manual) IsSet() bool { return m != (Manual{}) }

// Dates parses Date and FallbackDate; each is nil if not set.
func (m Manual) Dates() (forced, fallback *importer.GivenDate, err error) {
	parse := func(flag, s string) (*importer.GivenDate, error) {
		if s == "" {
			return nil, nil
		}
		g, err := importer.ParseGivenDate(s)
		if err != nil {
			return nil, fmt.Errorf("-%s: %w", flag, err)
		}
		return &g, nil
	}
	forced, err1 := parse("date", m.Date)
	fallback, err2 := parse("fallback-date", m.FallbackDate)
	return forced, fallback, errors.Join(err1, err2)
}

func (m Manual) validate() error {
	var errs []error
	if _, _, err := m.Dates(); err != nil {
		errs = append(errs, err)
	}
	if m.Date != "" && m.FallbackDate != "" {
		errs = append(errs, errors.New("use either -date or -fallback-date, not both: -date replaces every file's date, -fallback-date only fills in missing ones"))
	}
	if m.Album != "" && m.Event != "" {
		errs = append(errs, errors.New("use either -album or -event, not both: -album puts everything in one album folder, -event names the dated folders"))
	}
	for flag, name := range map[string]string{"event": m.Event, "album": m.Album} {
		if name != "" && !isFolderName(name) {
			errs = append(errs, fmt.Errorf("-%s %q must be usable as a single folder name (none of / \\ : * ? \" < > |, not . or .., no leading or trailing space)", flag, name))
		}
	}
	return errors.Join(errs...)
}

// isFolderName reports whether name is safe as one folder name on any common
// filesystem, so the library stays portable between Windows, macOS and Linux.
func isFolderName(name string) bool {
	if name == "." || name == ".." || strings.TrimSpace(name) != name || strings.HasSuffix(name, ".") {
		return false
	}
	for _, r := range name {
		if r < 0x20 || strings.ContainsRune(`/\:*?"<>|`, r) {
			return false
		}
	}
	return name != "" && filepath.IsLocal(name)
}

// Layout controls the library's folder structure.
type Layout struct {
	// DateFormat names dated folders, as a Go time layout (reference date
	// 2006-01-02). Slashes create nested folders (-date-format).
	DateFormat string `toml:"date_format"`

	// UnknownDir holds files with no detectable date (-unknown-dir).
	UnknownDir string `toml:"unknown_dir"`

	// Albums is AlbumsFolder or AlbumsIgnore (-albums).
	Albums string `toml:"albums"`

	// AlbumsDir is the parent folder of albums (-albums-dir).
	AlbumsDir string `toml:"albums_dir"`
}

// Media controls which files are imported.
type Media struct {
	// IncludeExt adds extensions to the built-in list, e.g. ["dng", "arw"] (-include-ext).
	IncludeExt []string `toml:"include_ext"`

	// ExcludeExt removes extensions from it (-exclude-ext).
	ExcludeExt []string `toml:"exclude_ext"`
}

// Default returns the built-in settings. The layout is the importer's default.
func Default() Config {
	l := importer.DefaultLayout()
	albums := AlbumsIgnore
	if l.KeepAlbums {
		albums = AlbumsFolder
	}
	return Config{
		Workers:         8,
		MaxBufferMB:     16,
		SkipShortVideos: 3,
		Layout: Layout{
			DateFormat: l.DateFormat,
			UnknownDir: l.UnknownDir,
			Albums:     albums,
			AlbumsDir:  l.AlbumsDir,
		},
	}
}

// LoadFile overlays the settings in the TOML file at path onto cfg. Keys missing from
// the file leave cfg unchanged; unknown keys are an error, so typos are caught.
// Relative paths in the file (out, in, zip) are relative to the file's folder.
func LoadFile(path string, cfg *Config) error {
	// Decoding onto cfg replaces only the values present in the file.
	md, err := toml.DecodeFile(path, cfg)
	if err != nil {
		return fmt.Errorf("config file %s: %w", path, err)
	}
	if undecoded := md.Undecoded(); len(undecoded) > 0 {
		keys := make([]string, len(undecoded))
		for i, k := range undecoded {
			keys[i] = k.String()
			if manualKeys[keys[i]] {
				return fmt.Errorf("config file %s: %s can only be given on the command line, since it applies to one import", path, keys[i])
			}
		}
		return fmt.Errorf("config file %s: unknown setting(s): %s", path, strings.Join(keys, ", "))
	}

	base := filepath.Dir(path)
	resolve := func(p string) string {
		if p == "" || filepath.IsAbs(p) {
			return p
		}
		return filepath.Join(base, p)
	}
	if md.IsDefined("out") {
		cfg.Library = resolve(cfg.Library)
	}
	if md.IsDefined("in") {
		for i := range cfg.Folders {
			cfg.Folders[i] = resolve(cfg.Folders[i])
		}
	}
	if md.IsDefined("zip") {
		for i := range cfg.Zips {
			cfg.Zips[i] = resolve(cfg.Zips[i])
		}
	}
	return nil
}

// FindFile returns the config file to load: explicit if set, otherwise FileName in
// the library folder if that exists, otherwise "".
func FindFile(explicit, library string) string {
	if explicit != "" {
		return explicit
	}
	if library == "" {
		return ""
	}
	p := filepath.Join(library, FileName)
	if _, err := os.Stat(p); err == nil {
		return p
	}
	return ""
}

// Validate checks that the settings are usable together.
func (c Config) Validate() error {
	var errs []error
	add := func(format string, args ...any) { errs = append(errs, fmt.Errorf(format, args...)) }

	if c.Library == "" {
		add("an output library folder is required (-out)")
	}
	hasInput := len(c.Folders) > 0 || len(c.Zips) > 0
	if !hasInput && !c.Sync && !c.Rehash && !c.Prune && !c.Reorganize {
		add("nothing to do: give an input (-in, -zip), -sync, -rehash, -prune or -reorganize")
	}
	if c.Manual.IsSet() && !hasInput {
		add("-date, -fallback-date, -event and -album need something to import: give a folder or zip")
	}
	errs = append(errs, c.Manual.validate())
	if c.Manual.Album != "" && c.Layout.Albums != AlbumsFolder && !isRelativeDir(c.Layout.AlbumsDir) {
		add("albums folder %q must be a relative folder path", c.Layout.AlbumsDir)
	}
	if c.Workers < 1 {
		add("workers must be at least 1, got %d", c.Workers)
	}
	if c.MaxBufferMB < 0 {
		add("max-buffer-mb must not be negative, got %d", c.MaxBufferMB)
	}
	if c.SkipShortVideos < 0 {
		add("skip-short-videos must not be negative, got %d", c.SkipShortVideos)
	}
	if _, err := c.Location(); err != nil {
		add("invalid time zone %q: %v", c.TimeZone, err)
	}
	if _, err := c.Filter(); err != nil {
		errs = append(errs, err)
	}
	errs = append(errs, c.Layout.validate())
	return errors.Join(errs...)
}

func (l Layout) validate() error {
	var errs []error
	sample := time.Date(2006, 1, 2, 15, 4, 5, 0, time.UTC).Format(l.DateFormat)
	if l.DateFormat == "" || !isRelativeDir(sample) {
		errs = append(errs, fmt.Errorf("date format %q must produce a relative folder path", l.DateFormat))
	}
	if !isRelativeDir(l.UnknownDir) {
		errs = append(errs, fmt.Errorf("unknown-date folder %q must be a relative folder path", l.UnknownDir))
	}
	switch l.Albums {
	case AlbumsFolder:
		if !isRelativeDir(l.AlbumsDir) {
			errs = append(errs, fmt.Errorf("albums folder %q must be a relative folder path", l.AlbumsDir))
		}
	case AlbumsIgnore:
	default:
		errs = append(errs, fmt.Errorf("albums must be %q or %q, got %q", AlbumsFolder, AlbumsIgnore, l.Albums))
	}
	return errors.Join(errs...)
}

// isRelativeDir reports whether p (slash-separated) names a folder inside the library:
// not absolute, no "..", no empty or "." components.
func isRelativeDir(p string) bool {
	if p == "" || strings.HasPrefix(p, "/") {
		return false
	}
	for _, part := range strings.Split(p, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return filepath.IsLocal(filepath.FromSlash(p))
}

// Location returns the configured time zone, or time.Local if none is set.
func (c Config) Location() (*time.Location, error) {
	if c.TimeZone == "" {
		return time.Local, nil
	}
	return time.LoadLocation(c.TimeZone)
}

// Filter returns the media filter described by c.Media.
func (c Config) Filter() (media.Filter, error) {
	return media.NewFilter(c.Media.IncludeExt, c.Media.ExcludeExt)
}

// BufferLimit is MaxBufferMB in bytes.
func (c Config) BufferLimit() int64 { return int64(c.MaxBufferMB) << 20 }

// ShortVideoLimit is SkipShortVideos as a duration.
func (c Config) ShortVideoLimit() time.Duration {
	return time.Duration(c.SkipShortVideos) * time.Second
}
