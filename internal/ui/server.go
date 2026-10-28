package ui

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"photosorter/internal/config"
)

//go:embed static/index.html
var static embed.FS

// tokenHeader carries the API token on fetch calls. EventSource can't set headers,
// so the token can also be given as the "t" query parameter.
const tokenHeader = "X-PhotoSorter-Token"

// Options configures [Serve].
type Options struct {
	// Initial is the settings the program started with, from defaults, the config
	// file and any flags given with -ui. They prefill the form and are the base for
	// runs into the same library.
	Initial config.Config

	// ConfigFile is the config file Initial was loaded from, or "".
	ConfigFile string

	// Execute performs one run with validated settings and returns its exit code, as
	// cli.Execute does.
	Execute func(ctx context.Context, cfg config.Config, stdout, stderr io.Writer) int

	// Stdout receives the page's address and a copy of every run's output, so the
	// terminal window shows what is happening too. Stderr receives warnings.
	Stdout, Stderr io.Writer
}

// openBrowser opens the page. Tests replace it.
var openBrowser = openPath

// Serve starts the browser interface, opens it, and serves it until ctx is
// cancelled or the page's Quit button is used. A run still in progress is stopped
// and waited for before it returns.
func Serve(ctx context.Context, opts Options) error {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("cannot start the browser interface: %w", err)
	}
	s, err := newServer(ctx, opts)
	if err != nil {
		ln.Close()
		return err
	}
	srv := &http.Server{Handler: s.handler(), ReadHeaderTimeout: 10 * time.Second}
	go srv.Serve(ln)

	url := fmt.Sprintf("http://%s/?t=%s", ln.Addr(), s.token)
	fmt.Fprintf(opts.Stdout, "PhotoSorter is open in your web browser at:\n  %s\nKeep this window open while you use it. Press Ctrl-C or close it to quit.\n", url)
	if err := openBrowser(url); err != nil {
		fmt.Fprintf(opts.Stderr, "warning: cannot open the browser (%v); open the address above yourself.\n", err)
	}

	select {
	case <-ctx.Done():
	case <-s.quit:
	}
	s.shutdown()
	// Close rather than Shutdown: event streams never finish on their own.
	srv.Close()
	fmt.Fprintln(opts.Stdout, "PhotoSorter closed.")
	return nil
}

// server holds the state of the browser interface.
type server struct {
	ctx     context.Context
	opts    Options
	token   string
	console *console

	mu       sync.Mutex
	cancel   context.CancelFunc // stops the current run; nil when idle
	runs     sync.WaitGroup
	pickMu   sync.Mutex // one picker at a time
	quit     chan struct{}
	quitOnce sync.Once
}

func newServer(ctx context.Context, opts Options) (*server, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	return &server{
		ctx:     ctx,
		opts:    opts,
		token:   hex.EncodeToString(b),
		console: newConsole(),
		quit:    make(chan struct{}),
	}, nil
}

func (s *server) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.handleIndex)
	mux.HandleFunc("GET /api/settings", s.guard(s.handleSettings))
	mux.HandleFunc("POST /api/pick", s.guard(s.handlePick))
	mux.HandleFunc("POST /api/open", s.guard(s.handleOpen))
	mux.HandleFunc("POST /api/run", s.guard(s.handleRun))
	mux.HandleFunc("POST /api/cancel", s.guard(s.handleCancel))
	mux.HandleFunc("POST /api/quit", s.guard(s.handleQuit))
	mux.HandleFunc("GET /api/events", s.guard(s.handleEvents))
	return mux
}

// guard rejects API calls without the token or addressed to a non-local host name.
func (s *server) guard(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		host, _, err := net.SplitHostPort(r.Host)
		if err != nil {
			host = r.Host
		}
		token := r.Header.Get(tokenHeader)
		if token == "" {
			token = r.URL.Query().Get("t")
		}
		if (host != "127.0.0.1" && host != "localhost") || subtle.ConstantTimeCompare([]byte(token), []byte(s.token)) != 1 {
			writeError(w, http.StatusForbidden, errors.New("forbidden"))
			return
		}
		h(w, r)
	}
}

func (s *server) handleIndex(w http.ResponseWriter, r *http.Request) {
	page, _ := static.ReadFile("static/index.html")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(page)
}

