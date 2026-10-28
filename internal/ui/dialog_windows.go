package ui

import "os/exec"

// pickerPrelude makes PowerShell print UTF-8, so paths with accents survive, and
// gives the dialog a topmost owner so it doesn't open behind the browser.
const pickerPrelude = `[Console]::OutputEncoding = [System.Text.Encoding]::UTF8
Add-Type -AssemblyName System.Windows.Forms
$owner = New-Object System.Windows.Forms.Form -Property @{TopMost = $true}
`

// pick shows the Windows folder or file dialog through PowerShell.
func pick(kind string) ([]string, error) {
	script := pickerPrelude + `$d = New-Object System.Windows.Forms.FolderBrowserDialog
$d.Description = 'Choose a folder'
if ($d.ShowDialog($owner) -eq 'OK') { $d.SelectedPath }
`
	if kind == pickZips {
		script = pickerPrelude + `$d = New-Object System.Windows.Forms.OpenFileDialog
$d.Title = 'Choose zip files'
$d.Filter = 'Zip files (*.zip)|*.zip'
$d.Multiselect = $true
if ($d.ShowDialog($owner) -eq 'OK') { $d.FileNames }
`
	}
	return runPicker(exec.Command("powershell", "-NoProfile", "-STA", "-Command", script))
}

// openPath opens a URL in the default browser, or a folder in Explorer.
func openPath(target string) error {
	return exec.Command("rundll32", "url.dll,FileProtocolHandler", target).Start()
}
