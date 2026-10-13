package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	_ "time/tzdata" // lets -tz work on systems without a zoneinfo database
)

const exitInterrupted = 130

func main() {
	os.Exit(run())
}

// run holds the program body so deferred cleanup (flushing the database, closing
// sources and the log) always executes before the process exits.
func run() int {
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "PhotoSorter - Google Photos Takeout Organizer\n\n")
		fmt.Fprintf(flag.CommandLine.Output(), "Usage:\n")
		fmt.Fprintf(flag.CommandLine.Output(), "  photosorter -zip <takeout.zip> -out <destination_folder> [-nosync]\n")
		fmt.Fprintf(flag.CommandLine.Output(), "  photosorter -out <destination_folder> -sync\n\n")
		fmt.Fprintf(flag.CommandLine.Output(), "Flags:\n")
		flag.PrintDefaults()
	}

	zipFlag := flag.String("zip", "", "Path or glob pattern to the Google Photos takeout zip file(s)")
	inFlag := flag.String("in", "", "Path to a local input folder of media files")
	outFlag := flag.String("out", "", "Path to the output folder")
	syncFlag := flag.Bool("sync", false, "Sync database with existing files in the output folder")
	nosyncFlag := flag.Bool("nosync", false, "Prevent automatic library sync from running")
	workersFlag := flag.Int("workers", 8, "Number of concurrent workers")
	dryRunFlag := flag.Bool("dry-run", false, "Simulate extraction without writing to disk")
	tzFlag := flag.String("tz", "", "IANA timezone (e.g. America/New_York) used to turn timestamps into dates; defaults to this machine's timezone")
	skipShortVideosFlag := flag.Int("skip-short-videos", 3, "Skip importing videos shorter than or equal to this many seconds (e.g. motion photos)")
	flag.Parse()

	if *outFlag == "" {
		fmt.Println("An output folder (-out) is required.")
		return 1
	}

	if *tzFlag != "" {
		loc, err := time.LoadLocation(*tzFlag)
		if err != nil {
			fmt.Printf("Invalid -tz %q: %v\n", *tzFlag, err)
			return 1
		}
		time.Local = loc
	}

	var sources []InputSource
	// Shared so a sidecar in one zip part can describe media in another.
	sidecars := NewSidecarIndex()

	if *inFlag != "" {
		_, err := os.Stat(*inFlag)
		if err != nil {
			fmt.Printf("Input directory does not exist or cannot be accessed: %s\n", *inFlag)
			return 1
		}
		if pathsOverlap(*inFlag, *outFlag) {
			fmt.Printf("The input folder (%s) and output folder (%s) must not contain one another.\n", *inFlag, *outFlag)
			return 1
		}
		sources = append(sources, NewLocalFolderSource(*inFlag, sidecars))
	}

	if *zipFlag != "" {
		if strings.Contains(*zipFlag, "*") || strings.Contains(*zipFlag, "?") {
			matches, err := filepath.Glob(*zipFlag)
			if err != nil || len(matches) == 0 {
				fmt.Printf("No zip files matched pattern: %s\n", *zipFlag)
				return 1
			}
			for _, m := range matches {
				src, err := NewZipSource(m, sidecars)
				if err != nil {
					fmt.Printf("Failed to open zip source %s: %v\n", m, err)
					return 1
				}
				sources = append(sources, src)
			}
		} else {
			src, err := NewZipSource(*zipFlag, sidecars)
			if err != nil {
				fmt.Printf("Input zip file does not exist or is invalid: %s\n", *zipFlag)
				return 1
			}
			sources = append(sources, src)
		}
	}

	defer func() {
		for _, src := range sources {
			src.Close()
		}
	}()

	if len(sources) == 0 && !*syncFlag {
		fmt.Println("Usage: photosorter -in <path> -out <path>  OR  photosorter -zip <path> -out <path>")
		return 1
	}

	// First Ctrl-C / SIGTERM stops work gracefully; a second one restores the default and kills the process.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		stop()
	}()

	fmt.Println("Initializing output directory and database cache...")
	db, trackedCount, err := InitDB(*outFlag)
	if err != nil {
		fmt.Printf("Failed to initialize database: %v\n", err)
		return 1
	}
	dbClosed := false
	closeDB := func() int {
		if dbClosed {
			return 0
		}
		dbClosed = true
		if err := db.Close(); err != nil {
			fmt.Printf("Error: failed to save records to the database: %v\n", err)
			fmt.Println("Files already written may be re-imported on the next run; run with -sync to rebuild the cache.")
			return 1
		}
		return 0
	}
	defer closeDB()
	fmt.Printf("Database cache loaded: %d files tracked.\n", trackedCount)

	// check for auto-sync conditions
	needsSync := *syncFlag
	if !needsSync && !*nosyncFlag {
		fmt.Println("Counting files in library...")
		mediaCount, _ := CountMediaFiles(*outFlag)
		// need to initialize database if no hashes are saved yet
		if mediaCount > 0 && trackedCount == 0 {
			fmt.Printf("Detected %d physical media files but database has not been built. Auto-syncing...\n", mediaCount)
			needsSync = true
		} else if mediaCount > trackedCount {
			fmt.Printf("Warning: Detected %d physical media files but only %d in database. Database may need to be synced...\n", mediaCount, trackedCount)
		}
	}

	if needsSync {
		err = SyncFolderWithDB(ctx, *outFlag, db)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				fmt.Println("\nInterrupted during sync.")
				closeDB()
				return exitInterrupted
			}
			fmt.Printf("Sync failed: %v\n", err)
			return 1
		}
	}

	var logger *log.Logger
	logFilePath := filepath.Join(*outFlag, fmt.Sprintf("photosorter_%s.log", time.Now().Format("20060102_150405")))
	logFile, err := os.OpenFile(logFilePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if err == nil {
		logger = log.New(logFile, "", log.LstdFlags)
		fmt.Printf("Logging detailed output to: %s\n", logFilePath)
		defer logFile.Close()
	} else {
		fmt.Printf("Warning: failed to open log file, detailed logs will not be saved: %v\n", err)
		// Fallback to a discard logger if it fails to open
		logger = log.New(os.Stderr, "", log.LstdFlags)
	}

	if len(sources) > 0 {
		processor := NewProcessor(sources, *outFlag, db, *workersFlag, *skipShortVideosFlag, *dryRunFlag, logger)
		err = processor.Run(ctx)
		if errors.Is(err, context.Canceled) {
			fmt.Println("Interrupted. Finished files were recorded; re-run to continue.")
			closeDB()
			return exitInterrupted
		}
		var failed *FilesFailedError
		if errors.As(err, &failed) {
			fmt.Printf("Finished, but %d file(s) could not be processed. See the log for details.\n", failed.Count)
			closeDB()
			return 1
		}
		if err != nil {
			fmt.Printf("Pipeline error: %v\n", err)
			closeDB()
			return 1
		}
	}

	if code := closeDB(); code != 0 {
		return code
	}
	fmt.Println("Done!")
	return 0
}
