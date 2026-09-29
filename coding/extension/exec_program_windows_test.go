//go:build windows

package extension_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

type execProgramOutcome struct {
	Result   extension.ExecResult `json:"result"`
	Rejected string               `json:"rejected"`
}

func copyProgram(t *testing.T, source, target string) {
	t.Helper()
	data, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, data, 0o755); err != nil {
		t.Fatal(err)
	}
}

// Pi's execCommand (core/exec.ts) runs spawn(command, args, { cwd, shell:
// false }), so libuv finds the program: in cwd first unless
// NoDefaultCurrentDirectoryInExePath is set, then in PATH, trying only .com
// and .exe. "npm" does not find npm.cmd and resolves with code 1 after the
// ENOENT error event; "npm.cmd" makes spawn throw EINVAL, which rejects the
// call. Pi's own execCommand is the oracle.
func TestExecCommandFindsProgramsAsPiDoes(t *testing.T) {
	hostname := filepath.Join(os.Getenv("SystemRoot"), "System32", "HOSTNAME.EXE")
	root := t.TempDir()
	cwd := filepath.Join(root, "cwd")
	onPath := filepath.Join(root, "bin")
	for _, dir := range []string{cwd, onPath} {
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "npm.cmd"), []byte("@echo npm batch\r\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	copyProgram(t, hostname, filepath.Join(cwd, "tool.exe"))
	copyProgram(t, hostname, filepath.Join(onPath, "onpath.exe"))
	t.Setenv("PATH", onPath+string(os.PathListSeparator)+os.Getenv("PATH"))
	commands := []string{"npm", "npm.cmd", "NPM.CMD ", "tool", "onpath", "missing"}
	pi, err := filepath.EvalSymlinks("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent")
	if err != nil {
		t.Fatal(err)
	}
	for _, searchCwd := range []bool{true, false} {
		t.Run(map[bool]string{true: "cwd searched", false: "NoDefaultCurrentDirectoryInExePath"}[searchCwd], func(t *testing.T) {
			t.Setenv("NoDefaultCurrentDirectoryInExePath", "1")
			if searchCwd {
				if err := os.Unsetenv("NoDefaultCurrentDirectoryInExePath"); err != nil {
					t.Fatal(err)
				}
			}
			input, err := json.Marshal(map[string]any{"module": filepath.Join(pi, "dist", "core", "exec.js"), "cwd": cwd, "commands": commands})
			if err != nil {
				t.Fatal(err)
			}
			node := exec.CommandContext(t.Context(), "node", "--input-type=module", "-e", `
import { readFileSync } from "node:fs";
import { pathToFileURL } from "node:url";
const { module, cwd, commands } = JSON.parse(readFileSync(0, "utf8"));
const { execCommand } = await import(pathToFileURL(module).href);
const outcomes = [];
for (const command of commands) {
	try {
		outcomes.push({ result: await execCommand(command, [], cwd), rejected: "" });
	} catch (error) {
		outcomes.push({ result: { stdout: "", stderr: "", code: 0, killed: false }, rejected: error.message });
	}
}
process.stdout.write(JSON.stringify(outcomes));
`)
			node.Stdin = bytes.NewReader(input)
			out, err := node.Output()
			if err != nil {
				t.Fatalf("Pi execCommand: %v; output %s", err, out)
			}
			var want []execProgramOutcome
			if err := json.Unmarshal(out, &want); err != nil || len(want) != len(commands) {
				t.Fatalf("decode Pi outcomes %s: %v", out, err)
			}
			for i, command := range commands {
				result, err := extension.ExecCommand(context.Background(), cwd, command, nil, nil)
				got := execProgramOutcome{Result: result}
				if err != nil {
					got = execProgramOutcome{Rejected: err.Error()}
				}
				if got != want[i] {
					t.Errorf("command %q\n got %+v\nwant %+v", command, got, want[i])
				}
			}
		})
	}
}
