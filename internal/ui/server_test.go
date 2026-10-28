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
	"path/filepath"
	"reflect"
	"strings"
	"sync"
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

// testServer starts the interface's handler with fake runs.
func testServer(t *testing.T, initial config.Config) (*server, *httptest.Server, *fakeRun) {
	t.Helper()
	fake := &fakeRun{release: make(chan struct{})}
	s, err := newServer(context.Background(), Options{Initial: initial, Execute: fake.execute, Stdout: io.Discard, Stderr: io.Discard})
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

func TestServer_RunValidatesTheForm(t *testing.T) {
	s, ts, _ := testServer(t, config.Default())
	lib := t.TempDir()
	tests := map[string]struct {
		form form
		want string
	}{
		"no library":         {form{Inputs: []string{"x.zip"}}, "library folder is required"},
		"nothing to do":      {form{Library: lib}, "nothing to do"},
		"bad date":           {form{Library: lib, Inputs: []string{"x.zip"}, DateMode: "date", Date: "July"}, "-date"},
		"date left empty":    {form{Library: lib, Inputs: []string{"x.zip"}, DateMode: "fallback", Date: " "}, "enter a date"},
		"name left empty":    {form{Library: lib, Inputs: []string{"x.zip"}, NameMode: "album"}, "enter the album name"},
		"bad album name":     {form{Library: lib, Inputs: []string{"x.zip"}, NameMode: "album", Name: "a/b"}, "-album"},
		"unknown album mode": {form{Library: lib, Sync: true, Albums: "maybe"}, "albums must be"},
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

func TestServer_RunMapsFormToSettings(t *testing.T) {
	initial := config.Default()
	initial.Library = t.TempDir()
	initial.Workers = 3 // as if given with -ui; must survive into the run
	s, ts, fake := testServer(t, initial)
	folder := t.TempDir()

	f := form{
		Library:  initial.Library,
		Inputs:   []string{folder, "takeout-*.zip", " "},
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
	want.Zips = []string{"takeout-*.zip"}
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
