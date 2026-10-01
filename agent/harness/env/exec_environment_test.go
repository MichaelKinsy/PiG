package env

import (
	"bytes"
	"context"
	"encoding/json"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent/harness"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// environmentCase is one NodeExecutionEnv with shellEnv and one exec with env
// and inheritEnv.
type environmentCase struct {
	ShellEnv   map[string]string `json:"shellEnv"`
	Env        map[string]string `json:"env"`
	InheritEnv *bool             `json:"inheritEnv"`
}

// Pi's NodeExecutionEnv.exec spawns the shell with env getShellEnv(shellEnv,
// env, inheritEnv) (harness/env/nodejs.ts:248-258): {...process.env,
// ...shellEnv, ...env}, or {...env} alone. process.env enumerates PiG's
// environment block and, on Windows, skips the hidden "=C:" names and reads
// each value case-insensitively (src/node_env_var.cc). Node's
// normalizeSpawnArguments then builds the child's pairs in key order; on
// Windows it sorts the keys by UTF-16 code unit and keeps the first of names
// that differ only in case, so "PATH" wins over "Path" and "Path" over "path"
// (lib/child_process.js). libuv's make_program_env adds HOMEDRIVE, HOMEPATH,
// LOGONSERVER, PATH, SYSTEMDRIVE, SYSTEMROOT, TEMP, USERDOMAIN, USERNAME,
// USERPROFILE, and WINDIR from PiG's environment when the env lacks them
// (src/win/process.c). The test binary stands in for the shell and prints the
// environment it received, in order; Pi's own NodeExecutionEnv is the oracle.
func TestExecGivesTheShellPisEnvironment(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	// Names that differ from PiG's own PATH name only in case.
	pathName := "PATH"
	for _, entry := range os.Environ() {
		if name, _, ok := strings.Cut(entry, "="); ok && strings.EqualFold(name, "PATH") {
			pathName = name
			break
		}
	}
	var casings []string
	for _, name := range []string{"PATH", "Path", "path"} {
		if name != pathName {
			casings = append(casings, name)
		}
	}
	helper := map[string]string{testenv.EnvironHelper: "1"}
	with := func(extra map[string]string) map[string]string {
		merged := map[string]string{testenv.EnvironHelper: "1"}
		maps.Copy(merged, extra)
		return merged
	}
	off := false
	cases := []environmentCase{
		{Env: with(map[string]string{casings[0]: "/pig-first"})},
		{Env: with(map[string]string{casings[1]: "/pig-second"})},
		{Env: with(map[string]string{casings[0]: "/pig-first", casings[1]: "/pig-second"})},
		{ShellEnv: map[string]string{casings[0]: "/pig-base"}, Env: with(map[string]string{pathName: "/pig-exact"})},
		{Env: with(map[string]string{"PIG_ZZ": "1", "pig_aa": "2", "Pig_Mm": "3", "PIG_MM": "4"})},
		{Env: with(map[string]string{casings[1]: "/pig-only"}), InheritEnv: &off},
		{Env: helper, InheritEnv: &off},
		{Env: with(map[string]string{"PIG_A": "one", "PIG_A=B": "two"}), InheritEnv: &off},
	}
	cwd := t.TempDir()
	root, err := filepath.EvalSymlinks("../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/node_modules/@earendil-works/pi-agent-core")
	if err != nil {
		t.Fatal(err)
	}
	input, err := json.Marshal(map[string]any{"root": root, "cwd": cwd, "shell": exe, "cases": cases})
	if err != nil {
		t.Fatal(err)
	}
	node := exec.CommandContext(t.Context(), "node", "--input-type=module", "-e", `
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { pathToFileURL } from "node:url";
const { root, cwd, shell, cases } = JSON.parse(readFileSync(0, "utf8"));
const load = (path) => import(pathToFileURL(join(root, "dist", ...path)).href);
const { NodeExecutionEnv } = await load(["harness", "env", "nodejs.js"]);
const { applyShellOutputUpdate } = await load(["harness", "utils", "output-capture.js"]);
const outcomes = [];
for (const { shellEnv, env, inheritEnv } of cases) {
	let output;
	const options = { env, onUpdate: (update) => { output = applyShellOutputUpdate(output, update); } };
	if (inheritEnv !== null) options.inheritEnv = inheritEnv;
	const result = await new NodeExecutionEnv({ cwd, shellPath: shell, shellEnv: shellEnv ?? undefined }).exec("report", options, {});
	if (!result.ok) throw result.error;
	if (result.value.exitCode !== 0) throw new Error("exit code " + result.value.exitCode + ": " + output?.text);
	outcomes.push(JSON.parse(output?.text ?? "null"));
}
process.stdout.write(JSON.stringify(outcomes));
`)
	node.Stdin = bytes.NewReader(input)
	out, err := node.Output()
	if err != nil {
		t.Fatalf("Pi NodeExecutionEnv: %v; output %s", err, out)
	}
	var want [][]string
	if err := json.Unmarshal(out, &want); err != nil || len(want) != len(cases) {
		t.Fatalf("decode Pi environments %s: %v", out, err)
	}
	if runtime.GOOS == "windows" && !slices.ContainsFunc(want[len(want)-1], func(entry string) bool { return strings.HasPrefix(entry, "SYSTEMDRIVE=") }) {
		t.Fatalf("Pi's child without inherited environment lacks libuv's SYSTEMDRIVE: %q", want[len(want)-1])
	}
	for i, c := range cases {
		shell := NewNodeExecutionEnv(NodeExecutionEnvOptions{Cwd: cwd, ShellPath: exe, ShellEnv: c.ShellEnv})
		result, collected, err := execCollect(context.Background(), shell, "report", &harness.ShellExecOptions{Env: c.Env, InheritEnv: c.InheritEnv})
		mustDo(t, err)
		if result.ExitCode != 0 {
			t.Fatalf("case %d: exit code %d, output %q", i, result.ExitCode, collected.text())
		}
		var got []string
		if err := json.Unmarshal([]byte(collected.text()), &got); err != nil {
			t.Fatalf("case %d: decode %q: %v", i, collected.text(), err)
		}
		if !slices.Equal(got, want[i]) {
			t.Errorf("case %d %+v: shell environment differs from Pi's\n got %q\nwant %q", i, c, got, want[i])
		}
	}
}

// A name in the env option may contain "=", and libuv writes the property into
// the block as "name=value" without splitting the name from the rest. A
// property whose entry shares the text before its first "=" with another's
// still reaches the child, as it does in Pi. Pi's getShellEnv(undefined, env,
// false) is {...env}, so Node's spawn of the test binary with env, in the key
// order PiG gives it, is the oracle. Outside Windows the test binary's
// os.Environ reports what its getenv sees, the first of such entries. On
// Windows it reports the whole block in order, with the variables libuv's
// make_program_env adds (src/win/process.c).
func TestExecPassesEveryPropertyWhoseNameHoldsAnEqualsSign(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	off := false
	shell := NewNodeExecutionEnv(NodeExecutionEnvOptions{Cwd: t.TempDir(), ShellPath: exe})
	env := map[string]string{testenv.EnvironHelper: "1", "PIG_A": "one", "PIG_A=B": "two"}
	result, collected, err := execCollect(context.Background(), shell, "report", &harness.ShellExecOptions{Env: env, InheritEnv: &off})
	mustDo(t, err)
	if result.ExitCode != 0 {
		t.Fatalf("exit code %d, output %q", result.ExitCode, collected.text())
	}
	var got []string
	if err := json.Unmarshal([]byte(collected.text()), &got); err != nil {
		t.Fatalf("decode %q: %v", collected.text(), err)
	}
	want := nodeSpawnEnvironment(t, exe, env)
	if !slices.Contains(want, "PIG_A=one") || (runtime.GOOS == "windows") != slices.Contains(want, "PIG_A=B=two") {
		t.Fatalf("Node's child environment %q: want PIG_A=one, and PIG_A=B=two only on Windows", want)
	}
	if !slices.Equal(got, want) {
		t.Errorf("environment\n got %q\nwant %q", got, want)
	}
}

// nodeSpawnEnvironment is the environment that the test binary program reports
// when Node's spawnSync starts it with env, whose keys JSON gives in sorted
// order.
func nodeSpawnEnvironment(t *testing.T, program string, env map[string]string) []string {
	t.Helper()
	input, err := json.Marshal(map[string]any{"file": program, "env": env})
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
	var environment []string
	if err := json.Unmarshal(out, &environment); err != nil {
		t.Fatalf("decode %q: %v", out, err)
	}
	return environment
}
