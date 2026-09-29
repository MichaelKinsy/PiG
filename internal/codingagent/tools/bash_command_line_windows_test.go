//go:build windows

package tools

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// bashCommandLineCases hold the characters whose Windows command-line quoting
// differs between libuv's quote_cmd_arg and Go's syscall.EscapeArg, or that
// select a quote_cmd_arg branch: double quotes with and without spaces,
// backslash runs before a quote and at the end, empty strings, and tabs.
var bashCommandLineCases = []string{
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

// bashToolOutcome is the bash tool's result and what bash wrote to stderr.
type bashToolOutcome struct {
	IsError bool   `json:"isError"`
	Text    string `json:"text"`
	Stderr  string `json:"stderr"`
}

// piBashToolOutcomes runs each command through Pi's own bash tool definition
// (core/tools/bash.ts), which starts Git Bash with Node's
// child_process.spawn(shell, ["-c", command]), and reads bash's stderr from
// the probe's file after each command.
func piBashToolOutcomes(t *testing.T, cwd, stderr string, commands []string) []bashToolOutcome {
	t.Helper()
	root, err := filepath.EvalSymlinks("../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent")
	if err != nil {
		t.Fatal(err)
	}
	input, err := json.Marshal(map[string]any{"module": filepath.Join(root, "dist", "core", "tools", "bash.js"), "cwd": cwd, "stderr": stderr, "commands": commands})
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "--input-type=module", "-e", `
import { readFileSync } from "node:fs";
import { pathToFileURL } from "node:url";
const { module, cwd, stderr, commands } = JSON.parse(readFileSync(0, "utf8"));
const { createBashToolDefinition } = await import(pathToFileURL(module).href);
const tool = createBashToolDefinition(cwd);
const outcomes = [];
for (const command of commands) {
	try {
		const result = await tool.execute("call", { command });
		outcomes.push({ isError: false, text: result.content.map((part) => part.text).join(""), stderr: readFileSync(stderr, "utf8") });
	} catch (error) {
		outcomes.push({ isError: true, text: error.message, stderr: readFileSync(stderr, "utf8") });
	}
}
process.stdout.write(JSON.stringify(outcomes));
`)
	cmd.Stdin = bytes.NewReader(input)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi bash tool: %v; output %s", err, out)
	}
	var outcomes []bashToolOutcome
	if err := json.Unmarshal(out, &outcomes); err != nil || len(outcomes) != len(commands) {
		t.Fatalf("decode Pi outcomes %s: %v", out, err)
	}
	return outcomes
}

// Pi's bash tool builds Git Bash's command line with libuv's quote_cmd_arg.
// Git Bash parses that line with MSYS2 rules, not the C runtime's, so any
// byte of difference changes the command bash receives and runs. A BASH_ENV
// script prints BASH_EXECUTION_STRING before the command runs. The result and
// bash's stderr each match Pi's exactly; the script keeps stderr out of the
// result because Pi merges two pipes in an order that varies between runs.
func TestWin_BashToolPassesCommandsToGitBashAsPiDoes(t *testing.T) {
	probe, stderr := testenv.BashExecutionStringProbe(t)
	t.Setenv("BASH_ENV", probe)
	cwd := t.TempDir()
	want := piBashToolOutcomes(t, cwd, stderr, bashCommandLineCases)
	if !slices.ContainsFunc(want, func(outcome bashToolOutcome) bool { return outcome.Stderr != "" }) {
		t.Fatal("no command wrote stderr to the probe's file")
	}
	for i, command := range bashCommandLineCases {
		result := bashPort(t, &BashTool{CWD: cwd}, command)
		if got := (bashToolOutcome{IsError: result.IsError, Text: result.Text(), Stderr: testenv.BashStderr(t, stderr)}); got != want[i] {
			t.Errorf("command %q\n got %+v\nwant %+v", command, got, want[i])
		}
	}
}
