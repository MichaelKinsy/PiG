//go:build windows

package pico3

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/shellconfig"
)

// Pi's pico3 bashTool (harness/pico3/bash.ts) runs spawn("bash", ["-c",
// command], { cwd }), so libuv looks for bash.com and bash.exe in the tool's
// cwd before PATH unless NoDefaultCurrentDirectoryInExePath is set. A copy of
// HOSTNAME.EXE named bash.exe in cwd shows which program ran; Git Bash leads
// PATH. Pi's own bashTool is the oracle. Each program writes to one stream at
// most, HOSTNAME.EXE only to stderr and Git Bash "git-bash" to stdout, so the
// streamed output is compared exactly.
func TestBashToolFindsBashAsPiDoes(t *testing.T) {
	shell, err := shellconfig.Default()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", filepath.Dir(shell.Path)+string(os.PathListSeparator)+os.Getenv("PATH"))
	cwd := t.TempDir()
	program, err := os.ReadFile(filepath.Join(os.Getenv("SystemRoot"), "System32", "HOSTNAME.EXE"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cwd, "bash.exe"), program, 0o755); err != nil {
		t.Fatal(err)
	}
	root, err := filepath.EvalSymlinks("../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/node_modules/@earendil-works/pi-agent-core")
	if err != nil {
		t.Fatal(err)
	}
	const command = "echo git-bash"
	for _, searchCwd := range []bool{true, false} {
		t.Run(map[bool]string{true: "cwd searched", false: "NoDefaultCurrentDirectoryInExePath"}[searchCwd], func(t *testing.T) {
			t.Setenv("NoDefaultCurrentDirectoryInExePath", "1")
			if searchCwd {
				if err := os.Unsetenv("NoDefaultCurrentDirectoryInExePath"); err != nil {
					t.Fatal(err)
				}
			}
			input, err := json.Marshal(map[string]any{"module": filepath.Join(root, "dist", "harness", "pico3", "bash.js"), "cwd": cwd, "command": command})
			if err != nil {
				t.Fatal(err)
			}
			node := exec.CommandContext(t.Context(), "node", "--input-type=module", "-e", `
import { readFileSync } from "node:fs";
import { pathToFileURL } from "node:url";
const { module, cwd, command } = JSON.parse(readFileSync(0, "utf8"));
const { bashTool } = await import(pathToFileURL(module).href);
const chunks = [];
const result = await bashTool().execute({ command, cwd }, { stream: (chunk) => chunks.push(Buffer.from(chunk)) }, {});
process.stdout.write(JSON.stringify({ isError: result.isError, exitCode: result.details.exitCode, output: Buffer.concat(chunks).toString("utf8") }));
`)
			node.Stdin = bytes.NewReader(input)
			out, err := node.Output()
			if err != nil {
				t.Fatalf("Pi pico3 bashTool: %v; output %s", err, out)
			}
			var want bashOutcome
			if err := json.Unmarshal(out, &want); err != nil {
				t.Fatalf("decode Pi outcome %s: %v", out, err)
			}
			var (
				mu       sync.Mutex
				streamed bytes.Buffer
			)
			api := &ToolApi{stream: func(chunk []byte) {
				mu.Lock()
				defer mu.Unlock()
				streamed.Write(chunk)
			}}
			result, err := runBash(context.Background(), JsonObject{"command": command, "cwd": cwd}, api)
			if err != nil {
				t.Fatal(err)
			}
			exitCode, _ := result.Details.(JsonObject)["exitCode"].(float64)
			got := bashOutcome{IsError: result.IsError, ExitCode: exitCode, Output: streamed.String()}
			if got != want {
				t.Errorf("got %+v\nwant %+v", got, want)
			}
		})
	}
}
