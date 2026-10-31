// Package pioracle runs the compiled Pi modules vendored for the Node extension runtime against Go test inputs, so a test compares Pig with Pi itself rather than with a transcription of Pi's behavior.
package pioracle

import (
	"bytes"
	"encoding/json"
	"net/url"
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
	return filepath.Join(repoRoot(t), filepath.FromSlash(distRoot))
}

// d217Register is the file URL of the D217 hook of tui/testdata/pi-d217, or "" once that directory is gone. The hook swaps the vendored
// pi-tui utils.js and image-resize-core.js for the pin's sources with the Pi fixes 0.6.0 carries (D217), after checking that the module
// it replaces is the pinned one, so an oracle compares what PiG ships. Delete this with the directory when the pin passes c2760d32.
func d217Register(t testing.TB) string {
	t.Helper()
	root := repoRoot(t)
	register := filepath.Join(root, "tui", "testdata", "pi-d217", "register.mjs")
	if _, err := os.Stat(register); err != nil {
		return ""
	}
	return (&url.URL{Scheme: "file", Path: filepath.ToSlash(register)}).String()
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
	args := []string{"--input-type=module", "-e", script}
	if register := d217Register(t); register != "" {
		args = append([]string{"--import", register}, args...)
	}
	//nolint:gosec // G204 (class b): program is node from exec.LookPath; args are fixed --input-type=module -e with a constant script (and the D217 hook import), and the input is on stdin.
	cmd := exec.CommandContext(t.Context(), node, args...)
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
