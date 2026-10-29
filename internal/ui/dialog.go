package ui

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
)

// Kinds of picker the page can ask for.
const (
	pickFolder = "folder" // one folder
	pickZips   = "zips"   // one or more zip files
)

// showPicker shows the system's folder or file picker (see pick). It is killed when
// ctx ends. Tests replace it.
var showPicker = pick

// runPicker runs a dialog command and returns the paths it prints, one per line.
// Every picker used exits non-zero when the user cancels, either silently or, for
// osascript, with error -128. That is not an error: it returns no paths. Some
// pickers also print toolkit warnings while exiting with their documented cancel
// status; cancelCodes lists those statuses, which count as a cancel whatever stderr
// says.
func runPicker(cmd *exec.Cmd, cancelCodes ...int) ([]string, error) {
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		msg := strings.TrimSpace(stderr.String())
		if errors.As(err, &exit) && (msg == "" || strings.Contains(msg, "-128") || slices.Contains(cancelCodes, exit.ExitCode())) {
			return nil, nil
		}
		if msg != "" {
			return nil, fmt.Errorf("file picker failed: %s", msg)
		}
		return nil, fmt.Errorf("file picker failed: %w", err)
	}
	var paths []string
	for _, line := range strings.Split(stdout.String(), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			paths = append(paths, filepath.Clean(line))
		}
	}
	return paths, nil
}
