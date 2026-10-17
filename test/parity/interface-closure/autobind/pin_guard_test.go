package main

import (
	"strings"
	"testing"
)

// An override run (PI_UPSTREAM_REF) exports PIG_UPSTREAM_VERSION. A build that did not pick up the overlay would derive and write the
// committed pin's ledger, so the run must stop before it reads or writes any ledger.
func TestRunRefusesALedgerVersionThatIsNotTheExportedPin(t *testing.T) {
	t.Setenv("PIG_UPSTREAM_VERSION", "9.9.9+deadbeef")
	err := run(options{root: t.TempDir(), version: "1.1.0"})
	if err == nil || !strings.Contains(err.Error(), "9.9.9+deadbeef") || !strings.Contains(err.Error(), "1.1.0") {
		t.Fatalf("run err = %v, want a refusal naming both versions", err)
	}
}

func TestRunAcceptsTheExportedPinVersion(t *testing.T) {
	t.Setenv("PIG_UPSTREAM_VERSION", "1.1.0")
	root := t.TempDir()
	t.Chdir(root) // run moves the working directory to its root; t.Chdir restores the package directory for the tests after this one
	err := run(options{root: root, version: "1.1.0"})
	if err != nil && strings.Contains(err.Error(), "exported pin") {
		t.Fatalf("run refused the matching version: %v", err)
	}
}
