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
//   - A run's settings are layered the way flags are. When the library is the one
//     the program started with, the base is those settings resolved again (defaults,
//     the config file as it is now, flags given with -ui), so edits to the file apply
//     without a restart. For another library it's the defaults plus that library's
//     photosorter.toml. The form's fields go on top, and the page and server reject
//     fields the other doesn't know, so they can't silently drift apart.
//   - The page asks first whether to import or to maintain the library, and shows
//     only that task's fields. The server clears the other task's fields before a
//     run, so nothing hidden on the page changes what it does.
//   - Output is the text the command line prints. [console] splits it into lines,
//     treats carriage-return redraws as the progress bar, and streams both to the
//     page as server-sent events. A backlog lets a reloaded page catch up.
//   - Only one run happens at a time. Stop cancels it like Ctrl-C does, so it ends
//     cleanly and is safe to re-run.
//   - Folder and file pickers are the operating system's own dialogs. The program
//     shells out to osascript, PowerShell, zenity or kdialog to show them, because a
//     browser can't give the program a full path. A picker is closed when its page
//     goes away or the program quits. If no picker is available, paths can be typed
//     instead. Typed paths must be full paths (~ is expanded): the working directory
//     of a double-clicked program is not something the user can see.
//
// The server listens only on 127.0.0.1. Every API call must carry a random token
// that is only in the URL the program opens, and must be addressed to a local host
// name. Other web pages, including ones that rebind DNS to 127.0.0.1, can't start
// runs or open dialogs.
package ui
