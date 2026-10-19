package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), FileName)
	if err := os.WriteFile(p, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return p
}

// valid returns a default config with the fields Validate requires.
func valid() Config {
	c := Default()
	c.Library = "/lib"
	c.Sync = true
	return c
}

func TestDefault_IsValidOnceRequiredFieldsAreSet(t *testing.T) {
	if err := valid().Validate(); err != nil {
		t.Errorf("default config invalid: %v", err)
	}
}

func TestLoadFile_OverlaysOnlyKeysPresent(t *testing.T) {
	path := writeConfig(t, `
out = "library"
zip = ["takeout-*.zip", "/abs/other.zip"]
workers = 3

[layout]
date_format = "2006/01"

[media]
exclude_ext = [".gif"]
`)
	cfg := Default()
	if err := LoadFile(path, &cfg); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Dir(path)
	if cfg.Library != filepath.Join(dir, "library") {
		t.Errorf("Library = %q, want it resolved against the file's folder", cfg.Library)
	}
	if cfg.Zips[0] != filepath.Join(dir, "takeout-*.zip") || cfg.Zips[1] != "/abs/other.zip" {
		t.Errorf("Zips = %v", cfg.Zips)
	}
	if cfg.Workers != 3 || cfg.Layout.DateFormat != "2006/01" || cfg.Media.ExcludeExt[0] != ".gif" {
		t.Errorf("file values not applied: %+v", cfg)
	}
	// Untouched keys keep their defaults.
	if cfg.MaxBufferMB != 16 || cfg.Layout.UnknownDir != "Unknown_Date" || cfg.Layout.Albums != AlbumsFolder {
		t.Errorf("defaults lost: %+v", cfg)
	}
}

func TestLoadFile_RejectsUnknownKeys(t *testing.T) {
	path := writeConfig(t, "wokers = 3\n[layout]\ndate_fmt = \"2006\"\n")
	cfg := Default()
	err := LoadFile(path, &cfg)
	if err == nil || !strings.Contains(err.Error(), "wokers") || !strings.Contains(err.Error(), "layout.date_fmt") {
		t.Errorf("err = %v, want both unknown keys named", err)
	}
}

func TestLoadFile_BadSyntax(t *testing.T) {
	cfg := Default()
	if err := LoadFile(writeConfig(t, "workers = "), &cfg); err == nil {
		t.Error("expected a parse error")
	}
	if err := LoadFile(filepath.Join(t.TempDir(), "missing.toml"), &cfg); err == nil {
		t.Error("expected an error for a missing file")
	}
}

func TestFindFile(t *testing.T) {
	lib := t.TempDir()
	if got := FindFile("", lib); got != "" {
		t.Errorf("no file: got %q", got)
	}
	os.WriteFile(filepath.Join(lib, FileName), nil, 0644)
	if got := FindFile("", lib); got != filepath.Join(lib, FileName) {
		t.Errorf("library file: got %q", got)
	}
	if got := FindFile("/explicit.toml", lib); got != "/explicit.toml" {
		t.Errorf("explicit: got %q", got)
	}
}

func TestValidate_Rejects(t *testing.T) {
	tests := map[string]func(*Config){
		"missing library":     func(c *Config) { c.Library = "" },
		"nothing to do":       func(c *Config) { c.Sync = false },
		"zero workers":        func(c *Config) { c.Workers = 0 },
		"negative buffer":     func(c *Config) { c.MaxBufferMB = -1 },
		"negative short":      func(c *Config) { c.SkipShortVideos = -1 },
		"bad zone":            func(c *Config) { c.TimeZone = "Mars/Olympus" },
		"bad extension":       func(c *Config) { c.Media.IncludeExt = []string{"a/b"} },
		"empty date format":   func(c *Config) { c.Layout.DateFormat = "" },
		"absolute date":       func(c *Config) { c.Layout.DateFormat = "/2006" },
		"escaping date":       func(c *Config) { c.Layout.DateFormat = "../2006" },
		"empty segment":       func(c *Config) { c.Layout.DateFormat = "2006//01" },
		"escaping unknown":    func(c *Config) { c.Layout.UnknownDir = "../x" },
		"empty unknown":       func(c *Config) { c.Layout.UnknownDir = "" },
		"bad albums mode":     func(c *Config) { c.Layout.Albums = "sometimes" },
		"escaping albums dir": func(c *Config) { c.Layout.AlbumsDir = ".." },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			c := valid()
			mutate(&c)
			if err := c.Validate(); err == nil {
				t.Error("expected a validation error")
			}
		})
	}
}

func TestValidate_Accepts(t *testing.T) {
	tests := map[string]func(*Config){
		"nested date":            func(c *Config) { c.Layout.DateFormat = "2006/01/02" },
		"year-month":             func(c *Config) { c.Layout.DateFormat = "2006/2006-01" },
		"nested unknown":         func(c *Config) { c.Layout.UnknownDir = "misc/Undated" },
		"albums ignored, no dir": func(c *Config) { c.Layout.Albums = AlbumsIgnore; c.Layout.AlbumsDir = "" },
		"zip only":               func(c *Config) { c.Sync = false; c.Zips = []string{"x.zip"} },
		"reorganize only":        func(c *Config) { c.Sync = false; c.Reorganize = true },
		"zone":                   func(c *Config) { c.TimeZone = "America/New_York" },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			c := valid()
			mutate(&c)
			if err := c.Validate(); err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

// The example file in the repository must stay loadable, including once every
// commented-out setting is enabled.
func TestExampleFileIsValid(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "photosorter.example.toml"))
	if err != nil {
		t.Fatal(err)
	}
	var uncommented []string
	for _, line := range strings.Split(string(data), "\n") {
		// "# key = value" lines are settings; other comments are prose.
		if rest, ok := strings.CutPrefix(line, "# "); ok && strings.Contains(rest, " = ") && !strings.HasPrefix(rest, " ") {
			line = rest
		}
		uncommented = append(uncommented, line)
	}
	for name, content := range map[string]string{"as shipped": string(data), "all enabled": strings.Join(uncommented, "\n")} {
		t.Run(name, func(t *testing.T) {
			cfg := valid() // the shipped file sets no library or inputs
			if err := LoadFile(writeConfig(t, content), &cfg); err != nil {
				t.Fatal(err)
			}
			if err := cfg.Validate(); err != nil {
				t.Errorf("Validate: %v", err)
			}
		})
	}
}
