package ui

import "os/exec"

// pick shows the macOS folder or file chooser through AppleScript.
func pick(kind string) ([]string, error) {
	script := []string{`POSIX path of (choose folder with prompt "Choose a folder")`}
	if kind == pickZips {
		script = []string{
			`set chosen to choose file with prompt "Choose zip files" of type {"public.zip-archive"} with multiple selections allowed`,
			`set out to ""`,
			`repeat with f in chosen`,
			`set out to out & POSIX path of f & linefeed`,
			`end repeat`,
			`return out`,
		}
	}
	var args []string
	for _, line := range script {
		args = append(args, "-e", line)
	}
	return runPicker(exec.Command("osascript", args...))
}

// openPath opens a URL in the default browser, or a folder in Finder.
func openPath(target string) error {
	return exec.Command("open", target).Start()
}
