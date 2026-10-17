package main

// pi: packages/ai/scripts/check-model-data.ts

import (
	"bytes"
	"strings"
	"testing"
)

// check-model-data.ts: a package root without generated data fails with the validation message followed by the
// hydration hint on stderr and a non-zero exit code, and prints nothing on stdout.
func TestRunReportsMissingModelDataWithTheHydrationHint(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"-root", t.TempDir()}, &stdout, &stderr); code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q", stdout.String())
	}
	if !strings.HasSuffix(stderr.String(), "\n\nModel data is missing or stale. Run `npm run hydrate:model-data` from the repository root.\n") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

// A hydration failure exits before validation and does not print the hint (hydrate-model-catalog.ts throws).
func TestRunStopsAtHydrationFailure(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"-root", t.TempDir(), "-hydrate", "/nonexistent/models.all.json"}, &stdout, &stderr); code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if stdout.Len() != 0 || strings.Contains(stderr.String(), "hydrate:model-data") {
		t.Fatalf("stdout = %q, stderr = %q", stdout.String(), stderr.String())
	}
}
