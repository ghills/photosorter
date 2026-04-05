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
		fmt.Fprintf(flag.CommandLine.Output(), "  photosorter -zip <takeout.zip> -out <destination_folder>\n\n")
		fmt.Fprintf(flag.CommandLine.Output(), "Flags:\n")
		flag.PrintDefaults()
	}

	zipFlag := flag.String("zip", "", "Path to the Google Photos takeout zip file")
	outFlag := flag.String("out", "", "Path to the output folder")
	flag.Parse()

	if *zipFlag == "" || *outFlag == "" {
		fmt.Println("Usage: photosorter -zip <path> -out <path>")
		os.Exit(1)
	}

	_, err := os.Stat(*zipFlag)
	if os.IsNotExist(err) {
		fmt.Printf("Input zip file does not exist: %s\n", *zipFlag)
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

	processor := NewProcessor(*zipFlag, *outFlag, db)
	err = processor.Run()
	if err != nil {
		fmt.Printf("Pipeline error: %v\n", err)
		os.Exit(1)
	}
	
	fmt.Println("Done!")
}
