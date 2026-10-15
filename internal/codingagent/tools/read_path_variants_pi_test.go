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
	if got, err := resolveReadPath(nfc+".txt", cwd); err != nil || got != decomposed {
		t.Errorf("NFD variant: %q, %v, want %q", got, err, decomposed)
	}
	curly := create("Capture d\u2019cran.txt")
	if got, err := resolveReadPath("Capture d'cran.txt", cwd); err != nil || got != curly {
		t.Errorf("curly-quote variant: %q, %v, want %q", got, err, curly)
	}
	both := create("d\u2019" + nfd + ".txt")
	if got, err := resolveReadPath("d'"+nfc+".txt", cwd); err != nil || got != both {
		t.Errorf("NFD plus curly-quote variant: %q, %v, want %q", got, err, both)
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
