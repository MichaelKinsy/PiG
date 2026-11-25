// SPDX-License-Identifier: MIT

package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAuditRunCLIExitCodeAndBlindSpots(t *testing.T) {
	tmp := t.TempDir()
	files := filepath.Join(tmp, "files.txt")
	if err := os.WriteFile(files, []byte("README.md\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Docs-only change: empty failed lanes -> audit passes.
	if err := run([]string{"-root", "../../../../..", "-files-from", files, "-audit-run", "-failed-lanes", ""}); err != nil {
		t.Fatalf("audit with zero failed lanes failed: %v", err)
	}

	// Docs-only change where a full run had a failing lane -> blind spot error.
	if err := run([]string{"-root", "../../../../..", "-files-from", files, "-audit-run", "-failed-lanes", "test-fast"}); err == nil {
		t.Fatalf("expected audit blind spot error when test-fast failed on docs-only plan")
	}

	// Force-full event (merge_group) covers every failing lane.
	if err := run([]string{"-root", "../../../../..", "-files-from", files, "-event", "merge_group", "-audit-run", "-failed-lanes", "test-fast,win-cli"}); err != nil {
		t.Fatalf("expected merge_group full plan to cover all failing lanes, got: %v", err)
	}
}
