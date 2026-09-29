//go:build windows

package codingagent

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Pi's runClipboardCommand (utils/clipboard-command.ts) spawns the command
// with shell false, so on Windows libuv finds only a .com or .exe program: a
// clipboard helper installed as pigclip.cmd does not run, and the error event
// resolves the call as a failure. Pi's own runClipboardCommand is the oracle.
func TestClipboardCommandFindsProgramsAsPiDoes(t *testing.T) {
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "pigclip.cmd"), []byte("@echo batch clipboard\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	root, err := filepath.EvalSymlinks("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent")
	if err != nil {
		t.Fatal(err)
	}
	commands := []string{"pigclip", "hostname"}
	input, err := json.Marshal(map[string]any{"module": filepath.Join(root, "dist", "utils", "clipboard-command.js"), "commands": commands})
	if err != nil {
		t.Fatal(err)
	}
	node := exec.CommandContext(t.Context(), "node", "--input-type=module", "-e", `
import { readFileSync } from "node:fs";
import { pathToFileURL } from "node:url";
const { module, commands } = JSON.parse(readFileSync(0, "utf8"));
const { runClipboardCommand } = await import(pathToFileURL(module).href);
const outcomes = [];
for (const command of commands) {
	const output = await runClipboardCommand(command, []);
	outcomes.push({ ok: output !== undefined, output: output === undefined ? "" : output.toString("utf8") });
}
process.stdout.write(JSON.stringify(outcomes));
`)
	node.Stdin = bytes.NewReader(input)
	out, err := node.Output()
	if err != nil {
		t.Fatalf("Pi runClipboardCommand: %v; output %s", err, out)
	}
	type outcome struct {
		OK     bool   `json:"ok"`
		Output string `json:"output"`
	}
	var want []outcome
	if err := json.Unmarshal(out, &want); err != nil || len(want) != len(commands) {
		t.Fatalf("decode Pi outcomes %s: %v", out, err)
	}
	for i, command := range commands {
		output, ok := runClipboardCommand(command, nil, clipboardCommandOptions{})
		if got := (outcome{OK: ok, Output: string(output)}); got != want[i] {
			t.Errorf("command %q: got %+v, want Pi's %+v", command, got, want[i])
		}
	}
}
