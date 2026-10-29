package ui

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"photosorter/internal/config"
)

// fakeRun is an Execute that prints a line, then waits until it's released or
// cancelled. It records the settings it was given.
type fakeRun struct {
	mu      sync.Mutex
	cfgs    []config.Config
	release chan struct{}
}

func (f *fakeRun) execute(ctx context.Context, cfg config.Config, stdout, stderr io.Writer) int {
	f.mu.Lock()
	f.cfgs = append(f.cfgs, cfg)
	f.mu.Unlock()
	fmt.Fprintf(stdout, "importing into %s\n", cfg.Library)
	fmt.Fprint(stderr, "warning: something odd\n")
	select {
	case <-f.release:
		return 0
	case <-ctx.Done():
		return 130
	}
}

func (f *fakeRun) last() config.Config {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.cfgs[len(f.cfgs)-1]
}

// testServer starts the interface's handler with fake runs. reload, if given, is
// its [Options.Reload].
func testServer(t *testing.T, initial config.Config, reload ...func() (config.Config, string, error)) (*server, *httptest.Server, *fakeRun) {
	t.Helper()
	fake := &fakeRun{release: make(chan struct{})}
	opts := Options{Initial: initial, Execute: fake.execute, Stdout: io.Discard, Stderr: io.Discard}
	if len(reload) > 0 {
		opts.Reload = reload[0]
	}
	s, err := newServer(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s.handler())
	t.Cleanup(func() {
		s.shutdown()
		ts.Close()
	})
	return s, ts, fake
}

// call makes an API request with the server's token and decodes the reply into out.
func call(t *testing.T, s *server, ts *httptest.Server, method, path string, body, out any) int {
	t.Helper()
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, ts.URL+path, r)
	req.Header.Set(tokenHeader, s.token)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if out != nil {
		json.NewDecoder(res.Body).Decode(out)
	}
	return res.StatusCode
}

// stream reads events from /api/events until stop returns true for one.
func stream(t *testing.T, s *server, ts *httptest.Server, stop func(event) bool) []event {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", ts.URL+"/api/events?t="+s.token, nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var events []event
	sc := bufio.NewScanner(res.Body)
	for sc.Scan() {
		data, ok := strings.CutPrefix(sc.Text(), "data: ")
		if !ok {
			continue
		}
		var ev event
		if err := json.Unmarshal([]byte(data), &ev); err != nil {
			t.Fatal(err)
		}
		events = append(events, ev)
		if stop(ev) {
			return events
		}
	}
	t.Fatalf("event stream ended early: %v (events %+v)", sc.Err(), events)
	return nil
}

func isDone(ev event) bool { return ev.Type == "done" }

func TestServer_RejectsRequestsWithoutToken(t *testing.T) {
	s, ts, _ := testServer(t, config.Default())
	for _, tc := range []struct{ name, url, token, host string }{
		{"no token", "/api/settings", "", ""},
		{"wrong token", "/api/settings", "nope", ""},
		{"rebound host name", "/api/settings", s.token, "evil.example:80"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req, _ := http.NewRequest("GET", ts.URL+tc.url, nil)
			req.Header.Set(tokenHeader, tc.token)
			if tc.host != "" {
				req.Host = tc.host
			}
			res, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			res.Body.Close()
			if res.StatusCode != http.StatusForbidden {
				t.Errorf("status = %d, want 403", res.StatusCode)
			}
		})
	}

	// The page itself has no secrets and needs no token.
	res, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusOK || !bytes.Contains(page, []byte("<title>PhotoSorter</title>")) {
		t.Errorf("index: status %d", res.StatusCode)
	}
}

