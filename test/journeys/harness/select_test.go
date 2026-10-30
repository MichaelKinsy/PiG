package harness

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The base comes from the JOURNEY_CHANGED_BASE environment variable. A value that begins with a dash is a git option
// (`git diff --output=<file>` writes the file), so ChangedFiles must refuse it before git runs.
func TestChangedFilesRefusesOptionLikeBase(t *testing.T) {
	dir := t.TempDir()
	written := filepath.Join(dir, "written")
	_, err := ChangedFiles(dir, "--output="+written)
	if err == nil || !strings.Contains(err.Error(), "base") {
		t.Fatalf("ChangedFiles() error = %v, want a base error", err)
	}
	if _, statErr := os.Stat(written + "...HEAD"); statErr == nil {
		t.Fatal("git diff wrote a file named by the base")
	}
}
