package cli

import (
	"path/filepath"
	"strings"
	"testing"
)

// Files the importer wrote, and folders reorganize moved, are known to the database,
// so a later sync doesn't hash them again.
func TestRun_SyncSkipsImportedAndReorganizedFiles(t *testing.T) {
	dir := t.TempDir()
	lib := filepath.Join(dir, "Library")
	in := filepath.Join(dir, "in")
	writeFile(t, filepath.Join(in, "IMG_20230514.jpg"), "one")
	writeFile(t, filepath.Join(in, "IMG_20230515.jpg"), "two")

	// The flat layout leaves folders for -reorganize to move.
	if code, out := runCLI(t, "-out", lib, "-date-format", "2006-01-02", in); code != ExitOK {
		t.Fatalf("import: exit %d\n%s", code, out)
	}
	if code, out := runCLI(t, "-out", lib, "-sync"); code != ExitOK || !strings.Contains(out, "0 files hashed, 2 unchanged") {
		t.Errorf("sync after import: exit %d\n%s", code, out)
	}
	if code, out := runCLI(t, "-out", lib, "-reorganize", "-sync"); code != ExitOK || !strings.Contains(out, "0 files hashed, 2 unchanged") {
		t.Errorf("sync after reorganize: exit %d\n%s", code, out)
	}
	if code, out := runCLI(t, "-out", lib, "-rehash"); code != ExitOK || !strings.Contains(out, "2 files hashed, 0 unchanged") {
		t.Errorf("rehash: exit %d\n%s", code, out)
	}
}
