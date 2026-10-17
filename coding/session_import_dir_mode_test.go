//go:build !windows

package coding

import (
	"os"
	"path/filepath"
	"testing"
)

// pig divergence (D132): /import creates a missing session directory owner-only, like every session directory.
func TestImportFromJsonlCreatesAnOwnerOnlySessionDirectory(t *testing.T) {
	sessionDir := filepath.Join(t.TempDir(), "agent", "sessions", "--proj--")
	sess, err := NewSession(newTestServices(t), SessionOptions{Model: fakeModel(), SessionDir: sessionDir})
	if err != nil {
		t.Fatal(err)
	}
	drainSessionEvents(t, sess)
	if err := os.RemoveAll(filepath.Join(sessionDir, "..", "..")); err != nil {
		t.Fatal(err)
	}
	importPath := filepath.Join(t.TempDir(), "import", "imported.jsonl")
	writeImportTestFile(t, importPath, importTestHeader("imported", t.TempDir()))
	if result, err := sess.ImportFromJsonl(t.Context(), importPath, ""); err != nil || result.Cancelled {
		t.Fatalf("import = %+v, %v", result, err)
	}
	for _, dir := range []string{filepath.Join(sessionDir, "..", ".."), filepath.Join(sessionDir, ".."), sessionDir} {
		info, err := os.Stat(dir)
		if err != nil {
			t.Fatal(err)
		}
		if mode := info.Mode().Perm(); mode != 0o700 {
			t.Errorf("%s mode = %o, want 700", dir, mode)
		}
	}
}
