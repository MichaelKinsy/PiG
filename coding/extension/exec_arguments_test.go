package extension_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// execArgumentCase is one execCommand(command, args, cwd) call whose arguments
// Node's spawn rejects.
type execArgumentCase struct {
	Command string   `json:"command"`
	Args    []string `json:"args"`
	Cwd     string   `json:"cwd"`
}

// Pi's execCommand (core/exec.ts) calls spawn inside its Promise executor, so
// the ERR_INVALID_ARG_VALUE TypeError that Node's normalizeSpawnArguments
// throws for an empty file or a NUL in the file, an argument, or the cwd
// rejects the call on every platform. The message renders the value with
// util.inspect: quotes chosen by content, escapes, line splitting past 76
// units, and the cut at 128 units. Pi's own execCommand is the oracle.
func TestExecCommandRejectsInvalidArgumentsAsPiDoes(t *testing.T) {
	long := strings.Repeat("x", 70) + "\n" + strings.Repeat("y", 20) + "\x00\n" + "z"
	cases := []execArgumentCase{
		{Command: ""},
		{Command: "", Args: []string{"a\x00"}},
		{Command: "a\x00b"},
		{Command: "node", Args: []string{"ok", "it's \x00"}},
		{Command: "node", Args: []string{"'\"`\x00"}},
		{Command: "node", Args: []string{"'\"${\x00"}},
		{Command: "node", Args: []string{"\x1b\x7f\u0085\u2028\\\t\x00"}},
		{Command: "node", Args: []string{long}},
		{Command: "node", Args: []string{"\x00" + strings.Repeat("é", 200)}},
		{Command: "node", Args: []string{strings.Repeat("a", 125) + "😀\x00"}},
		{Command: "node", Args: []string{strings.Repeat("a", 126) + "😀\x00"}},
		{Command: "node", Cwd: `C:\dir` + "\x00"},
		{Command: "node", Args: []string{"x\x00"}, Cwd: "d\x00"},
	}
	pi, err := filepath.EvalSymlinks("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent")
	if err != nil {
		t.Fatal(err)
	}
	input, err := json.Marshal(map[string]any{"module": filepath.Join(pi, "dist", "core", "exec.js"), "cases": cases})
	if err != nil {
		t.Fatal(err)
	}
	node := exec.CommandContext(t.Context(), "node", "--input-type=module", "-e", `
import { readFileSync } from "node:fs";
import { pathToFileURL } from "node:url";
const { module, cases } = JSON.parse(readFileSync(0, "utf8"));
const { execCommand } = await import(pathToFileURL(module).href);
const outcomes = [];
for (const { command, args, cwd } of cases) {
	try {
		await execCommand(command, args ?? [], cwd);
		outcomes.push("resolved");
	} catch (error) {
		outcomes.push(error.message);
	}
}
process.stdout.write(JSON.stringify(outcomes));
`)
	node.Stdin = bytes.NewReader(input)
	out, err := node.Output()
	if err != nil {
		t.Fatalf("Pi execCommand: %v; output %s", err, out)
	}
	var want []string
	if err := json.Unmarshal(out, &want); err != nil || len(want) != len(cases) {
		t.Fatalf("decode Pi outcomes %s: %v", out, err)
	}
	for i, c := range cases {
		got := "resolved"
		if _, err := extension.ExecCommand(context.Background(), t.TempDir(), c.Command, c.Args, &extension.ExecOptions{CWD: c.Cwd}); err != nil {
			got = err.Error()
		}
		if !strings.HasPrefix(want[i], "The ") {
			t.Errorf("case %d: Pi outcome %q is not an argument error", i, want[i])
		}
		if got != want[i] {
			t.Errorf("case %d %+q\n got %q\nwant %q", i, c, got, want[i])
		}
	}
}
