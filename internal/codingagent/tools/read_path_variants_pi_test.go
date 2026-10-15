package tools

import (
	"os"
	"path/filepath"
	"testing"
)

// Pi packages/coding-agent/src/core/tools/path-utils.ts resolveReadPath (:55-90): the resolved path when it exists; otherwise, in order, the macOS screenshot variant (a narrow no-break
// space before AM/PM), the NFD variant, the curly-quote variant and the NFD plus curly-quote variant, each only when it differs from the resolved path and exists; otherwise the resolved path
// itself, for the caller to report. A path that cannot be resolved is an error.
// mutation-checked: each variant skipped or taken when it equals the resolved path, and an existence test that is inverted, fail it.
func TestResolveReadPathTriesTheMacOSVariantsInPiOrder(t *testing.T) {
	cwd := t.TempDir()
	create := func(name string) string {
		t.Helper()
		path := filepath.Join(cwd, name)
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}
	const nnbsp = "\u202f"
	const nfc = "caf\u00e9"
	const nfd = "cafe\u0301"
	exact := create("exact.txt")
	if got, err := resolveReadPath("exact.txt", cwd); err != nil || got != exact {
		t.Errorf("an existing path: %q, %v, want %q", got, err, exact)
	}
	screenshot := create("Screenshot 2024-01-01 at 10.00.00" + nnbsp + "AM.png")
	if got, err := resolveReadPath("Screenshot 2024-01-01 at 10.00.00 AM.png", cwd); err != nil || got != screenshot {
		t.Errorf("AM/PM variant: %q, %v, want %q", got, err, screenshot)
	}
	pm := create("shot" + nnbsp + "pm.png")
	if got, err := resolveReadPath("shot pm.png", cwd); err != nil || got != pm {
		t.Errorf("lower-case pm variant: %q, %v, want %q", got, err, pm)
	}
	decomposed := create(nfd + ".txt")
	// APFS and HFS+ compare names by canonical equivalence, so the NFC spelling already names the NFD file: resolveReadPath's first existence test succeeds and returns the path as given, as Pi's does, and the
	// NFD and NFD plus curly-quote steps cannot be reached. A file system that keeps the two spellings apart (ext4, NTFS, tmpfs) must reach them, and Linux and Windows runs prove that.
	_, statErr := os.Stat(filepath.Join(cwd, nfc+".txt"))
	foldsNormalization := statErr == nil
	wantDecomposed := decomposed
	if foldsNormalization {
		wantDecomposed = filepath.Join(cwd, nfc+".txt")
	}
	if got, err := resolveReadPath(nfc+".txt", cwd); err != nil || got != wantDecomposed {
		t.Errorf("NFD variant (file system folds normalization: %v): %q, %v, want %q", foldsNormalization, got, err, wantDecomposed)
	}
	curly := create("Capture d\u2019cran.txt")
	if got, err := resolveReadPath("Capture d'cran.txt", cwd); err != nil || got != curly {
		t.Errorf("curly-quote variant: %q, %v, want %q", got, err, curly)
	}
	both := create("d\u2019" + nfd + ".txt")
	// With normalization folded, the straight-quote path misses and the curly-quote step finds the file under the NFC spelling the caller wrote.
	wantBoth := both
	if foldsNormalization {
		wantBoth = filepath.Join(cwd, "d\u2019"+nfc+".txt")
	}
	if got, err := resolveReadPath("d'"+nfc+".txt", cwd); err != nil || got != wantBoth {
		t.Errorf("NFD plus curly-quote variant (file system folds normalization: %v): %q, %v, want %q", foldsNormalization, got, err, wantBoth)
	}
	// Nothing exists: the resolved path comes back for the caller to report.
	missing := filepath.Join(cwd, "missing "+"AM.png")
	if got, err := resolveReadPath("missing AM.png", cwd); err != nil || got != missing {
		t.Errorf("a missing path: %q, %v, want %q", got, err, missing)
	}
	// A directory exists too.
	if err := os.Mkdir(filepath.Join(cwd, "dir"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got, err := resolveReadPath("dir", cwd); err != nil || got != filepath.Join(cwd, "dir") {
		t.Errorf("a directory: %q, %v", got, err)
	}
	// An unresolvable path is an error, and expandPath keeps a path it cannot normalize as written.
	if _, err := resolveReadPath("file://%zz", cwd); err == nil {
		t.Error("an invalid file URL resolved")
	}
	if got := expandPath("file://%zz"); got != "file://%zz" {
		t.Errorf("expandPath of an invalid file URL = %q, want it unchanged", got)
	}
}