func TestServer_Settings(t *testing.T) {
	initial := config.Default()
	initial.Library = t.TempDir()
	initial.DryRun = true
	initial.Folders = []string{"/photos"}
	initial.Manual = config.Manual{FallbackDate: "1998", Event: "Scans"}
	s, ts, _ := testServer(t, initial)

	var got settings
	if code := call(t, s, ts, "GET", "/api/settings", nil, &got); code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	want := form{Library: initial.Library, Inputs: []string{"/photos"}, DateMode: "fallback", Date: "1998", NameMode: "event", Name: "Scans", DryRun: true, Albums: initial.Layout.Albums}
	if !reflect.DeepEqual(got.Form, want) {
		t.Errorf("initial form = %+v, want %+v", got.Form, want)
	}

	// Another library's settings come from its own config file.
	other := t.TempDir()
	path := filepath.Join(other, config.FileName)
	os.WriteFile(path, []byte("sync = true\n[layout]\nalbums = \"ignore\"\n"), 0644)
	got = settings{}
	call(t, s, ts, "GET", "/api/settings?library="+other, nil, &got)
	if got.ConfigFile != path || !got.Form.Sync || got.Form.Albums != "ignore" || got.Form.DryRun || got.Form.Library != other {
		t.Errorf("other library settings = %+v", got)
	}

	// A broken config file is reported.
	os.WriteFile(path, []byte("bogus = 1\n"), 0644)
	var errReply map[string]string
	if code := call(t, s, ts, "GET", "/api/settings?library="+other, nil, &errReply); code != http.StatusBadRequest || !strings.Contains(errReply["error"], "bogus") {
		t.Errorf("status %d, reply %v", code, errReply)
	}
}

func TestServer_InitialLibraryIsReloadedWithFullPaths(t *testing.T) {
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	initial := config.Default()
	initial.Library, initial.Folders = "Library", []string{"in"} // as given on the command line
	var reloads atomic.Int32
	s, ts, fake := testServer(t, initial, func() (config.Config, string, error) {
		reloads.Add(1)
		cfg := initial
		cfg.Workers = 7 // as if the config file was edited since startup
		return cfg, "", nil
	})

	var got settings
	call(t, s, ts, "GET", "/api/settings", nil, &got)
	lib := filepath.Join(dir, "Library")
	if got.Form.Library != lib || !reflect.DeepEqual(got.Form.Inputs, []string{filepath.Join(dir, "in")}) {
		t.Errorf("form library %q, inputs %q; want full paths under %s", got.Form.Library, got.Form.Inputs, dir)
	}

	if code := call(t, s, ts, "POST", "/api/run", form{Library: lib, Sync: true}, nil); code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	close(fake.release)
	stream(t, s, ts, isDone)
	if cfg := fake.last(); cfg.Workers != 7 || reloads.Load() != 2 {
		t.Errorf("run workers = %d after %d reloads; want 7 after 2", cfg.Workers, reloads.Load())
	}
}

func TestServer_RunValidatesTheForm(t *testing.T) {
	s, ts, _ := testServer(t, config.Default())
	lib := t.TempDir()
	zip := filepath.Join(lib, "x.zip")
	tests := map[string]struct {
		form form
		want string
	}{
		"no library":         {form{Inputs: []string{zip}}, "library folder is required"},
		"nothing to do":      {form{Library: lib}, "nothing to do"},
		"bad date":           {form{Library: lib, Inputs: []string{zip}, DateMode: "date", Date: "July"}, "-date"},
		"date left empty":    {form{Library: lib, Inputs: []string{zip}, DateMode: "fallback", Date: " "}, "enter a date"},
		"name left empty":    {form{Library: lib, Inputs: []string{zip}, NameMode: "album"}, "enter the album name"},
		"bad album name":     {form{Library: lib, Inputs: []string{zip}, NameMode: "album", Name: "a/b"}, "-album"},
		"unknown album mode": {form{Library: lib, Sync: true, Albums: "maybe"}, "albums must be"},
		"relative library":   {form{Library: "Pictures/Library", Sync: true}, `"Pictures/Library" is not a full path`},
		"relative input":     {form{Library: lib, Inputs: []string{"takeout.zip"}}, `"takeout.zip" is not a full path`},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			var reply map[string]string
			code := call(t, s, ts, "POST", "/api/run", tt.form, &reply)
			if code != http.StatusBadRequest || !strings.Contains(reply["error"], tt.want) {
				t.Errorf("status %d, error %q; want 400 containing %q", code, reply["error"], tt.want)
			}
		})
	}
}

func TestServer_RunRejectsUnknownFields(t *testing.T) {
	s, ts, _ := testServer(t, config.Default())
	body := map[string]any{"library": t.TempDir(), "sync": true, "dry_run": true}
	var reply map[string]string
	if code := call(t, s, ts, "POST", "/api/run", body, &reply); code != http.StatusBadRequest || !strings.Contains(reply["error"], "dry_run") {
		t.Errorf("status %d, error %q; want 400 naming dry_run", code, reply["error"])
	}
}

