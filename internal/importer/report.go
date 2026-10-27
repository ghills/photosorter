package importer

import (
	"fmt"
	"io"
	"sync"
)

// Report summarises an import run.
type Report struct {
	// DryRun is set when nothing was actually written.
	DryRun bool

	// Imported counts imported files by how their date was found.
	Imported map[DateSource]int

	// DateConflicts counts files filed under a forced date that have a different date
	// of their own (EXIF, sidecar or file name). The log lists them.
	DateConflicts int

	// XMPWritten counts XMP sidecars written (in a dry run, that would be written) for
	// files dated from their Takeout sidecar. XMPFailed counts those that could not be written; the files were still
	// imported, and the log says why.
	XMPWritten, XMPFailed int

	Duplicates  int // already in the library (or earlier in this run)
	ShortVideos int // skipped by the short-video rule
	Failed      int // could not be imported; see the log
	Interrupted int // aborted mid-copy by cancellation; safe to re-run
	Unreadable  int // paths a folder source could not read
}

// TotalImported is the number of files imported, whatever their date source.
func (r Report) TotalImported() int {
	n := 0
	for _, c := range r.Imported {
		n += c
	}
	return n
}

// dateLabels describes each DateSource in the printed report.
var dateLabels = map[DateSource]string{
	DateFromEXIF:         "dated from EXIF",
	DateFromJSONTaken:    "dated from JSON (taken)",
	DateFromVideo:        "dated from video metadata",
	DateFromJSONCreation: "dated from JSON (created)",
	DateFromFilename:     "dated from file name",
	DateUnknown:          "date unknown",
	DateForced:           "dated by -date",
	DateFallback:         "dated by -fallback-date",
}

// optionalSources are only printed when they were used.
var optionalSources = map[DateSource]bool{DateForced: true, DateFallback: true}

// Print writes the report as an aligned table.
func (r Report) Print(w io.Writer) {
	row := func(label string, n int) { fmt.Fprintf(w, "%-28s: %d\n", label, n) }

	fmt.Fprintln(w, "\n--- Summary ---")
	if r.DryRun {
		fmt.Fprintln(w, "[DRY RUN: nothing was written]")
	}
	row("Imported", r.TotalImported())
	for _, src := range dateSources {
		if optionalSources[src] && r.Imported[src] == 0 {
			continue
		}
		row("  "+dateLabels[src], r.Imported[src])
		if src == DateForced && r.DateConflicts > 0 {
			row("    own date differed (log)", r.DateConflicts)
		}
	}
	if r.XMPWritten > 0 {
		label := "XMP sidecars written"
		if r.DryRun {
			label = "XMP sidecars to write"
		}
		row(label, r.XMPWritten)
	}
	if r.XMPFailed > 0 {
		row("XMP sidecars failed (log)", r.XMPFailed)
	}
	row("Duplicates skipped", r.Duplicates)
	row("Short videos skipped", r.ShortVideos)
	row("Failed", r.Failed)
	if r.Unreadable > 0 {
		row("Unreadable paths skipped", r.Unreadable)
	}
	if r.Interrupted > 0 {
		row("Interrupted (safe to re-run)", r.Interrupted)
	}
}

// tally accumulates a Report from concurrent workers.
type tally struct {
	mu sync.Mutex
	r  Report
}

func newTally(dryRun bool) *tally {
	return &tally{r: Report{DryRun: dryRun, Imported: make(map[DateSource]int)}}
}

func (t *tally) add(update func(*Report)) {
	t.mu.Lock()
	defer t.mu.Unlock()
	update(&t.r)
}

// report returns a copy of the current totals.
func (t *tally) report() Report {
	t.mu.Lock()
	defer t.mu.Unlock()
	r := t.r
	r.Imported = make(map[DateSource]int, len(t.r.Imported))
	for k, v := range t.r.Imported {
		r.Imported[k] = v
	}
	return r
}
