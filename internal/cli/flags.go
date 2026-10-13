package cli

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"photosorter/internal/config"
)

// deprecatedFlags maps old flag names, still accepted for compatibility, to their
// replacements. They are left out of the help text.
var deprecatedFlags = map[string]string{"nosync": "no-sync"}

// parseArgs resolves the run's settings from defaults, the config file and args.
//
// Flags are parsed twice. The first pass only finds -config and -out, which say
// which config file to load. The second pass binds flags onto the loaded settings, so
// only flags that were actually given override the file.
//
// Positional arguments are inputs: folders become -in, anything else -zip.
func parseArgs(args []string, stderr io.Writer) (config.Config, error) {
	probe := config.Default()
	var configPath string
	fs := newFlagSet(&probe, &configPath, io.Discard)
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			fs.SetOutput(stderr)
			fs.Usage()
		}
		return config.Config{}, err
	}

	cfg := config.Default()
	if path := config.FindFile(configPath, probe.Library); path != "" {
		if err := config.LoadFile(path, &cfg); err != nil {
			return config.Config{}, err
		}
	}
	fs = newFlagSet(&cfg, &configPath, io.Discard)
	if err := fs.Parse(args); err != nil {
		return config.Config{}, err
	}
	fs.Visit(func(f *flag.Flag) {
		if replacement, ok := deprecatedFlags[f.Name]; ok {
			fmt.Fprintf(stderr, "warning: -%s is deprecated; use -%s\n", f.Name, replacement)
		}
	})

	for _, arg := range fs.Args() {
		if info, err := os.Stat(arg); err == nil && info.IsDir() {
			cfg.Folders = append(cfg.Folders, arg)
		} else {
			cfg.Zips = append(cfg.Zips, arg)
		}
	}
	return cfg, cfg.Validate()
}

// newFlagSet defines every flag, bound to the fields of cfg. Each flag's default is
// the value already in cfg, so parsing only changes what is explicitly given.
func newFlagSet(cfg *config.Config, configPath *string, output io.Writer) *flag.FlagSet {
	fs := flag.NewFlagSet("photosorter", flag.ContinueOnError)
	fs.SetOutput(output)
	fs.Usage = func() { usage(fs) }

	fs.StringVar(configPath, "config", "", "Read settings from this TOML `file` (default: <out>/"+config.FileName+" if present)")

	fs.StringVar(&cfg.Library, "out", cfg.Library, "Library `folder` to import into (required)")
	fs.Var(newListFlag(&cfg.Folders, false), "in", "Import from this `folder` (repeatable)")
	fs.Var(newListFlag(&cfg.Zips, false), "zip", "Import from this Takeout zip `file` or glob pattern (repeatable)")

	fs.BoolVar(&cfg.Sync, "sync", cfg.Sync, "Hash every file already in the library before importing")
	fs.BoolVar(&cfg.NoSync, "no-sync", cfg.NoSync, "Never sync the library automatically")
	fs.BoolVar(&cfg.NoSync, "nosync", cfg.NoSync, "Deprecated spelling of -no-sync")
	fs.BoolVar(&cfg.DryRun, "dry-run", cfg.DryRun, "Show what would happen without writing anything")

	fs.IntVar(&cfg.Workers, "workers", cfg.Workers, "Number of files processed concurrently")
	fs.IntVar(&cfg.MaxBufferMB, "max-buffer-mb", cfg.MaxBufferMB, "Read files up to this size (`MB`) into memory once; 0 disables. Peak memory is about workers × this")
	fs.StringVar(&cfg.TimeZone, "tz", cfg.TimeZone, "IANA time `zone` for turning timestamps into dates, e.g. America/New_York (default: this machine's)")
	fs.IntVar(&cfg.SkipShortVideos, "skip-short-videos", cfg.SkipShortVideos, "Skip MP4/MOV videos this many `seconds` long or shorter (motion photos); 0 disables")

	fs.StringVar(&cfg.Layout.DateFormat, "date-format", cfg.Layout.DateFormat, "Go time `layout` for dated folders; slashes nest, e.g. 2006/01")
	fs.StringVar(&cfg.Layout.UnknownDir, "unknown-dir", cfg.Layout.UnknownDir, "Destination `folder` for files with no detectable date")
	fs.StringVar(&cfg.Layout.Albums, "albums", cfg.Layout.Albums, "Album `mode`: \"folder\" keeps Google Photos albums together, \"ignore\" files their media by date")
	fs.StringVar(&cfg.Layout.AlbumsDir, "albums-dir", cfg.Layout.AlbumsDir, "Parent `folder` for album folders")

	fs.Var(newListFlag(&cfg.Media.IncludeExt, true), "include-ext", "Also import these comma-separated `extensions`, e.g. dng,arw")
	fs.Var(newListFlag(&cfg.Media.ExcludeExt, true), "exclude-ext", "Never import these comma-separated `extensions`, e.g. gif")
	return fs
}

// usage prints the help text, hiding deprecated flags.
func usage(fs *flag.FlagSet) {
	w := fs.Output()
	fmt.Fprint(w, `PhotoSorter imports Google Takeout archives into a local photo library,
skipping media the library already has and filing the rest by date or album.

Usage:
  photosorter -out <library> [flags] [takeout.zip | folder ...]
  photosorter -out <library> -sync

Examples:
  photosorter -out ~/Photos takeout-*.zip
  photosorter -out ~/Photos -in ~/Downloads/Takeout -date-format 2006/01
  photosorter -out ~/Photos -dry-run takeout-001.zip

Flags:
`)
	// Copy the visible flags into a fresh set so PrintDefaults skips deprecated ones.
	visible := flag.NewFlagSet("", flag.ContinueOnError)
	visible.SetOutput(w)
	fs.VisitAll(func(f *flag.Flag) {
		if _, hidden := deprecatedFlags[f.Name]; !hidden {
			visible.Var(f.Value, f.Name, f.Usage)
		}
	})
	visible.PrintDefaults()
	fmt.Fprintf(w, "\nAll flags can also be set in a config file; see photosorter.example.toml.\n")
}

// listFlag is a repeatable flag that collects values into a slice. The first use on
// the command line replaces any values from the config file rather than adding to
// them. With split set, each value is also split on commas.
type listFlag struct {
	dst   *[]string
	split bool
	set   bool
}

func newListFlag(dst *[]string, split bool) *listFlag {
	return &listFlag{dst: dst, split: split}
}

func (l *listFlag) String() string {
	if l == nil || l.dst == nil {
		return ""
	}
	return strings.Join(*l.dst, ",")
}

func (l *listFlag) Set(v string) error {
	if !l.set {
		*l.dst = nil
		l.set = true
	}
	if !l.split {
		*l.dst = append(*l.dst, v)
		return nil
	}
	for _, part := range strings.Split(v, ",") {
		if part = strings.TrimSpace(part); part != "" {
			*l.dst = append(*l.dst, part)
		}
	}
	return nil
}
