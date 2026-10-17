//go:build !windows

package nodespawn

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// Node's spawn without an env option passes process.env to the child as it is
// (normalizeSpawnArguments in lib/child_process.js: `options.env ||
// process.env`), and the cwd option only changes the child's directory, so a
// child started in another directory still sees PiG's PWD. A shell's pwd then
// prints the physical directory, as under Pi, not the symlinked name it was
// given. Node's spawn with the same cwd is the oracle; the test binary reports
// the environment it received, in block order.
func TestStartWithoutEnvPassesPiGsEnvironmentAsNodeDoes(t *testing.T) {
	t.Setenv(testenv.EnvironHelper, "1")
	t.Setenv("PWD", t.TempDir())
	dir := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(t.TempDir(), dir); err != nil {
		t.Fatal(err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	input, err := json.Marshal(map[string]string{"file": exe, "cwd": dir})
	if err != nil {
		t.Fatal(err)
	}
	nodeBinary, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	node := exec.CommandContext(t.Context(), nodeBinary, "-e", `
const { spawnSync } = require("node:child_process");
const { file, cwd } = JSON.parse(require("node:fs").readFileSync(0, "utf8"));
const result = spawnSync(file, [], { cwd, encoding: "utf8" });
if (result.error || result.status !== 0) throw result.error ?? new Error(result.stderr);
process.stdout.write(result.stdout);
`)
	node.Stdin = bytes.NewReader(input)
	out, err := node.Output()
	if err != nil {
		t.Fatalf("node spawn: %v; output %s", err, out)
	}
	var want []string
	if err := json.Unmarshal(out, &want); err != nil {
		t.Fatalf("decode %q: %v", out, err)
	}
	cmd := exec.Command(exe)
	cmd.Dir = dir
	SetProgram(cmd)
	SetCommandLine(cmd)
	stdout := output(t, cmd)
	var got []string
	if err := json.Unmarshal(stdout, &got); err != nil {
		t.Fatalf("decode %q: %v", stdout, err)
	}
	if !slices.Equal(got, want) {
		onlyIn := func(a, b []string) []string {
			return slices.DeleteFunc(slices.Clone(a), func(entry string) bool { return slices.Contains(b, entry) })
		}
		t.Errorf("child environment in block order differs: only Start's %q, only Node's %q\n got %q\nwant %q", onlyIn(got, want), onlyIn(want, got), got, want)
	}
}
