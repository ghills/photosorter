// Command photosorter imports Google Takeout archives into a local photo library.
//
// It reads Takeout zips (or extracted folders) directly, skips media the library
// already contains, and files everything else into dated or album folders using
// EXIF data, Google Photos JSON sidecars, or the file name. Run
// "photosorter -h" for usage; see README.md for details.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	_ "time/tzdata" // lets -tz work on systems without a zoneinfo database

	"photosorter/internal/cli"
)

func main() {
	// The first Ctrl-C or SIGTERM stops the run gracefully. Once it arrives, the
	// default handling is restored, so a second one kills the process.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	go func() {
		<-ctx.Done()
		stop()
	}()

	code := cli.Run(ctx, os.Args[1:], os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}
