package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"photosorter/internal/config"
	"photosorter/internal/ui"
)

// fakeUI replaces the browser interface for the test and returns the options it was
// started with, or nil if it wasn't.
func fakeUI(t *testing.T, err error) **ui.Options {
	t.Helper()
	var got *ui.Options
	orig := serveUI
	serveUI = func(_ context.Context, opts ui.Options) error {
		got = &opts
		return err
	}
	t.Cleanup(func() { serveUI = orig })
	return &got
}

func TestRun_StartsTheInterface(t *testing.T) {
	lib := t.TempDir()
	for name, args := range map[string][]string{
		"no arguments": nil,
		"-ui":          {"-ui", "-out", lib, "-dry-run"},
	} {
		t.Run(name, func(t *testing.T) {
			got := fakeUI(t, nil)
			if code, out := runCLI(t, args...); code != ExitOK {
				t.Fatalf("exit code %d: %s", code, out)
			}
			opts := *got
			if opts == nil {
				t.Fatal("the interface was not started")
			}
			if opts.Execute == nil {
				t.Error("no Execute function passed")
			}
			if len(args) > 0 && (opts.Initial.Library != lib || !opts.Initial.DryRun) {
				t.Errorf("flags given with -ui not passed on: %+v", opts.Initial)
			}
		})
	}
}

func TestRun_InterfaceFailure(t *testing.T) {
	fakeUI(t, errors.New("no network"))
	if code, out := runCLI(t, "-ui"); code != ExitFailure {
		t.Errorf("exit code %d, want %d: %s", code, ExitFailure, out)
	}
}

func TestRun_FlagsStillRunTheCLI(t *testing.T) {
	got := fakeUI(t, nil)
	if code, out := runCLI(t, "-out", t.TempDir(), "-sync"); code != ExitOK {
		t.Fatalf("exit code %d: %s", code, out)
	}
	if *got != nil {
		t.Error("the interface was started")
	}
}

func TestRun_InterfaceReloadSeesConfigFileEdits(t *testing.T) {
	lib := t.TempDir()
	path := filepath.Join(lib, config.FileName)
	writeFile(t, path, "workers = 2\n")
	got := fakeUI(t, nil)
	if code, out := runCLI(t, "-ui", "-out", lib, "-dry-run"); code != ExitOK {
		t.Fatalf("exit code %d: %s", code, out)
	}
	opts := *got
	if opts.Initial.Workers != 2 {
		t.Fatalf("initial workers = %d, want 2", opts.Initial.Workers)
	}

	writeFile(t, path, "workers = 5\n")
	cfg, file, err := opts.Reload()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Workers != 5 || !cfg.DryRun || cfg.Library != lib || file != path {
		t.Errorf("reloaded workers=%d dryRun=%v library=%q file=%q; want 5, true (from -dry-run), %q, %q", cfg.Workers, cfg.DryRun, cfg.Library, file, lib, path)
	}

	os.WriteFile(path, []byte("bogus = 1\n"), 0644)
	if _, _, err := opts.Reload(); err == nil || !strings.Contains(err.Error(), "bogus") {
		t.Errorf("reload of a broken config file: err = %v", err)
	}
}

func TestExecute_ValidatesItsSettings(t *testing.T) {
	cfg := config.Default()
	cfg.Library = t.TempDir()
	cfg.Sync = true
	cfg.TimeZone = "Mars/Olympus_Mons"
	var out bytes.Buffer
	if code := Execute(context.Background(), cfg, &out, &out); code != ExitFailure {
		t.Errorf("exit code %d, want %d", code, ExitFailure)
	}
	if !strings.Contains(out.String(), "invalid time zone") {
		t.Errorf("output %q does not explain the error", out.String())
	}
	if entries, _ := os.ReadDir(cfg.Library); len(entries) > 0 {
		t.Errorf("library was touched: %v", entries)
	}
}
