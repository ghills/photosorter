// Package parallel runs a function over a list of items with a fixed number of
// workers and an optional terminal progress bar. Both the import and the library
// sync use it.
package parallel

import (
	"context"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/schollz/progressbar/v3"
)

// Options configures [ForEach].
type Options struct {
	// Workers is the number of concurrent calls. Values below 1 mean 1.
	Workers int

	// Stop, if set, is checked before each item. Once it returns true no further items
	// are started. Context cancellation is always honored.
	Stop func() bool

	// Progress receives a progress bar, typically os.Stderr. Nil disables it.
	Progress io.Writer

	// Label is shown in front of the progress bar.
	Label string
}

// ForEach calls fn for every item using opts.Workers goroutines and waits for them to
// finish. When ctx is cancelled or opts.Stop returns true, in-flight calls finish and
// the remaining items are skipped. Callers check ctx.Err() to tell the cases apart.
func ForEach[T any](ctx context.Context, items []T, opts Options, fn func(T)) {
	if len(items) == 0 {
		return
	}
	workers := max(opts.Workers, 1)
	bar := newBar(opts.Progress, len(items), opts.Label)

	queue := make(chan T, len(items))
	for _, it := range items {
		queue <- it
	}
	close(queue)

	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for it := range queue {
				if ctx.Err() != nil || (opts.Stop != nil && opts.Stop()) {
					return
				}
				fn(it)
				bar.Add(1)
			}
		}()
	}
	wg.Wait()
}

// newBar mirrors progressbar.Default but writes to w. With a nil w it returns a
// silent bar so callers never need a nil check.
func newBar(w io.Writer, total int, label string) *progressbar.ProgressBar {
	if w == nil {
		return progressbar.DefaultSilent(int64(total), label)
	}
	return progressbar.NewOptions64(int64(total),
		progressbar.OptionSetDescription(label),
		progressbar.OptionSetWriter(w),
		progressbar.OptionSetWidth(10),
		progressbar.OptionThrottle(65*time.Millisecond),
		progressbar.OptionShowCount(),
		progressbar.OptionShowIts(),
		progressbar.OptionOnCompletion(func() { fmt.Fprintln(w) }),
		progressbar.OptionSpinnerType(14),
		progressbar.OptionFullWidth(),
		progressbar.OptionSetRenderBlankState(true),
	)
}
