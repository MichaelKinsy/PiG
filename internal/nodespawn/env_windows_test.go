//go:build windows

package nodespawn

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// Node's normalizeSpawnArguments keeps the first, in UTF-16 order, of the env
// names whose String.prototype.toUpperCase values are equal
// (lib/child_process.js). toUpperCase applies Unicode's full case mapping, so
// "ß" uppercases to "SS", "ﬁ" to "FI", and "ŉ" to "ʼN": PIG_SS drops PIG_ß,
// PIG_FI drops PIG_ﬁ, and PIG_ŉ drops PIG_ʼN, which sorts after it. Node's
// spawn with that env object is the oracle; the test binary reports the
// environment it received. Both environments are compared sorted, since
// os/exec orders the block of non-ASCII names differently from libuv.
func TestSetEnvDropsNamesThatUppercaseAlikeAsNodeDoes(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	env := []string{
		testenv.EnvironHelper + "=1",
		"PIG_ß=sharp", "PIG_SS=double",
		"PIG_ﬁ=ligature", "PIG_FI=letters",
		"PIG_ʼN=apostrophe", "PIG_ŉ=preceded",
		"PIG_Ω=omega", "PIG_ω=small-omega",
	}
	object := map[string]string{}
	for _, pair := range env {
		name, value, _ := strings.Cut(pair, "=")
		object[name] = value
	}
	input, err := json.Marshal(map[string]any{"file": exe, "env": object})
	if err != nil {
		t.Fatal(err)
	}
	node := exec.CommandContext(t.Context(), "node", "-e", `
const { spawnSync } = require("node:child_process");
const { file, env } = JSON.parse(require("node:fs").readFileSync(0, "utf8"));
const result = spawnSync(file, [], { env, encoding: "utf8" });
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
	if slices.Contains(want, "PIG_ß=sharp") || !slices.Contains(want, "PIG_SS=double") {
		t.Fatalf("Node's child environment %q: want PIG_SS without PIG_ß", want)
	}
	cmd := exec.Command(exe)
	SetEnv(cmd, env)
	SetProgram(cmd)
	SetCommandLine(cmd)
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("%v; output %s", err, output)
	}
	var got []string
	if err := json.Unmarshal(output, &got); err != nil {
		t.Fatalf("decode %q: %v", output, err)
	}
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("child environment\n got %q\nwant %q", got, want)
	}
}

// libuv's make_program_env (src/win/process.c, libuv 1.52.1) adds a required
// variable that the env lacks when GetEnvironmentVariableW(name, NULL, 0)
// returns a nonzero size. That size includes the terminating null, so a
// variable with an empty value returns 1 and libuv adds "TEMP=". Node's spawn
// with an env that lacks TEMP, while Pi's TEMP is empty, is the oracle.
func TestSetEnvAddsARequiredVariableWithAnEmptyValueAsLibuvDoes(t *testing.T) {
	t.Setenv("TEMP", "")
	requireSameChildEnvironment(t, []string{testenv.EnvironHelper + "=1"})
}