// TestPageSendsTheFormFields keeps the page's readForm and the form struct in step.
// The server rejects fields it doesn't know, so a mismatch would otherwise only show
// up when someone pressed Start.
func TestPageSendsTheFormFields(t *testing.T) {
	page, err := static.ReadFile("static/index.html")
	if err != nil {
		t.Fatal(err)
	}
	_, body, ok := strings.Cut(string(page), "function readForm() {")
	body, _, ok2 := strings.Cut(body, "};")
	if !ok || !ok2 {
		t.Fatal("readForm not found in index.html")
	}
	var sent []string
	for _, m := range regexp.MustCompile(`(?m)^\s*(\w+)\s*[:,]`).FindAllStringSubmatch(body, -1) {
		sent = append(sent, m[1])
	}
	var fields []string
	typ := reflect.TypeFor[form]()
	for i := range typ.NumField() {
		fields = append(fields, typ.Field(i).Tag.Get("json"))
	}
	slices.Sort(sent)
	slices.Sort(fields)
	if !slices.Equal(sent, fields) {
		t.Errorf("readForm sends %q, but form has %q", sent, fields)
	}
}

func TestServer_RunMapsFormToSettings(t *testing.T) {
	initial := config.Default()
	initial.Library = t.TempDir()
	initial.Workers = 3 // as if given with -ui; must survive into the run
	s, ts, fake := testServer(t, initial)
	folder := t.TempDir()

	f := form{
		Library:  initial.Library,
		Inputs:   []string{folder, filepath.Join(folder, "takeout-*.zip"), " "},
		DateMode: "date", Date: " 2019-07-04 ",
		NameMode: "event", Name: "Fireworks",
		DryRun: true, Albums: config.AlbumsIgnore, Reorganize: true,
	}
	if code := call(t, s, ts, "POST", "/api/run", f, nil); code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	close(fake.release)
	stream(t, s, ts, isDone)

	got := fake.last()
	want := initial
	want.Folders = []string{folder}
	want.Zips = []string{filepath.Join(folder, "takeout-*.zip")}
	want.Manual = config.Manual{Date: "2019-07-04", Event: "Fireworks"}
	want.DryRun, want.Reorganize = true, true
	want.Layout.Albums = config.AlbumsIgnore
	if !reflect.DeepEqual(got, want) {
		t.Errorf("run settings =\n%+v\nwant\n%+v", got, want)
	}
}

func TestServer_RunStreamsAndCancels(t *testing.T) {
	s, ts, _ := testServer(t, config.Default())
	f := form{Library: t.TempDir(), Sync: true}
	if code := call(t, s, ts, "POST", "/api/run", f, nil); code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}

	// The page catches up on output written before it connected.
	stream(t, s, ts, func(ev event) bool { return ev.Kind == "warning" })

	// Only one run at a time.
	var reply map[string]string
	if code := call(t, s, ts, "POST", "/api/run", f, &reply); code != http.StatusConflict {
		t.Errorf("second run: status %d, want 409", code)
	}

	call(t, s, ts, "POST", "/api/cancel", struct{}{}, nil)
	events := stream(t, s, ts, isDone)
	var lines []string
	for _, ev := range events {
		if ev.Type == "line" {
			lines = append(lines, ev.Text)
		}
	}
	if want := []string{"importing into " + f.Library, "warning: something odd"}; !reflect.DeepEqual(lines, want) {
		t.Errorf("lines = %q, want %q", lines, want)
	}
	if code := events[len(events)-1].Code; code != 130 {
		t.Errorf("exit code = %d, want 130", code)
	}

	// Once the run has ended another can start.
	if code := call(t, s, ts, "POST", "/api/run", f, nil); code != http.StatusOK {
		t.Errorf("run after cancel: status %d", code)
	}
}

func TestServer_QuitStopsTheRun(t *testing.T) {
	s, ts, _ := testServer(t, config.Default())
	call(t, s, ts, "POST", "/api/run", form{Library: t.TempDir(), Sync: true}, nil)
	call(t, s, ts, "POST", "/api/quit", struct{}{}, nil)

	done := make(chan struct{})
	go func() {
		s.shutdown()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("shutdown did not stop the run")
	}
	if code := call(t, s, ts, "POST", "/api/run", form{Library: t.TempDir(), Sync: true}, nil); code != http.StatusConflict {
		t.Errorf("run after quit: status %d, want 409", code)
	}
}