// form is what the page shows and sends: the settings a run commonly needs. The
// rest come from the config file.
type form struct {
	Library string `json:"library"`

	// Inputs are folders and zip files (or zip glob patterns), told apart the way
	// positional command-line arguments are.
	Inputs []string `json:"inputs"`

	// DateMode is "" (files' own dates), "date" (-date) or "fallback"
	// (-fallback-date).
	DateMode string `json:"dateMode"`
	Date     string `json:"date"`

	// NameMode is "" (none), "event" (-event) or "album" (-album).
	NameMode string `json:"nameMode"`
	Name     string `json:"name"`

	DryRun     bool   `json:"dryRun"`
	Albums     string `json:"albums"`
	Sync       bool   `json:"sync"`
	Rehash     bool   `json:"rehash"`
	Prune      bool   `json:"prune"`
	Reorganize bool   `json:"reorganize"`
}

// settings is the reply to /api/settings.
type settings struct {
	Form       form   `json:"form"`
	ConfigFile string `json:"configFile"`
	Running    bool   `json:"running"`
}

// formFrom describes cfg as the page shows it.
func formFrom(cfg config.Config) form {
	f := form{
		Library:    cfg.Library,
		Inputs:     append(append([]string{}, cfg.Folders...), cfg.Zips...),
		DryRun:     cfg.DryRun,
		Albums:     cfg.Layout.Albums,
		Sync:       cfg.Sync,
		Rehash:     cfg.Rehash,
		Prune:      cfg.Prune,
		Reorganize: cfg.Reorganize,
	}
	switch m := cfg.Manual; {
	case m.Date != "":
		f.DateMode, f.Date = "date", m.Date
	case m.FallbackDate != "":
		f.DateMode, f.Date = "fallback", m.FallbackDate
	}
	switch m := cfg.Manual; {
	case m.Album != "":
		f.NameMode, f.Name = "album", m.Album
	case m.Event != "":
		f.NameMode, f.Name = "event", m.Event
	}
	return f
}

// apply puts the form's settings onto cfg. Every field the page shows replaces
// what cfg had, so the page is always what runs.
func (f form) apply(cfg *config.Config) {
	cfg.Library = f.Library
	cfg.Folders, cfg.Zips = nil, nil
	for _, in := range f.Inputs {
		if in = expandHome(strings.TrimSpace(in)); in == "" {
			continue
		}
		if info, err := os.Stat(in); err == nil && info.IsDir() {
			cfg.Folders = append(cfg.Folders, in)
		} else {
			cfg.Zips = append(cfg.Zips, in)
		}
	}
	date := strings.TrimSpace(f.Date)
	name := strings.TrimSpace(f.Name)
	cfg.Manual = config.Manual{}
	switch f.DateMode {
	case "date":
		cfg.Manual.Date = date
	case "fallback":
		cfg.Manual.FallbackDate = date
	}
	switch f.NameMode {
	case "event":
		cfg.Manual.Event = name
	case "album":
		cfg.Manual.Album = name
	}
	if f.Albums != "" {
		cfg.Layout.Albums = f.Albums
	}
	cfg.DryRun, cfg.Sync, cfg.Rehash, cfg.Prune, cfg.Reorganize = f.DryRun, f.Sync, f.Rehash, f.Prune, f.Reorganize
}

// validate catches what the settings can't: a chosen option whose field was left
// empty would otherwise quietly do nothing.
func (f form) validate() error {
	var errs []error
	if f.DateMode != "" && strings.TrimSpace(f.Date) == "" {
		errs = append(errs, errors.New("enter a date (YYYY, YYYY-MM or YYYY-MM-DD), or choose to use each file's own date"))
	}
	if f.NameMode != "" && strings.TrimSpace(f.Name) == "" {
		errs = append(errs, fmt.Errorf("enter the %s name, or choose dated folders only", f.NameMode))
	}
	return errors.Join(errs...)
}

