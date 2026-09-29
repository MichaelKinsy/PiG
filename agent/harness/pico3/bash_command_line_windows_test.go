//go:build windows

package pico3

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/shellconfig"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// bashOutcome is the tool result, the output it streamed, and what bash wrote
// to stderr when a probe keeps stderr out of the stream.
type bashOutcome struct {
	IsError  bool    `json:"isError"`
	ExitCode float64 `json:"exitCode"`
	Output   string  `json:"output"`
	Stderr   string  `json:"stderr"`
}

// Pi's pico3 bashTool (harness/pico3/bash.ts) runs spawn("bash", ["-c",
// command]), so Git Bash receives libuv's command line and parses it with
// MSYS2 rules. Git Bash leads PATH for both runs. A BASH_ENV script prints
// BASH_EXECUTION_STRING, the -c argument as bash received it, before the
// command runs. The result, the streamed stdout, and bash's stderr each match
// Pi's exactly; the script keeps stderr out of the stream because Pi and PiG
// stream stdout and stderr from two pipes in an order that varies between
// runs.
func TestBashToolPassesCommandsToGitBashAsPiDoes(t *testing.T) {
	shell, err := shellconfig.Default()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", filepath.Dir(shell.Path)+string(os.PathListSeparator)+os.Getenv("PATH"))
	probe, stderr := testenv.BashExecutionStringProbe(t)
	t.Setenv("BASH_ENV", probe)
	commands := []string{`a"b`, `"`, `x\"y`, `a\b\`, `echo"x"y`, `echo "x y"`, `echo a\"b trailing\`, `printf '[%s]' "" ''`, "printf '%s|' \"tab\there\"", ``}
	cwd := t.TempDir()
	root, err := filepath.EvalSymlinks("../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/node_modules/@earendil-works/pi-agent-core")
	if err != nil {
		t.Fatal(err)
	}
	input, err := json.Marshal(map[string]any{"module": filepath.Join(root, "dist", "harness", "pico3", "bash.js"), "cwd": cwd, "stderr": stderr, "commands": commands})
	if err != nil {
		t.Fatal(err)
	}
	node := exec.CommandContext(t.Context(), "node", "--input-type=module", "-e", `
import { readFileSync } from "node:fs";
import { pathToFileURL } from "node:url";
const { module, cwd, stderr, commands } = JSON.parse(readFileSync(0, "utf8"));
const { bashTool } = await import(pathToFileURL(module).href);
const tool = bashTool();
const outcomes = [];
for (const command of commands) {
	const chunks = [];
	const result = await tool.execute({ command, cwd }, { stream: (chunk) => chunks.push(Buffer.from(chunk)) }, {});
	outcomes.push({ isError: result.isError, exitCode: result.details.exitCode, output: Buffer.concat(chunks).toString("utf8"), stderr: readFileSync(stderr, "utf8") });
}
process.stdout.write(JSON.stringify(outcomes));
`)
	node.Stdin = bytes.NewReader(input)
	out, err := node.Output()
	if err != nil {
		t.Fatalf("Pi pico3 bashTool: %v; output %s", err, out)
	}
	var want []bashOutcome
	if err := json.Unmarshal(out, &want); err != nil || len(want) != len(commands) {
		t.Fatalf("decode Pi outcomes %s: %v", out, err)
	}
	if !slices.ContainsFunc(want, func(outcome bashOutcome) bool { return outcome.Stderr != "" }) {
		t.Fatal("no command wrote stderr to the probe's file")
	}
	for i, command := range commands {
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
		got := bashOutcome{IsError: result.IsError, ExitCode: exitCode, Output: streamed.String(), Stderr: testenv.BashStderr(t, stderr)}
		if got != want[i] {
			t.Errorf("command %q\n got %+v\nwant %+v", command, got, want[i])
		}
	}
}
