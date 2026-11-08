package oracle

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"testing"
)

// Go-only: Pi's cases for the browser's own TypeScript that need more than their file's relative imports (the plugin API
// `@p2/app/v1`, Chord, the schema package) are re-assembled from Pi's test file for Node's runner; each case keeps its Pi title.

// NodeCase is one case (or suite) of a `node --test` run, by its full title (suite titles, then the case's, joined by " > ").
type NodeCase struct {
	Title string `json:"title"`
	// Nesting is how deep the case sits: 0 for a top-level suite or a file that failed to load.
	Nesting int  `json:"nesting"`
	OK      bool `json:"ok"`
	Skip    bool `json:"skip"`
	Todo    bool `json:"todo"`
	// Error is the failure's stack when OK is false.
	Error string `json:"error"`
}

// RunNodeCases runs the node:test file (a path relative to apps/p2) in Node on the vendored sources (oracle/source-hooks.mjs
// resolves `@p2/app/v1` and the `@earendil-works/*` packages to them) and returns every case and suite it reported, in the
// order they finished. A missing node binary fails the test: the oracle is never skipped. A run that fails is returned all the
// same: its cases say which.
func RunNodeCases(t testing.TB, file string) []NodeCase {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatalf("the Node cases need node (run in the tern-ci image): %v", err)
	}
	app := filepath.Join(Root(), "apps/p2")
	cmd := exec.Command(node,
		"--import", filepath.Join(app, "oracle/source-hooks.mjs"),
		"--test", "--test-concurrency=1", "--test-reporter="+filepath.Join(app, "oracle/json-reporter.mjs"),
		filepath.Join(app, file))
	cmd.Dir = app
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, runErr := cmd.Output()
	var tests []NodeCase
	for _, line := range bytes.Split(out, []byte("\n")) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var test NodeCase
		if err := json.Unmarshal(line, &test); err != nil {
			t.Fatalf("node --test %s: reporter line %q: %v\n%s", file, line, err, stderr.String())
		}
		tests = append(tests, test)
	}
	if len(tests) == 0 {
		t.Fatalf("node --test %s reported nothing: %v\n%s", file, runErr, stderr.String())
	}
	return tests
}
