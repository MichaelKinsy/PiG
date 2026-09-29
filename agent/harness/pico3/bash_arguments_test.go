package pico3

import (
	"bytes"
	"context"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"testing"
)

// Pi's pico3 bashTool (harness/pico3/bash.ts:17) calls spawn("bash", ["-c",
// command], { cwd }) in its async execute, so the ERR_INVALID_ARG_VALUE that
// Node's normalizeSpawnArguments throws for a NUL in the command or the cwd
// rejects the call on every platform. Pi's own bashTool is the oracle.
func TestBashToolRejectsSpawnArgumentErrorsAsPiDoes(t *testing.T) {
	type bashCase struct {
		Command string `json:"command"`
		Cwd     string `json:"cwd"`
	}
	cases := []bashCase{
		{Command: "echo a\x00b", Cwd: t.TempDir()},
		{Command: "echo ok", Cwd: t.TempDir() + "\x00"},
	}
	root, err := filepath.EvalSymlinks("../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/node_modules/@earendil-works/pi-agent-core")
	if err != nil {
		t.Fatal(err)
	}
	input, err := json.Marshal(map[string]any{"module": filepath.Join(root, "dist", "harness", "pico3", "bash.js"), "cases": cases})
	if err != nil {
		t.Fatal(err)
	}
	node := exec.CommandContext(t.Context(), "node", "--input-type=module", "-e", `
import { readFileSync } from "node:fs";
import { pathToFileURL } from "node:url";
const { module, cases } = JSON.parse(readFileSync(0, "utf8"));
const { bashTool } = await import(pathToFileURL(module).href);
const outcomes = [];
for (const { command, cwd } of cases) {
	try {
		await bashTool().execute({ command, cwd }, { stream: () => {} }, {});
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
		t.Fatalf("Pi pico3 bashTool: %v; output %s", err, out)
	}
	var want []string
	if err := json.Unmarshal(out, &want); err != nil || len(want) != len(cases) {
		t.Fatalf("decode Pi outcomes %s: %v", out, err)
	}
	for i, c := range cases {
		got := "resolved"
		if _, err := runBash(context.Background(), JsonObject{"command": c.Command, "cwd": c.Cwd}, &ToolApi{stream: func([]byte) {}}); err != nil {
			got = err.Error()
		}
		if want[i] == "resolved" || got != want[i] {
			t.Errorf("case %d %+q\n got %q\nwant %q", i, c, got, want[i])
		}
	}
}
