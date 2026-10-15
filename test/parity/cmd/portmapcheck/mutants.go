package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
)

// mutant is a reviewed one-site edit of a cited Go file: Old occurs exactly once in File and New replaces it. A test that carries a
// `// pi:` marker proves its Pi file only when the unmutated tests pass and at least one mutant makes them fail (red-proof).
type mutant struct {
	File string `json:"file"`
	Old  string `json:"old"`
	New  string `json:"new"`
}

// onlyMarkerReason reports whether the marker is the sole reason a row is unproven.
func onlyMarkerReason(res result) bool {
	return len(res.Reasons) == 1 && strings.HasPrefix(res.Reasons[0], "MARKER-UNPROVEN")
}

// mutantProblem returns why a marker row has no killed mutant, or "". Each mutant is replayed through `go test -overlay`, so the work
// tree is never modified.
func mutantProblem(root string, res result, mutants []mutant) string {
	if len(mutants) == 0 {
		return "MARKER-UNPROVEN: portmap-mutants.json names no mutant of a cited file for this row"
	}
	cited := res.Cited
	for _, mu := range mutants {
		if !slices.Contains(cited, mu.File) {
			return "MARKER-UNPROVEN: mutant file " + mu.File + " is not a cited file"
		}
		if strings.HasSuffix(mu.File, "_test.go") {
			// Editing the evidence itself (an assertion turned into t.Fatal) fails the tests without touching the ported code.
			return "MARKER-UNPROVEN: mutant file " + mu.File + " is a test file; a mutant must edit the cited implementation"
		}
		if killed(root, res, mu) {
			return ""
		}
	}
	return "MARKER-UNPROVEN: no listed mutant makes the marker tests fail (the tests would pass without the cited code)"
}

// killed replays one mutant: the old text must occur once, and the evidence tests must fail with a test failure (not a build error).
func killed(root string, res result, mu mutant) bool {
	src, err := os.ReadFile(filepath.Join(root, mu.File))
	if err != nil || strings.Count(string(src), mu.Old) != 1 {
		return false
	}
	dir, err := os.MkdirTemp("", "portmap-mutant-")
	if err != nil {
		return false
	}
	defer func() { _ = os.RemoveAll(dir) }()
	mutated := filepath.Join(dir, "mutated.go")
	if err := os.WriteFile(mutated, []byte(strings.Replace(string(src), mu.Old, mu.New, 1)), 0o644); err != nil {
		return false
	}
	overlay, _ := json.Marshal(map[string]map[string]string{"Replace": {filepath.Join(root, mu.File): mutated}})
	overlayFile := filepath.Join(dir, "overlay.json")
	if err := os.WriteFile(overlayFile, overlay, 0o644); err != nil {
		return false
	}
	byDir := map[string][]string{}
	for _, t := range res.Tests {
		d, name, _ := strings.Cut(t, "#")
		byDir[d] = append(byDir[d], name)
	}
	for d, names := range byDir {
		slices.Sort(names)
		cmd := exec.Command("go", "test", "-count=1", "-overlay="+overlayFile, "-run", "^("+strings.Join(names, "|")+")$", "./"+d)
		cmd.Dir = root
		cmd.Env = evidenceEnv(dir)
		out, err := cmd.CombinedOutput()
		if err != nil && assertionFailure(string(out)) {
			return true
		}
	}
	return false
}

// assertionFailure reports whether go test output is a failed assertion. A build error is not a kill, and neither is a panic or a
// timeout: a mutant that panics (or loops) in the cited code fails every test that merely executes it, which rule 3's coverage
// already shows, so it proves nothing about what the tests assert.
func assertionFailure(out string) bool {
	return strings.Contains(out, "--- FAIL") && !strings.Contains(out, "[build failed]") && !strings.Contains(out, "\npanic: ") &&
		!strings.HasPrefix(out, "panic: ")
}
