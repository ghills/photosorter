package ui_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"photosorter/internal/cli"
	"photosorter/internal/config"
	"photosorter/internal/media/mediatest"
	"photosorter/internal/ui"
)

// TestImportThroughTheInterface runs a real import the way the page does: post the
// form, then follow the event stream until the run is done.
func TestImportThroughTheInterface(t *testing.T) {
	lib := filepath.Join(t.TempDir(), "Library")
	src := t.TempDir()
	os.WriteFile(filepath.Join(src, "a.jpg"), mediatest.JPEG(time.Date(2021, 5, 14, 10, 0, 0, 0, time.UTC)), 0644)
	os.WriteFile(filepath.Join(src, "scan.jpg"), []byte("no exif here"), 0644)

	h, token, shutdown := ui.NewTestHandler(context.Background(), ui.Options{
		Initial: config.Default(),
		Execute: cli.Execute,
		Stdout:  io.Discard,
		Stderr:  io.Discard,
	})
	ts := httptest.NewServer(h)
	defer ts.Close()
	defer shutdown()

	body, _ := json.Marshal(map[string]any{
		"library":  lib,
		"inputs":   []string{src},
		"dateMode": "fallback",
		"date":     "1998",
		"nameMode": "event",
		"name":     "Scans",
	})
	req, _ := http.NewRequest("POST", ts.URL+"/api/run", bytes.NewReader(body))
	req.Header.Set("X-PhotoSorter-Token", token)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	reply, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("run: status %d: %s", res.StatusCode, reply)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	req, _ = http.NewRequestWithContext(ctx, "GET", ts.URL+"/api/events?t="+token, nil)
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var output []string
	code := -1
	for sc := bufio.NewScanner(res.Body); sc.Scan() && code < 0; {
		data, ok := strings.CutPrefix(sc.Text(), "data: ")
		if !ok {
			continue
		}
		var ev struct {
			Type, Text string
			Code       int
		}
		json.Unmarshal([]byte(data), &ev)
		switch ev.Type {
		case "line":
			output = append(output, ev.Text)
		case "done":
			code = ev.Code
		}
	}
	if code != cli.ExitOK || !strings.Contains(strings.Join(output, "\n"), "Done!") {
		t.Fatalf("exit code %d, output:\n%s", code, strings.Join(output, "\n"))
	}

	for _, want := range []string{"2021/2021-05-14 Scans/a.jpg", "1998/1998 Scans/scan.jpg"} {
		if _, err := os.Stat(filepath.Join(lib, filepath.FromSlash(want))); err != nil {
			t.Errorf("missing %s: %v", want, err)
		}
	}
	if t.Failed() {
		filepath.WalkDir(lib, func(p string, d os.DirEntry, err error) error {
			t.Log(p)
			return nil
		})
	}
}
