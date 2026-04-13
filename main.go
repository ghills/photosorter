package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func main() {
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
	skipShortVideosFlag := flag.Int("skip-short-videos", 3, "Skip importing videos shorter than or equal to this many seconds (e.g. motion photos)")
	flag.Parse()

	if *outFlag == "" {
		fmt.Println("An output folder (-out) is required.")
		os.Exit(1)
	}

	var sources []InputSource

	if *inFlag != "" {
		_, err := os.Stat(*inFlag)
		if err != nil {
			fmt.Printf("Input directory does not exist or cannot be accessed: %s\n", *inFlag)
			os.Exit(1)
		}
		sources = append(sources, NewLocalFolderSource(*inFlag))
	}

	if *zipFlag != "" {
		if strings.Contains(*zipFlag, "*") || strings.Contains(*zipFlag, "?") {
			matches, err := filepath.Glob(*zipFlag)
			if err != nil || len(matches) == 0 {
				fmt.Printf("No zip files matched pattern: %s\n", *zipFlag)
				os.Exit(1)
			}
			for _, m := range matches {
				src, err := NewZipSource(m)
				if err != nil {
					fmt.Printf("Failed to open zip source %s: %v\n", m, err)
					os.Exit(1)
				}
				sources = append(sources, src)
			}
		} else {
			src, err := NewZipSource(*zipFlag)
			if err != nil {
				fmt.Printf("Input zip file does not exist or is invalid: %s\n", *zipFlag)
				os.Exit(1)
			}
			sources = append(sources, src)
		}
	}

	if len(sources) == 0 && !*syncFlag {
		fmt.Println("Usage: photosorter -in <path> -out <path>  OR  photosorter -zip <path> -out <path>")
		os.Exit(1)
	}

	fmt.Println("Initializing output directory and database cache...")
	db, trackedCount, err := InitDB(*outFlag)
	if err != nil {
		fmt.Printf("Failed to initialize database: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()
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
		err = SyncFolderWithDB(*outFlag, db)
		if err != nil {
			fmt.Printf("Sync failed: %v\n", err)
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
		err = processor.Run()
		if err != nil {
			fmt.Printf("Pipeline error: %v\n", err)
			os.Exit(1)
		}
	}

	fmt.Println("Done!")
}
