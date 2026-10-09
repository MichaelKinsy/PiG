// Package pioracle runs the compiled Pi 1.0.4 modules vendored for the Node extension runtime against Go test inputs, so a test compares Pig with Pi itself rather than with a transcription of Pi's behavior.
package pioracle

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// distRoot is the vendored compiled Pi packages.
const distRoot = "coding/extension/host/subprocess/runtime-node/shims/pi-dist"

// Root returns the absolute path of the vendored Pi distribution.
func Root(t testing.TB) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return filepath.Join(dir, filepath.FromSlash(distRoot))
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("pioracle: go.mod not found above the test directory")
		}
		dir = parent
	}
}

// Run executes an ES module body in Node with the Pi distribution root in `root`, the decoded `input` in scope, and returns the JSON the body passes to `emit`. The body is the async function body of the check. Environment extra entries (KEY=value) are added to the child environment. A missing Node is a failure: the Pig test suite requires it.
func Run(t testing.TB, body string, input, output any, env ...string) {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatalf("pioracle: node is required: %v", err)
	}
	payload, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	script := `import { readFileSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = process.env.PI_ORACLE_ROOT;
const input = JSON.parse(readFileSync(0, "utf8"));
const load = (path) => import(pathToFileURL(root + "/" + path).href);
let result;
const emit = (value) => { result = value; };
await (async () => {
` + body + `
})();
process.stdout.write(JSON.stringify(result));
`
	cmd := exec.CommandContext(t.Context(), node, "--input-type=module", "-e", script)
	cmd.Stdin = bytes.NewReader(payload)
	cmd.Env = append(os.Environ(), "PI_ORACLE_ROOT="+Root(t))
	cmd.Env = append(cmd.Env, env...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("pioracle: node failed: %v\n%s", err, stderr.String())
	}
	if err := json.Unmarshal(stdout.Bytes(), output); err != nil {
		t.Fatalf("pioracle: undecodable output %q: %v", stdout.String(), err)
	}
}
