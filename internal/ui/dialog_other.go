//go:build !darwin && !windows

package ui

import (
	"context"
	"errors"
	"os/exec"
)

// errNoPicker means this system has no dialog program to show a picker with.
var errNoPicker = errors.New("no file picker is available on this system; type the path instead")

// pick shows a folder or file chooser with zenity (GNOME) or kdialog (KDE),
// whichever is installed.
func pick(ctx context.Context, kind string) ([]string, error) {
	if _, err := exec.LookPath("zenity"); err == nil {
		args := []string{"--file-selection", "--title=Choose a folder", "--directory"}
		if kind == pickZips {
			args = []string{"--file-selection", "--title=Choose zip files", "--multiple", "--separator=\n", "--file-filter=Zip files | *.zip"}
		}
		return runPicker(exec.CommandContext(ctx, "zenity", args...))
	}
	if _, err := exec.LookPath("kdialog"); err == nil {
		args := []string{"--title", "Choose a folder", "--getexistingdirectory", "."}
		if kind == pickZips {
			args = []string{"--title", "Choose zip files", "--getopenfilename", ".", "*.zip", "--multiple", "--separate-output"}
		}
		return runPicker(exec.CommandContext(ctx, "kdialog", args...))
	}
	return nil, errNoPicker
}

// openPath opens a URL in the default browser, or a folder in the file manager.
func openPath(target string) error {
	return exec.Command("xdg-open", target).Start()
}
