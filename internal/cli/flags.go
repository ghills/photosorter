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
	fs.Var(newListFlag(&cfg.Zips, false), "zip", "Import from this zip `file` or glob pattern, e.g. \"takeout-*.zip\" (repeatable)")

	fs.StringVar(&cfg.Manual.Date, "date", "", "Give every file in this run the date `YYYY[-MM[-DD]]`, even files with a date of their own")
	fs.StringVar(&cfg.Manual.FallbackDate, "fallback-date", "", "Give the date `YYYY[-MM[-DD]]` only to files whose own date can't be found, instead of putting them in the unknown-date folder")
	fs.StringVar(&cfg.Manual.Event, "event", "", "Add this `name` to the dated folders, e.g. 2019/2019-07-04 Fireworks/")
	fs.StringVar(&cfg.Manual.Album, "album", "", "Put everything from this run in one album, e.g. -album Wedding puts it in Albums/Wedding/")

	fs.BoolVar(&cfg.Sync, "sync", cfg.Sync, "Hash files in the library that are new or changed since the last sync, so they count as already imported")
	fs.BoolVar(&cfg.Rehash, "rehash", cfg.Rehash, "Like -sync, but hash every file in the library, even unchanged ones")
	fs.BoolVar(&cfg.Prune, "prune", cfg.Prune, "Sync the library, then forget cached hashes of files no longer in it so they can be imported again")
	fs.BoolVar(&cfg.Reorganize, "reorganize", cfg.Reorganize, "Move dated folders at the top of the library into year folders, e.g. 2023-05-14/ to 2023/2023-05-14/")
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

// usageIntro is the start of the help text: what the program does, then recipes for
// the common tasks, since those are what's hard to remember between uses.
const usageIntro = `PhotoSorter copies photos and videos into a library folder sorted by date,
skipping anything the library already has. It reads Google Takeout zips directly,
and any other folder or zip of photos too.

Usage:
  photosorter -out <library> [flags] [zip | folder ...]

Common tasks (add -dry-run to any of them to see what would happen first):

  Import a Google Takeout export. Pass every part of it together:
    photosorter -out ~/Photos ~/Downloads/takeout-*.zip

  Add a folder or zip of photos, dated from the files themselves (EXIF, file name):
    photosorter -out ~/Photos ~/Desktop/phone-backup

  Add photos you know the date of. Every file gets this date, even if it has its own:
    photosorter -out ~/Photos -date 2019-07-04 ~/Desktop/fireworks

  ...and name the folder (-> 2019/2019-07-04 Fireworks/):
    photosorter -out ~/Photos -date 2019-07-04 -event Fireworks ~/Desktop/fireworks

  Only know the year or month (-> 2004/, or 2004/2004-08 Lake house/):
    photosorter -out ~/Photos -date 2004 ~/Scans/box1
    photosorter -out ~/Photos -date 2004-08 -event "Lake house" ~/Scans/box2

  Keep each file's own date, but date the ones that have none (instead of Unknown_Date/):
    photosorter -out ~/Photos -fallback-date 1998 ~/Scans/mixed

  Keep a batch together as an album (-> Albums/Wedding/):
    photosorter -out ~/Photos -album Wedding ~/Downloads/wedding-photos.zip

  After copying files into the library by hand, so they count as already imported
  (this also happens on its own when the library has more files than last time):
    photosorter -out ~/Photos -sync

  After editing photos in the library with a tool that keeps their timestamps:
    photosorter -out ~/Photos -rehash

  After deleting files from the library, so they can be imported again:
    photosorter -out ~/Photos -prune

  Move old top-level 2023-05-14/ folders into year folders (2023/2023-05-14/):
    photosorter -out ~/Photos -reorganize
`

// flagGroups orders the flags in the help text. Every visible flag must be in one
// group (a test checks this).
var flagGroups = []struct {
	title string
	flags []string
}{
	{"Inputs (folders and zips can also be given without -in/-zip)", []string{"out", "in", "zip"}},
	{"Manual import (this run only; can't be set in the config file)", []string{"date", "fallback-date", "event", "album"}},
	{"Library maintenance", []string{"dry-run", "sync", "rehash", "prune", "reorganize", "no-sync"}},
	{"Dates and folders", []string{"tz", "date-format", "unknown-dir", "albums", "albums-dir"}},
	{"Which files", []string{"include-ext", "exclude-ext", "skip-short-videos"}},
	{"Performance", []string{"workers", "max-buffer-mb"}},
	{"Configuration", []string{"config"}},
}

// usage prints the help text: usageIntro, then the flags by group. Deprecated flags
// are hidden.
func usage(fs *flag.FlagSet) {
	w := fs.Output()
	fmt.Fprint(w, usageIntro)
	for _, g := range flagGroups {
		fmt.Fprintf(w, "\n%s:\n", g.title)
		for _, name := range g.flags {
			if f := fs.Lookup(name); f != nil {
				printFlag(w, f)
			}
		}
	}
	fmt.Fprintf(w, "\nAny flag outside \"Manual import\" can also be set in <library>/%s;\nsee photosorter.example.toml. Full guide: https://github.com/ghills/photosorter#readme\n", config.FileName)
}

// printFlag writes one flag's help the way flag.PrintDefaults does.
func printFlag(w io.Writer, f *flag.Flag) {
	arg, text := flag.UnquoteUsage(f)
	line := "  -" + f.Name
	if arg != "" {
		line += " " + arg
	}
	line += "\n    \t" + strings.ReplaceAll(text, "\n", "\n    \t")
	if def := f.DefValue; def != "" && def != "false" && def != "0" {
		line += " (default " + def + ")"
	}
	fmt.Fprintln(w, line)
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
