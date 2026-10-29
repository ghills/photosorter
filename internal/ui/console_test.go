package ui

import (
	"fmt"
	"reflect"
	"sync"
	"testing"
)

// texts returns the backlog's events after "start" as "type:text" strings.
func texts(c *console) []string {
	var out []string
	for _, ev := range c.backlog[1:] {
		s := ev.Type + ":" + ev.Text
		if ev.Kind != "" {
			s += ":" + ev.Kind
		}
		if ev.Type == "done" {
			s = fmt.Sprintf("done:%d", ev.Code)
		}
		out = append(out, s)
	}
	return out
}

func TestConsole_LinesAndProgress(t *testing.T) {
	c := newConsole()
	c.begin()
	ch, _ := c.subscribe()

	// Lines may arrive in pieces; the progress bar redraws with "\r", clears itself
	// with spaces, and keeps its final state as a line.
	for _, s := range []string{"Sync", "ing library...\n", "\r\x1b[2K 10% |#   |", "\r 50% |##  |", "\r      \r", "\r100% |####|\n", "warning: could not read 1 path(s):\n", "\n", "error: boom\n", "tail"} {
		c.Write([]byte(s))
	}
	c.end(1)

	want := []string{"line:Syncing library...", "line:100% |####|", "line:warning: could not read 1 path(s)::warning", "line:", "line:error: boom:error", "line:tail", "done:1"}
	if got := texts(c); !reflect.DeepEqual(got, want) {
		t.Errorf("backlog =\n%q\nwant\n%q", got, want)
	}

	var progress []string
	for len(ch) > 0 {
		if ev := <-ch; ev.Type == "progress" {
			progress = append(progress, ev.Text)
		}
	}
	if want := []string{" 10% |#   |", " 50% |##  |", ""}; !reflect.DeepEqual(progress, want) {
		t.Errorf("progress = %q, want %q", progress, want)
	}
}

func TestConsole_SnapshotCatchesUp(t *testing.T) {
	c := newConsole()
	c.begin()
	c.Write([]byte("one\n\r 5% |"))
	_, snap := c.subscribe()
	var got []string
	for _, ev := range snap {
		got = append(got, ev.Type+":"+ev.Text)
	}
	if want := []string{"reset:", "start:", "line:one", "progress: 5% |"}; !reflect.DeepEqual(got, want) {
		t.Errorf("snapshot = %q, want %q", got, want)
	}

	// A new run starts with an empty backlog.
	c.end(0)
	c.begin()
	if len(c.backlog) != 1 || c.backlog[0].Type != "start" {
		t.Errorf("backlog after begin = %+v", c.backlog)
	}
}

func TestConsole_BacklogIsCapped(t *testing.T) {
	c := newConsole()
	c.begin()
	for i := range maxBacklog + 10 {
		fmt.Fprintf(c, "line %d\n", i)
	}
	if len(c.backlog) != maxBacklog {
		t.Fatalf("backlog has %d events, want %d", len(c.backlog), maxBacklog)
	}
	if last := c.backlog[maxBacklog-1].Text; last != fmt.Sprintf("line %d", maxBacklog+9) {
		t.Errorf("last event = %q", last)
	}
}

func TestConsole_SlowSubscriberIsDropped(t *testing.T) {
	c := newConsole()
	c.begin()
	ch, _ := c.subscribe()
	for i := range subscriberBuffer + 1 {
		fmt.Fprintf(c, "line %d\n", i)
	}
	n := 0
	for range ch { // ends because the channel was closed
		n++
	}
	if n != subscriberBuffer {
		t.Errorf("received %d events before being dropped, want %d", n, subscriberBuffer)
	}
	c.unsubscribe(ch) // must not close it twice
}

func TestConsole_ConcurrentWrites(t *testing.T) {
	c := newConsole()
	c.begin()
	ch, _ := c.subscribe()
	defer c.unsubscribe(ch)
	var wg sync.WaitGroup
	for w := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range 50 {
				fmt.Fprintf(c, "worker %d line %d\n", w, i)
			}
		}()
	}
	wg.Wait()
	c.end(0)
	if got := len(c.backlog); got != 1+8*50+1 {
		t.Errorf("backlog has %d events, want %d", got, 1+8*50+1)
	}
}