func TestServer_QuitClosesTheFilePicker(t *testing.T) {
	opened, closed := make(chan struct{}), make(chan error, 1)
	orig := showPicker
	showPicker = func(ctx context.Context, kind string) ([]string, error) {
		close(opened)
		<-ctx.Done() // a real picker's process is killed here
		closed <- ctx.Err()
		return nil, nil
	}
	t.Cleanup(func() { showPicker = orig })
	s, ts, _ := testServer(t, config.Default())

	go func() {
		b, _ := json.Marshal(map[string]string{"kind": pickFolder})
		req, _ := http.NewRequest("POST", ts.URL+"/api/pick", bytes.NewReader(b))
		req.Header.Set(tokenHeader, s.token)
		if res, err := http.DefaultClient.Do(req); err == nil {
			res.Body.Close()
		}
	}()
	<-opened
	call(t, s, ts, "POST", "/api/quit", struct{}{}, nil)
	select {
	case err := <-closed:
		if err == nil {
			t.Error("picker context ended without an error")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("quitting did not close the picker")
	}
}

func TestRunPicker_KilledPickerReturnsNoPaths(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs sleep(1)")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	paths, err := runPicker(exec.CommandContext(ctx, "sleep", "10"))
	if paths != nil || err != nil {
		t.Errorf("runPicker = %q, %v; want no paths and no error, as for a cancelled dialog", paths, err)
	}
}

func TestRunPicker_CancelCodes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs sh(1)")
	}
	cancelWithWarning := func() *exec.Cmd {
		return exec.Command("sh", "-c", "echo 'Gtk-Message: mapped without a transient parent' >&2; exit 1")
	}
	if paths, err := runPicker(cancelWithWarning(), 1); paths != nil || err != nil {
		t.Errorf("with cancel code 1: %q, %v; want a cancel", paths, err)
	}
	if _, err := runPicker(cancelWithWarning()); err == nil || !strings.Contains(err.Error(), "Gtk-Message") {
		t.Errorf("without cancel codes: err = %v; want the message as an error", err)
	}
}

func TestServer_InitialLibraryMatchedAsAFolder(t *testing.T) {
	initial := config.Default()
	initial.Library = t.TempDir()
	initial.DryRun = true // as if given with -ui
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(initial.Library, link); err != nil {
		t.Skip("cannot make a symlink:", err)
	}
	s, ts, _ := testServer(t, initial)
	var got settings
	call(t, s, ts, "GET", "/api/settings?library="+link, nil, &got)
	if !got.Form.DryRun {
		t.Errorf("the initial library through a symlink lost its -ui settings: %+v", got.Form)
	}
}

func TestServer_ConfigFileShownAsFullPath(t *testing.T) {
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	fake := &fakeRun{release: make(chan struct{})}
	s, err := newServer(context.Background(), Options{Initial: config.Default(), ConfigFile: "my.toml", Execute: fake.execute, Stdout: io.Discard, Stderr: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s.handler())
	t.Cleanup(func() { s.shutdown(); ts.Close() })
	var got settings
	call(t, s, ts, "GET", "/api/settings", nil, &got)
	if want := filepath.Join(dir, "my.toml"); got.ConfigFile != want {
		t.Errorf("config file shown as %q, want %q", got.ConfigFile, want)
	}
}

func TestServer_OpenOnlyOpensFolders(t *testing.T) {
	s, ts, _ := testServer(t, config.Default())
	file := filepath.Join(t.TempDir(), "x.sh")
	os.WriteFile(file, []byte("#!/bin/sh\n"), 0755)
	if code := call(t, s, ts, "POST", "/api/open", map[string]string{"path": file}, nil); code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", code)
	}
}

func TestServe_OpensBrowserAndStopsOnCancel(t *testing.T) {
	opened := make(chan string, 1)
	orig := openBrowser
	openBrowser = func(url string) error { opened <- url; return nil }
	t.Cleanup(func() { openBrowser = orig })

	ctx, cancel := context.WithCancel(context.Background())
	var out bytes.Buffer
	errc := make(chan error, 1)
	go func() {
		errc <- Serve(ctx, Options{Initial: config.Default(), Execute: (&fakeRun{}).execute, Stdout: &out, Stderr: io.Discard})
	}()

	url := <-opened
	if !strings.HasPrefix(url, "http://127.0.0.1:") || !strings.Contains(url, "/?t=") {
		t.Errorf("opened %q", url)
	}
	res, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Errorf("page status = %d", res.StatusCode)
	}

	cancel()
	select {
	case err := <-errc:
		if err != nil {
			t.Error(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Serve did not return after cancel")
	}
	if !strings.Contains(out.String(), url) {
		t.Errorf("address not printed: %q", out.String())
	}
}
