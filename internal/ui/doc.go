// Package ui is PhotoSorter's browser interface: a single page that fills in the
// same settings as the command line and shows a run's output as it happens.
//
// A native GUI toolkit would need cgo, which would stop the project cross-compiling
// from one machine. So [Serve] starts a small web server inside the program and
// opens the page in the user's browser. The page is embedded in the binary and
// loads nothing from the internet.
//
// The design keeps the browser interface a thin layer over the command line:
//
//   - Runs go through the Execute function in [Options] (cli.Execute), so they
//     use the same pipeline, log file, exit codes and messages as the command line.
//     The package gets the function as a value, so it never imports cli.
//   - A run's settings are layered the way flags are. The base is the settings ui
//     started with when the library is the same, or the defaults plus that library's
//     photosorter.toml when it is not. The form's fields go on top.
//   - Output is the text the command line prints. [console] splits it into lines,
//     treats carriage-return redraws as the progress bar, and streams both to the
//     page as server-sent events. A backlog lets a reloaded page catch up.
//   - Only one run happens at a time. Stop cancels it like Ctrl-C does, so it ends
//     cleanly and is safe to re-run.
//   - Folder and file pickers are the operating system's own dialogs. The program
//     shells out to osascript, PowerShell, zenity or kdialog to show them, because a
//     browser can't give the program a full path. If no picker is available, paths
//     can be typed instead.
//
// The server listens only on 127.0.0.1. Every API call must carry a random token
// that is only in the URL the program opens, and must be addressed to a local host
// name. Other web pages, including ones that rebind DNS to 127.0.0.1, can't start
// runs or open dialogs.
package ui