// expandHome replaces a leading ~ with the home folder, as a shell would on the
// command line.
func expandHome(p string) string {
	if p != "~" && !strings.HasPrefix(p, "~/") && !strings.HasPrefix(p, `~\`) {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return p
	}
	return filepath.Join(home, p[1:])
}

// baseConfig returns the settings a run into library starts from, and the config
// file they came from. For the library the program started with, that's the
// initial settings, so flags given with -ui still apply. For any other library it's
// the defaults plus that library's photosorter.toml.
func (s *server) baseConfig(library string) (config.Config, string, error) {
	if filepath.Clean(library) == filepath.Clean(s.opts.Initial.Library) {
		return s.opts.Initial, s.opts.ConfigFile, nil
	}
	cfg := config.Default()
	path := config.FindFile("", library)
	if path != "" {
		if err := config.LoadFile(path, &cfg); err != nil {
			return cfg, path, err
		}
	}
	cfg.Library = library
	return cfg, path, nil
}

func (s *server) handleSettings(w http.ResponseWriter, r *http.Request) {
	library := s.opts.Initial.Library
	if r.URL.Query().Has("library") {
		library = expandHome(strings.TrimSpace(r.URL.Query().Get("library")))
	}
	cfg, path, err := s.baseConfig(library)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, settings{Form: formFrom(cfg), ConfigFile: path, Running: s.console.isRunning()})
}

func (s *server) handlePick(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Kind string `json:"kind"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || (req.Kind != pickFolder && req.Kind != pickZips) {
		writeError(w, http.StatusBadRequest, errors.New("kind must be folder or zips"))
		return
	}
	if !s.pickMu.TryLock() {
		writeError(w, http.StatusConflict, errors.New("a file picker is already open"))
		return
	}
	defer s.pickMu.Unlock()
	paths, err := pick(req.Kind)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, map[string][]string{"paths": paths})
}

// handleOpen shows a folder in the file manager. Only folders are opened, so the
// call can't be used to run a program.
func (s *server) handleOpen(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	path := expandHome(req.Path)
	if info, err := os.Stat(path); err != nil || !info.IsDir() {
		writeError(w, http.StatusBadRequest, fmt.Errorf("%q is not a folder", req.Path))
		return
	}
	if err := openPath(path); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, struct{}{})
}

func (s *server) handleRun(w http.ResponseWriter, r *http.Request) {
	var f form
	if err := json.NewDecoder(r.Body).Decode(&f); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	f.Library = expandHome(strings.TrimSpace(f.Library))
	cfg, _, err := s.baseConfig(f.Library)
	if err == nil {
		err = f.validate()
	}
	if err == nil {
		f.apply(&cfg)
		err = cfg.Validate()
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := s.start(cfg); err != nil {
		writeError(w, http.StatusConflict, err)
		return
	}
	writeJSON(w, struct{}{})
}

// start begins a run in the background, unless one is already going.
func (s *server) start(cfg config.Config) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		return errors.New("a run is already in progress")
	}
	select {
	case <-s.quit:
		return errors.New("PhotoSorter is closing")
	default:
	}
	ctx, cancel := context.WithCancel(s.ctx)
	s.cancel = cancel
	s.console.begin()
	s.runs.Add(1)
	go func() {
		defer s.runs.Done()
		out := io.MultiWriter(s.console, s.opts.Stdout)
		code := s.opts.Execute(ctx, cfg, out, out)
		cancel()
		s.mu.Lock()
		s.console.end(code)
		s.cancel = nil
		s.mu.Unlock()
	}()
	return nil
}

// stop cancels the current run, if any.
func (s *server) stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		s.cancel()
	}
}

func (s *server) handleCancel(w http.ResponseWriter, r *http.Request) {
	s.stop()
	writeJSON(w, struct{}{})
}

func (s *server) handleQuit(w http.ResponseWriter, r *http.Request) {
	s.quitOnce.Do(func() { close(s.quit) })
	writeJSON(w, struct{}{})
}

// shutdown stops any run and waits for it to finish, so the library database is
// closed cleanly.
func (s *server) shutdown() {
	s.quitOnce.Do(func() { close(s.quit) })
	s.stop()
	s.runs.Wait()
}

// handleEvents streams the console to the page as server-sent events. It starts
// with the backlog, so a page that connects mid-run, or reconnects, catches up.
func (s *server) handleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, errors.New("streaming unsupported"))
		return
	}
	ch, snapshot := s.console.subscribe()
	defer s.console.unsubscribe(ch)

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	send := func(ev event) {
		b, _ := json.Marshal(ev)
		fmt.Fprintf(w, "data: %s\n\n", b)
	}
	for _, ev := range snapshot {
		send(ev)
	}
	flusher.Flush()
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return
			}
			send(ev)
			flusher.Flush()
		case <-r.Context().Done():
			return
		case <-s.quit:
			return
		}
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}
