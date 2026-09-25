// Cross-validates that every divergence number referenced in parity-gate
// allowlists actually exists as a `## DN` heading in DIVERGENCES.md.
//
// Catches: typos like `"D9"` instead of `"D5"` in pigOnlyRunnerMethods
// (or any future allowlist) silently passing the gate.
//
// Identified during the meta-F/B-pass (post-F/B-pass-2) review as Q1.

package parity

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"testing"
)

// divergenceHeaderRE matches `## D1 -` / `## D42 -` style headers.
var divergenceHeaderRE = regexp.MustCompile(`^## (D\d+) `)

// divergenceRefRE matches a bare divergence reference (`D42`). Values in
// pigOnlyRunnerMethods that are not divergence references (e.g. SDK-surface
// rationales for language-surface accessor additions) do not resolve to a
// DIVERGENCES.md section and are skipped by the ref-resolution gate.
var divergenceRefRE = regexp.MustCompile(`^D\d+$`)

// loadDivergenceNumbers parses DIVERGENCES.md and returns the set of
// `D\d+` identifiers that have a top-level section. Walks up from this
// file to locate the doc: the parity test package lives under
// tests/upstream-parity but DIVERGENCES.md lives at the repo root.
func loadDivergenceNumbers(t *testing.T) map[string]bool {
	t.Helper()

	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	// thisFile = .../tests/upstream-parity/divergence_refs_test.go
	// repoRoot = .../pig
	repoRoot := filepath.Join(filepath.Dir(thisFile), "..", "..")
	docPath := filepath.Join(repoRoot, "DIVERGENCES.md")

	f, err := os.Open(docPath)
	if err != nil {
		t.Fatalf("open DIVERGENCES.md: %v", err)
	}
	defer func() { _ = f.Close() }()

	out := map[string]bool{}
	scan := bufio.NewScanner(f)
	for scan.Scan() {
		if m := divergenceHeaderRE.FindStringSubmatch(scan.Text()); m != nil {
			out[m[1]] = true
		}
	}
	if err := scan.Err(); err != nil {
		t.Fatalf("scan DIVERGENCES.md: %v", err)
	}
	if len(out) == 0 {
		t.Fatal("loadDivergenceNumbers found zero `## DN` headers: parser broken or DIVERGENCES.md restructured")
	}
	return out
}

// loadAllLedgerNumbers returns D-numbers declared in EITHER DIVERGENCES.md or
// docs/additive-features.md. Divergence and additive IDs share one global
// namespace (check-divergence-consistency.sh), so a ref citing an ID that lives
// in the additive ledger is valid: used by ref-resolution gates whose map
// values may point at a relocated (now-additive) record.
func loadAllLedgerNumbers(t *testing.T) map[string]bool {
	t.Helper()
	out := loadDivergenceNumbers(t)
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	docPath := filepath.Join(filepath.Dir(thisFile), "..", "..", "docs", "additive-features.md")
	f, err := os.Open(docPath)
	if err != nil {
		t.Fatalf("open ADDITIVE_FEATURES.md: %v", err)
	}
	defer func() { _ = f.Close() }()
	scan := bufio.NewScanner(f)
	for scan.Scan() {
		if m := divergenceHeaderRE.FindStringSubmatch(scan.Text()); m != nil {
			out[m[1]] = true
		}
	}
	if err := scan.Err(); err != nil {
		t.Fatalf("scan ADDITIVE_FEATURES.md: %v", err)
	}
	return out
}

// TestRunnerParityGate_DivergenceRefsResolve enforces that every
// divergence number cited in `pigOnlyRunnerMethods` (in
// runner_method_parity_test.go) actually exists in DIVERGENCES.md.
// Values that are SDK-surface rationales rather than `D<N>` references
// (language-surface accessor additions, which the DIVERGENCES.md header
// excludes) are not divergence citations and are skipped.
//
// Without this gate, a typo like `"D9"` in pigOnlyRunnerMethods would
// silently pass: the parity gate only checks "is this method allowed?",
// not "does the cited divergence exist?". This closes that loop.
func TestRunnerParityGate_DivergenceRefsResolve(t *testing.T) {
	known := loadDivergenceNumbers(t)
	for method, ref := range pigOnlyRunnerMethods {
		if !divergenceRefRE.MatchString(ref) {
			continue // SDK-surface rationale, not a divergence reference
		}
		if !known[ref] {
			t.Errorf("pigOnlyRunnerMethods[%q] = %q, but no `## %s` section exists in DIVERGENCES.md. "+
				"Either fix the typo or add the divergence section.",
				method, ref, ref)
		}
	}
}

// TestDivergenceDoc_HasExpectedActiveCount is a soft sanity check: the
// "Active divergences" header in DIVERGENCES.md states a count, and that
// count should match the number of `## DN` sections. Drift here means
// either a section was added without bumping the count, or vice versa.
//
// This is enforced loosely: we read the count from the heading and
// compare. If the heading is reformatted the test self-disables (logs a
// warning) rather than failing, so the gate doesn't become an obstacle
// to legitimate refactors.
func TestDivergenceDoc_HasExpectedActiveCount(t *testing.T) {
	known := loadDivergenceNumbers(t)

	_, thisFile, _, _ := runtime.Caller(0)
	repoRoot := filepath.Join(filepath.Dir(thisFile), "..", "..")
	docPath := filepath.Join(repoRoot, "DIVERGENCES.md")
	data, err := os.ReadFile(docPath)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	headerRE := regexp.MustCompile(`(?m)^## Active divergences \((\d+)\)`)
	m := headerRE.FindSubmatch(data)
	if m == nil {
		t.Skip("DIVERGENCES.md heading format changed: skipping count check (update test or restore heading)")
		return
	}
	wantStr := string(m[1])
	gotN := len(known)
	wantN := 0
	for _, c := range wantStr {
		wantN = wantN*10 + int(c-'0')
	}
	if gotN != wantN {
		t.Errorf("DIVERGENCES.md header says %d active divergences but %d `## DN` sections exist. "+
			"Update the header or the sections to match.", wantN, gotN)
	}
}
