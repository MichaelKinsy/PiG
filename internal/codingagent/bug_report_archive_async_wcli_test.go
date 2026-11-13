package codingagent

// pi: packages/coding-agent/src/core/bug-report.ts

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// Pi bug-report.ts:274 writeBugReportArchive resolves after the single archive write and rejects when it fails: a path that already holds a file fails and leaves
// that file as it was, and a path in a missing directory fails and creates nothing.
func TestWriteBugReportArchiveRejectsExistingAndUnwritablePathsWithoutResidue(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "report.zip")
	if err := WriteBugReportArchive(BugReportBundle{}, path); err != nil {
		t.Fatal(err)
	}
	first, err := os.ReadFile(path)
	if err != nil || len(first) == 0 {
		t.Fatalf("archive = %d bytes, %v", len(first), err)
	}
	if err := WriteBugReportArchive(BugReportBundle{}, path); err == nil {
		t.Fatal("a second write to the same path succeeded")
	}
	if after, _ := os.ReadFile(path); !bytes.Equal(after, first) {
		t.Fatal("the failed write changed the existing archive")
	}
	missing := filepath.Join(dir, "absent", "report.zip")
	if err := WriteBugReportArchive(BugReportBundle{}, missing); err == nil {
		t.Fatal("a write into a missing directory succeeded")
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Fatalf("directory holds %d entries, want only the first archive", len(entries))
	}
}
