//go:build windows

package codingagent

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// Pi's runClipboardCommand (utils/clipboard-command.ts) spawns the clipboard
// helper with windowsHide: true and stdio that inherits nothing, so libuv
// starts it with SW_HIDE and CREATE_NO_WINDOW. The test binary stands in for
// a clipboard reader and reports how Pi's runner and PiG's started it.
func TestClipboardCommandHidesTheWindowAsPiDoes(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(testenv.StartupHelper, "1")
	root, err := filepath.EvalSymlinks("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent")
	if err != nil {
		t.Fatal(err)
	}
	input, err := json.Marshal(map[string]any{"module": filepath.Join(root, "dist", "utils", "clipboard-command.js"), "command": exe})
	if err != nil {
		t.Fatal(err)
	}
	node := exec.CommandContext(t.Context(), "node", "--input-type=module", "-e", `
import { readFileSync } from "node:fs";
import { pathToFileURL } from "node:url";
const { module, command } = JSON.parse(readFileSync(0, "utf8"));
const { runClipboardCommand } = await import(pathToFileURL(module).href);
const output = await runClipboardCommand(command, []);
if (output === undefined) throw new Error("clipboard command failed");
process.stdout.write(output);
`)
	node.Stdin = bytes.NewReader(input)
	out, err := node.Output()
	if err != nil {
		t.Fatalf("Pi runClipboardCommand: %v; output %s", err, out)
	}
	var want testenv.Startup
	if err := json.Unmarshal(out, &want); err != nil {
		t.Fatalf("decode Pi report %q: %v", out, err)
	}
	output, err := runClipboardCommandContext(t.Context(), exe, nil, clipboardCommandOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var got testenv.Startup
	if err := json.Unmarshal(output, &got); err != nil {
		t.Fatalf("decode %q: %v", output, err)
	}
	if got != want {
		t.Errorf("clipboard command started with %+v, want Pi's %+v", got, want)
	}
}
