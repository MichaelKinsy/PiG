//go:build windows

package env

import (
	"bytes"
	"context"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/agent/harness"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// execCommandLineCases hold the characters whose Windows command-line quoting
// differs between libuv's quote_cmd_arg and Go's syscall.EscapeArg, or that
// select a quote_cmd_arg branch: double quotes with and without spaces,
// backslash runs before a quote and at the end, empty strings, and tabs.
var execCommandLineCases = []string{
	`a"b`,
	`"`,
	`x\"y`,
	`a\b\`,
	`echo "x y"`,
	`echo a\"b`,
	`echo "a\\" "b"`,
	`echo trailing\`,
	`printf '[%s]' "" ''`,
	"printf '%s|' \"tab\there\"",
	`echo"x"y`,
	``,
}

// execOutcome is a command's exit code, the text the environment captured,
// and what bash wrote to stderr.
type execOutcome struct {
	ExitCode int    `json:"exitCode"`
	Text     string `json:"text"`
	Stderr   string `json:"stderr"`
}

// piExecOutcomes runs each command through Pi's own NodeExecutionEnv.exec
// (harness/env/nodejs.ts), which starts Git Bash with Node's
// child_process.spawn(shell, ["-c", command]), and reads bash's stderr from
// the probe's file after each command.
func piExecOutcomes(t *testing.T, cwd, stderr string, env map[string]string, commands []string) []execOutcome {
	t.Helper()
	root, err := filepath.EvalSymlinks("../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/node_modules/@earendil-works/pi-agent-core")
	if err != nil {
		t.Fatal(err)
	}
	input, err := json.Marshal(map[string]any{"root": root, "cwd": cwd, "stderr": stderr, "env": env, "commands": commands})
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "--input-type=module", "-e", `
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { pathToFileURL } from "node:url";
const { root, cwd, stderr, env, commands } = JSON.parse(readFileSync(0, "utf8"));
const load = (path) => import(pathToFileURL(join(root, "dist", ...path)).href);
const { NodeExecutionEnv } = await load(["harness", "env", "nodejs.js"]);
const { applyShellOutputUpdate } = await load(["harness", "utils", "output-capture.js"]);
const shell = new NodeExecutionEnv({ cwd });
const outcomes = [];
for (const command of commands) {
	let output;
	const result = await shell.exec(command, { env, onUpdate: (update) => { output = applyShellOutputUpdate(output, update); } }, {});
	if (!result.ok) throw result.error;
	outcomes.push({ exitCode: result.value.exitCode, text: output?.text ?? "", stderr: readFileSync(stderr, "utf8") });
}
process.stdout.write(JSON.stringify(outcomes));
`)
	cmd.Stdin = bytes.NewReader(input)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi NodeExecutionEnv: %v; output %s", err, out)
	}
	var outcomes []execOutcome
	if err := json.Unmarshal(out, &outcomes); err != nil || len(outcomes) != len(commands) {
		t.Fatalf("decode Pi outcomes %s: %v", out, err)
	}
	return outcomes
}

// Pi's NodeExecutionEnv builds Git Bash's command line with libuv's
// quote_cmd_arg. Git Bash parses that line with MSYS2 rules, not the C
// runtime's, so any byte of difference changes the command bash receives. A
// BASH_ENV script prints BASH_EXECUTION_STRING, the -c argument as bash
// received it, before the command runs. The exit code, the captured stdout,
// and bash's stderr each match Pi's exactly; the script keeps stderr out of
// the captured text because the order in which Pi and PiG merge two pipes
// varies between runs.
func TestExecPassesCommandsToGitBashAsPiDoes(t *testing.T) {
	probe, stderr := testenv.BashExecutionStringProbe(t)
	cwd := t.TempDir()
	overrides := map[string]string{"BASH_ENV": probe}
	want := piExecOutcomes(t, cwd, stderr, overrides, execCommandLineCases)
	if !slices.ContainsFunc(want, func(outcome execOutcome) bool { return outcome.Stderr != "" }) {
		t.Fatal("no command wrote stderr to the probe's file")
	}
	shell := NewNodeExecutionEnv(NodeExecutionEnvOptions{Cwd: cwd})
	for i, command := range execCommandLineCases {
		result, collected, err := execCollect(context.Background(), shell, command, &harness.ShellExecOptions{Env: overrides})
		mustDo(t, err)
		if got := (execOutcome{ExitCode: result.ExitCode, Text: collected.text(), Stderr: testenv.BashStderr(t, stderr)}); got != want[i] {
			t.Errorf("command %q\n got %+v\nwant %+v", command, got, want[i])
		}
	}
}
