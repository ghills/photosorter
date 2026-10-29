package cli

import (
	"bytes"
	"errors"
	"flag"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"photosorter/internal/config"
)

func parse(t *testing.T, args ...string) (config.Config, string, error) {
	t.Helper()
	var stderr bytes.Buffer
	p, err := parseArgs(args, &stderr)
	if p.ui {
		t.Fatalf("parseArgs(%q) chose the browser interface", args)
	}
	return p.cfg, stderr.String(), err
}

func TestParseArgs_Defaults(t *testing.T) {
	cfg, _, err := parse(t, "-out", t.TempDir(), "-sync")
	if err != nil {
		t.Fatal(err)
	}
	def := config.Default()
	if cfg.Workers != def.Workers || cfg.Layout != def.Layout || cfg.SkipShortVideos != def.SkipShortVideos {
		t.Errorf("defaults not applied: %+v", cfg)
	}
}

func TestParseArgs_RepeatableAndPositionalInputs(t *testing.T) {
	folder := t.TempDir()
	cfg, _, err := parse(t, "-out", t.TempDir(), "-zip", "a.zip", "-zip", "b-*.zip", "-in", "x", folder, "c.zip")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"a.zip", "b-*.zip", "c.zip"}; !reflect.DeepEqual(cfg.Zips, want) {
		t.Errorf("Zips = %v, want %v", cfg.Zips, want)
	}
	if want := []string{"x", folder}; !reflect.DeepEqual(cfg.Folders, want) {
		t.Errorf("Folders = %v, want %v", cfg.Folders, want)
	}
}

func TestParseArgs_ExtensionListsSplitOnCommas(t *testing.T) {
	cfg, _, err := parse(t, "-out", t.TempDir(), "-sync", "-include-ext", "dng, arw", "-include-ext", "cr2", "-exclude-ext", "gif")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"dng", "arw", "cr2"}; !reflect.DeepEqual(cfg.Media.IncludeExt, want) {
		t.Errorf("IncludeExt = %v, want %v", cfg.Media.IncludeExt, want)
	}
}

func TestParseArgs_DeprecatedNosyncAlias(t *testing.T) {
	cfg, stderr, err := parse(t, "-out", t.TempDir(), "-nosync", "x.zip")
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.NoSync || !strings.Contains(stderr, "-nosync is deprecated") {
		t.Errorf("NoSync = %v, stderr = %q", cfg.NoSync, stderr)
	}
}

func TestParseArgs_ConfigFilePrecedence(t *testing.T) {
	lib := t.TempDir()
	os.WriteFile(filepath.Join(lib, config.FileName), []byte(`
workers = 2
zip = ["from-file.zip"]
[layout]
date_format = "2006/01"
unknown_dir = "Undated"
`), 0644)

	cfg, _, err := parse(t, "-out", lib, "-workers", "5")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Workers != 5 {
		t.Errorf("Workers = %d, want the flag (5) to beat the file (2)", cfg.Workers)
	}
	if cfg.Layout.DateFormat != "2006/01" || cfg.Layout.UnknownDir != "Undated" {
		t.Errorf("Layout = %+v, want file values", cfg.Layout)
	}
	if want := []string{filepath.Join(lib, "from-file.zip")}; !reflect.DeepEqual(cfg.Zips, want) {
		t.Errorf("Zips = %v, want %v", cfg.Zips, want)
	}

	// A -zip flag replaces the file's list rather than adding to it.
	cfg, _, _ = parse(t, "-out", lib, "-zip", "flag.zip")
	if !reflect.DeepEqual(cfg.Zips, []string{"flag.zip"}) {
		t.Errorf("Zips = %v, want only the flag value", cfg.Zips)
	}
}

func TestParseArgs_ExplicitConfigFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "custom.toml")
	os.WriteFile(path, []byte("out = \"lib\"\nsync = true\n"), 0644)
	cfg, _, err := parse(t, "-config", path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Library != filepath.Join(dir, "lib") || !cfg.Sync {
		t.Errorf("cfg = %+v", cfg)
	}
}

func TestParseArgs_Errors(t *testing.T) {
	lib := t.TempDir()
	tests := map[string][]string{
		"missing -out":      {"-sync"},
		"nothing to do":     {"-out", lib},
		"bad time zone":     {"-out", lib, "-sync", "-tz", "Mars/Olympus"},
		"bad albums mode":   {"-out", lib, "-sync", "-albums", "maybe"},
		"escaping folder":   {"-out", lib, "-sync", "-unknown-dir", "../x"},
		"unknown flag":      {"-out", lib, "-sync", "-bogus"},
		"bad int":           {"-out", lib, "-sync", "-workers", "many"},
		"bad config file":   {"-out", lib, "-sync", "-config", filepath.Join(lib, "missing.toml")},
		"zero workers":      {"-out", lib, "-sync", "-workers", "0"},
		"bad extension":     {"-out", lib, "-sync", "-include-ext", "a/b"},
		"absolute date fmt": {"-out", lib, "-sync", "-date-format", "/2006"},
	}
	for name, args := range tests {
		t.Run(name, func(t *testing.T) {
			if _, _, err := parse(t, args...); err == nil {
				t.Error("expected an error")
			}
		})
	}
}

func TestParseArgs_Help(t *testing.T) {
	_, stderr, err := parse(t, "-h")
	if !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("err = %v, want flag.ErrHelp", err)
	}
	for _, want := range []string{"Usage:", "Common tasks", "-date 2019-07-04 -event Fireworks", "-fallback-date 1998", "-album Wedding", "-date YYYY[-MM[-DD]]", "-out", "-no-sync", "-date-format", "photosorter.example.toml", "-ui"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("help text missing %q", want)
		}
	}
	if strings.Contains(stderr, "-nosync") {
		t.Error("help text shows the deprecated -nosync flag")
	}
}

func TestParseArgs_UI(t *testing.T) {
	lib := t.TempDir()
	path := filepath.Join(lib, config.FileName)
	os.WriteFile(path, []byte("workers = 3\n"), 0644)
	tests := map[string]struct {
		args   []string
		wantUI bool
	}{
		"no arguments":            {nil, true},
		"-ui alone":               {[]string{"-ui"}, true},
		"-ui with settings":       {[]string{"-ui", "-out", lib, "-dry-run"}, true},
		"-ui=false is the CLI":    {[]string{"-ui=false", "-out", lib, "-sync"}, false},
		"other flags run the CLI": {[]string{"-out", lib, "-sync"}, false},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			p, err := parseArgs(tt.args, io.Discard)
			if err != nil {
				t.Fatal(err)
			}
			if p.ui != tt.wantUI {
				t.Errorf("ui = %v, want %v", p.ui, tt.wantUI)
			}
		})
	}

	// The UI's settings are layered like the CLI's but not validated: with -ui,
	// "nothing to do" is not an error yet.
	p, err := parseArgs([]string{"-ui", "-out", lib}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if p.cfg.Library != lib || p.cfg.Workers != 3 || p.configFile != path {
		t.Errorf("cfg = %+v, configFile = %q", p.cfg, p.configFile)
	}
}
