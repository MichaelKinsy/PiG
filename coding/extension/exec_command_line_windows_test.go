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
	"github.com/MichaelKinsy/PiG/internal/shellconfig"
)

// Pi's execCommand (core/exec.ts) spawns with shell: false, so an extension's
// exec("bash", ["-c", command]) gives Git Bash libuv's command line, which
// bash parses with MSYS2 rules. A BASH_ENV script prints
// BASH_EXECUTION_STRING, the -c argument as bash received it, before the
// command runs.
func TestExecCommandPassesArgumentsToGitBashAsPiDoes(t *testing.T) {
	shell, err := shellconfig.Default()
	if err != nil {
		t.Fatal(err)
	}
	probe := filepath.Join(t.TempDir(), "execution-string.sh")
	if err := os.WriteFile(probe, []byte("printf '<%s>\\n' \"$BASH_EXECUTION_STRING\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BASH_ENV", probe)
	commands := []string{`a"b`, `"`, `x\"y`, `a\b\`, `echo"x"y`, `echo "x y"`, `echo a\"b trailing\`, `printf '[%s]' "" ''`, "printf '%s|' \"tab\there\"", ``}
	cwd := t.TempDir()
	root, err := filepath.EvalSymlinks("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent")
	if err != nil {
		t.Fatal(err)
	}
	input, err := json.Marshal(map[string]any{"module": filepath.Join(root, "dist", "core", "exec.js"), "shell": shell.Path, "cwd": cwd, "commands": commands})
	if err != nil {
		t.Fatal(err)
	}
	node := exec.CommandContext(t.Context(), "node", "--input-type=module", "-e", `
import { readFileSync } from "node:fs";
import { pathToFileURL } from "node:url";
const { module, shell, cwd, commands } = JSON.parse(readFileSync(0, "utf8"));
const { execCommand } = await import(pathToFileURL(module).href);
const results = [];
for (const command of commands) results.push(await execCommand(shell, ["-c", command], cwd));
process.stdout.write(JSON.stringify(results));
`)
	node.Stdin = bytes.NewReader(input)
	out, err := node.Output()
	if err != nil {
		t.Fatalf("Pi execCommand: %v; output %s", err, out)
	}
	var want []extension.ExecResult
	if err := json.Unmarshal(out, &want); err != nil || len(want) != len(commands) {
		t.Fatalf("decode Pi results %s: %v", out, err)
	}
	for i, command := range commands {
		got, err := extension.ExecCommand(context.Background(), cwd, shell.Path, []string{"-c", command}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if got != want[i] {
			t.Errorf("command %q\n got %+v\nwant %+v", command, got, want[i])
		}
	}
}
