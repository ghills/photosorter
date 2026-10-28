package cli

import (
	"context"
	"errors"
	"testing"

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
