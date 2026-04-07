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
	outFlag := flag.String("out", "", "Path to the output folder")
	syncFlag := flag.Bool("sync", false, "Sync database with existing files in the output folder")
	nosyncFlag := flag.Bool("nosync", false, "Prevent automatic library sync from running")
	workersFlag := flag.Int("workers", 8, "Number of concurrent workers")
	dryRunFlag := flag.Bool("dry-run", false, "Simulate extraction without writing to disk")
	flag.Parse()

	if *outFlag == "" {
		fmt.Println("An output folder (-out) is required.")
		os.Exit(1)
	}

	var zipFiles []string
	if *zipFlag != "" {
		if strings.Contains(*zipFlag, "*") || strings.Contains(*zipFlag, "?") {
			matches, err := filepath.Glob(*zipFlag)
			if err != nil || len(matches) == 0 {
				fmt.Printf("No zip files matched pattern: %s\n", *zipFlag)
				os.Exit(1)
			}
			zipFiles = matches
		} else {
			_, err := os.Stat(*zipFlag)
			if os.IsNotExist(err) {
				fmt.Printf("Input zip file does not exist: %s\n", *zipFlag)
				os.Exit(1)
			}
			zipFiles = append(zipFiles, *zipFlag)
		}
	} else if !*syncFlag {
		fmt.Println("Usage: photosorter -zip <path> -out <path>  OR  photosorter -out <path> -sync")
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

	needsSync := *syncFlag
	if !needsSync && !*nosyncFlag {
		mediaCount, _ := CountMediaFiles(*outFlag)
		// Small buffer: if the output folder has more media files than what the DB tracks, auto-sync.
		if mediaCount > trackedCount {
			fmt.Printf("Detected %d physical media files but only %d in database. Auto-syncing...\n", mediaCount, trackedCount)
			needsSync = true
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

	if len(zipFiles) > 0 {
		processor := NewProcessor(zipFiles, *outFlag, db, *workersFlag, *dryRunFlag, logger)
		err = processor.Run()
		if err != nil {
			fmt.Printf("Pipeline error: %v\n", err)
			os.Exit(1)
		}
	}
	
	fmt.Println("Done!")
}
