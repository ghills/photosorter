package ui

import (
	"bytes"
	"regexp"
	"strings"
	"sync"
)

// maxBacklog caps how many events a run keeps for pages that connect late. The
// oldest lines are dropped first.
const maxBacklog = 5000

// subscriberBuffer is how many events may queue for one page. A page that falls this
// far behind is disconnected. Its EventSource reconnects and catches up from the
// backlog, so a slow page never holds up the run.
const subscriberBuffer = 512

// event is one message to the page.
type event struct {
	// Type is "start" (a run began; clear the output), "line", "progress" (replaces
	// the progress line; empty Text hides it), "done" (the run ended with Code) or
	// "reset" (sent first on every connection; the backlog follows).
	Type string `json:"type"`
	Text string `json:"text,omitempty"`

	// Kind marks a line as a "warning" or an "error".
	Kind string `json:"kind,omitempty"`
	Code int    `json:"code"`
}

// ansi matches terminal escape sequences, which mean nothing in the page.
var ansi = regexp.MustCompile(`\x1b\[[0-9;?]*[A-Za-z]`)

// console is the io.Writer a run's output goes to. It turns the text into events
// for the page.
//
// A line ends with "\n". The progress bar redraws its line by starting it with "\r",
// so a segment ending in "\r", or text after one, is the progress bar's current
// state. When the bar finishes it ends its last state with "\n", which keeps that
// state as an ordinary line.
type console struct {
	mu       sync.Mutex
	partial  []byte
	redraw   bool // the text in partial follows a "\r"
	backlog  []event
	progress string
	subs     map[chan event]struct{}
}

func newConsole() *console {
	return &console{subs: map[chan event]struct{}{}}
}

// begin clears the previous run's output and announces a new run.
func (c *console) begin() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.partial, c.redraw, c.backlog, c.progress = nil, false, nil, ""
	c.add(event{Type: "start"})
}

// end flushes any unterminated output and announces the run's exit code.
func (c *console) end(code int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.redraw && len(c.partial) > 0 {
		c.line(string(c.partial))
	}
	c.partial, c.redraw = nil, false
	c.setProgress("")
	c.add(event{Type: "done", Code: code})
}

func (c *console) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.partial = append(c.partial, p...)
	for {
		i := bytes.IndexAny(c.partial, "\r\n")
		if i < 0 {
			break
		}
		seg := string(c.partial[:i])
		if c.partial[i] == '\n' {
			c.line(seg)
			c.setProgress("")
			c.redraw = false
		} else {
			if clean(seg) != "" {
				c.setProgress(seg)
			}
			c.redraw = true
		}
		c.partial = c.partial[i+1:]
	}
	if c.redraw && len(c.partial) > 0 {
		c.setProgress(string(c.partial))
	}
	return len(p), nil
}

// line records one finished line. Empty lines are kept, because the summary uses
// them to separate sections.
func (c *console) line(s string) {
	s = clean(s)
	ev := event{Type: "line", Text: s}
	switch {
	case strings.HasPrefix(s, "error:"):
		ev.Kind = "error"
	case strings.HasPrefix(s, "warning:"):
		ev.Kind = "warning"
	}
	c.add(ev)
}

func (c *console) setProgress(s string) {
	s = clean(s)
	if s == c.progress {
		return
	}
	c.progress = s
	c.broadcast(event{Type: "progress", Text: s})
}

func clean(s string) string {
	return strings.TrimRight(ansi.ReplaceAllString(s, ""), " \t")
}

// add appends ev to the backlog and sends it to every page.
func (c *console) add(ev event) {
	if len(c.backlog) == maxBacklog {
		// Slicing off the front is O(1); append reallocates once the capacity runs out.
		c.backlog = c.backlog[1:]
	}
	c.backlog = append(c.backlog, ev)
	c.broadcast(ev)
}

func (c *console) broadcast(ev event) {
	for ch := range c.subs {
		select {
		case ch <- ev:
		default:
			delete(c.subs, ch)
			close(ch)
		}
	}
}

// subscribe returns a channel of future events and the events a new page needs to
// show the current state. The channel is closed if the page falls too far behind.
func (c *console) subscribe() (chan event, []event) {
	c.mu.Lock()
	defer c.mu.Unlock()
	ch := make(chan event, subscriberBuffer)
	c.subs[ch] = struct{}{}
	snapshot := append([]event{{Type: "reset"}}, c.backlog...)
	if c.progress != "" {
		snapshot = append(snapshot, event{Type: "progress", Text: c.progress})
	}
	return ch, snapshot
}

// unsubscribe stops sending to ch.
func (c *console) unsubscribe(ch chan event) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.subs[ch]; ok {
		delete(c.subs, ch)
		close(ch)
	}
}
