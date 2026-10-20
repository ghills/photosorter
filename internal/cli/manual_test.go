package cli

import (
	"flag"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestRun_ManualImportWithDateAndEvent(t *testing.T) {
	dir := t.TempDir()
	lib := filepath.Join(dir, "Library")
	in := filepath.Join(dir, "fireworks")
	writeFile(t, filepath.Join(in, "IMG_20230514.jpg"), "camera clock was wrong")
	writeFile(t, filepath.Join(in, "scan.jpg"), "no date")

	code, out := runCLI(t, "-out", lib, "-date", "2019-07-04", "-event", "Fireworks", in)
	if code != ExitOK {
		t.Fatalf("exit %d\n%s", code, out)
	}
	if !strings.Contains(out, `Manual import: every file dated 2019-07-04, event "Fireworks".`) {
		t.Errorf("no manual-import summary:\n%s", out)
	}
	want := []string{"2019/2019-07-04 Fireworks/IMG_20230514.jpg", "2019/2019-07-04 Fireworks/scan.jpg"}
	if got := mediaIn(t, lib); !reflect.DeepEqual(got, want) {
		t.Errorf("library = %v, want %v", got, want)
	}
}

func TestRun_FallbackDateAndHandAddedFiles(t *testing.T) {
	dir := t.TempDir()
	lib := filepath.Join(dir, "Library")
	in := filepath.Join(dir, "scans")
	writeFile(t, filepath.Join(in, "IMG_20010304.jpg"), "dated by name")
	writeFile(t, filepath.Join(in, "scan.jpg"), "no date")
	if code, out := runCLI(t, "-out", lib, "-fallback-date", "1998", in); code != ExitOK {
		t.Fatalf("exit %d\n%s", code, out)
	}
	want := []string{"1998/scan.jpg", "2001/2001-03-04/IMG_20010304.jpg"}
	if got := mediaIn(t, lib); !reflect.DeepEqual(got, want) {
		t.Errorf("library = %v, want %v", got, want)
	}

	// A copy of a new photo put in the library by hand is synced before the next
	// import, so importing the same photo is recognised as a duplicate.
	writeFile(t, filepath.Join(lib, "by hand", "copy.jpg"), "new photo")
	writeFile(t, filepath.Join(in, "new.jpg"), "new photo")
	code, out := runCLI(t, "-out", lib, in)
	if code != ExitOK || !strings.Contains(out, "(added by hand?)") {
		t.Fatalf("exit %d, no automatic sync:\n%s", code, out)
	}
	for _, f := range mediaIn(t, lib) {
		if strings.HasSuffix(f, "new.jpg") {
			t.Errorf("hand-added photo imported again as %s", f)
		}
	}
}

func TestUsage_GroupsEveryVisibleFlag(t *testing.T) {
	var cfgPath string
	cfg, _, _ := parse(t, "-out", t.TempDir(), "-sync")
	fs := newFlagSet(&cfg, &cfgPath, nil)

	seen := map[string]int{}
	for _, g := range flagGroups {
		for _, name := range g.flags {
			if fs.Lookup(name) == nil {
				t.Errorf("help group %q lists unknown flag -%s", g.title, name)
			}
			seen[name]++
		}
	}
	fs.VisitAll(func(f *flag.Flag) {
		_, deprecated := deprecatedFlags[f.Name]
		switch {
		case deprecated && seen[f.Name] > 0:
			t.Errorf("deprecated flag -%s is shown in the help", f.Name)
		case !deprecated && seen[f.Name] != 1:
			t.Errorf("flag -%s appears in %d help groups, want exactly 1", f.Name, seen[f.Name])
		}
	})
}
