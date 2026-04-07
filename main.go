package main

import (
	"flag"
	"fmt"
	"os"
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

	zipFlag := flag.String("zip", "", "Path to the Google Photos takeout zip file")
	outFlag := flag.String("out", "", "Path to the output folder")
	syncFlag := flag.Bool("sync", false, "Sync database with existing files in the output folder")
	nosyncFlag := flag.Bool("nosync", false, "Prevent automatic library sync from running")
	flag.Parse()

	if *outFlag == "" {
		fmt.Println("An output folder (-out) is required.")
		os.Exit(1)
	}

	if *zipFlag != "" {
		_, err := os.Stat(*zipFlag)
		if os.IsNotExist(err) {
			fmt.Printf("Input zip file does not exist: %s\n", *zipFlag)
			os.Exit(1)
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

	if *zipFlag != "" {
		processor := NewProcessor(*zipFlag, *outFlag, db)
		err = processor.Run()
		if err != nil {
			fmt.Printf("Pipeline error: %v\n", err)
			os.Exit(1)
		}
	}
	
	fmt.Println("Done!")
}
