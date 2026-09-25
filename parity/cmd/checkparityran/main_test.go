package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestCountOutcomesMissingFileIsZeroNotError is the case that matters most:
// TestParity skipping wholesale (PIG_PARITY_PIG_BIN unset, tmux/pi missing)
// never writes the results file at all. That must read as "0 scenarios ran",
// not as an error the Makefile could mistake for something benign.
func TestCountOutcomesMissingFileIsZeroNotError(t *testing.T) {
	n, err := countOutcomes(filepath.Join(t.TempDir(), "does-not-exist.json"))
	if err != nil {
		t.Fatalf("countOutcomes on a missing file returned an error: %v", err)
	}
	if n != 0 {
		t.Fatalf("countOutcomes on a missing file = %d, want 0", n)
	}
}

func TestCountOutcomesCountsEachOutcome(t *testing.T) {
	path := filepath.Join(t.TempDir(), "results.json")
	const body = `{
		"upstream_version": "0.87.1",
		"outcomes": [
			{"name": "01-a", "passed": true},
			{"name": "02-b", "passed": false, "failures": ["mismatch"]}
		]
	}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	n, err := countOutcomes(path)
	if err != nil {
		t.Fatalf("countOutcomes: %v", err)
	}
	if n != 2 {
		t.Fatalf("countOutcomes = %d, want 2", n)
	}
}

func TestCountOutcomesEmptyOutcomesIsZero(t *testing.T) {
	path := filepath.Join(t.TempDir(), "results.json")
	if err := os.WriteFile(path, []byte(`{"upstream_version": "0.87.1", "outcomes": []}`), 0o644); err != nil {
		t.Fatal(err)
	}
	n, err := countOutcomes(path)
	if err != nil {
		t.Fatalf("countOutcomes: %v", err)
	}
	if n != 0 {
		t.Fatalf("countOutcomes = %d, want 0", n)
	}
}

func TestCountOutcomesInvalidJSONErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "results.json")
	if err := os.WriteFile(path, []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := countOutcomes(path); err == nil {
		t.Fatal("countOutcomes on malformed JSON: want error, got nil")
	}
}
