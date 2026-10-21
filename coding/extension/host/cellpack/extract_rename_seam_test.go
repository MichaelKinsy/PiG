package cellpack

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// The replacing rename goes through fsretry, and a rename that the platform refuses while a peer has just published the same bytes is adopted; a refusal with no such peer is an error.
func TestExtractRenameRefusal(t *testing.T) {
	denied := &os.LinkError{Op: "rename", Err: syscall.EACCES}
	restore := renameFile
	t.Cleanup(func() { renameFile = restore })

	fsys, m := goCellFixture(t, "NEW BINARY")
	t.Run("no peer", func(t *testing.T) {
		dest := t.TempDir()
		out := extractedBinaryPath(dest, "go/aaaa/runner")
		if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(out, []byte("OLD BINARY"), 0o755); err != nil {
			t.Fatal(err)
		}
		renameFile = func(string, string) error { return denied }
		if err := extract(fsys, m, dest); !errors.Is(err, syscall.EACCES) {
			t.Fatalf("extract = %v, want the refusal", err)
		}
		if entries, _ := filepath.Glob(filepath.Join(filepath.Dir(out), ".extract-*")); len(entries) != 0 {
			t.Fatalf("temp files left behind: %v", entries)
		}
	})
	t.Run("peer published the same bytes", func(t *testing.T) {
		dest := t.TempDir()
		out := extractedBinaryPath(dest, "go/aaaa/runner")
		if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(out, []byte("OLD BINARY"), 0o755); err != nil {
			t.Fatal(err)
		}
		renameFile = func(string, string) error {
			if err := os.WriteFile(out, []byte("NEW BINARY"), 0o755); err != nil {
				t.Fatal(err)
			}
			return denied
		}
		if err := extract(fsys, m, dest); err != nil {
			t.Fatalf("extract = %v, want the peer's file adopted", err)
		}
	})
}
