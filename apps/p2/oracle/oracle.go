// Package oracle runs Pi's side of a comparison in Node: it loads p2's TypeScript contracts, vendored verbatim under
// apps/p2/web/upstream, on the Chord copy PiG vendors for its plugin runtime, and reports each contract's name, version
// and fingerprint. p2's browser client is Pi's TypeScript and binds to the Go server by fingerprint, so every contract
// the Go port declares must hash exactly as Pi's does.
package oracle

import (
	"encoding/json"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// Contract is what Pi's Chord reports for one exported contract.
type Contract struct {
	Name        string `json:"name"`
	Version     int    `json:"version"`
	Fingerprint string `json:"fingerprint"`
}

// Root is the repository root (the directory of go.work).
func Root() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..", "..")
}

// Fingerprints loads each module (a path relative to apps/p2/web/upstream) in Node and returns the contracts it exports,
// keyed "<module>#<export>". A missing node binary fails the test: the oracle is never skipped.
func Fingerprints(t testing.TB, modules ...string) map[string]Contract {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatalf("the contract oracle needs node (run in the tern-ci image): %v", err)
	}
	root := Root()
	args := append([]string{filepath.Join(root, "apps/p2/oracle/fingerprints.mjs"), root}, modules...)
	cmd := exec.Command(node, args...)
	cmd.Stderr = nil
	out, err := cmd.Output()
	if err != nil {
		stderr := ""
		if exit, ok := err.(*exec.ExitError); ok {
			stderr = string(exit.Stderr)
		}
		t.Fatalf("node %v: %v\n%s", modules, err, stderr)
	}
	contracts := map[string]Contract{}
	if err := json.Unmarshal(out, &contracts); err != nil {
		t.Fatalf("oracle output %q: %v", out, err)
	}
	return contracts
}
