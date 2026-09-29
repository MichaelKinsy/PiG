//go:build windows

package env

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"

	"golang.org/x/sys/windows"

	"github.com/MichaelKinsy/PiG/agent/harness"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// Pi's NodeExecutionEnv.exec (harness/env/nodejs.ts) spawns the shell with
// windowsHide: true and stdio that inherits nothing, so libuv starts it with
// SW_HIDE and CREATE_NO_WINDOW. The test binary stands in for the configured
// shell and reports how Pi's environment and PiG's started it.
func TestExecHidesTheShellWindowAsPiDoes(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cwd := t.TempDir()
	overrides := map[string]string{testenv.StartupHelper: "1"}
	root, err := filepath.EvalSymlinks("../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/node_modules/@earendil-works/pi-agent-core")
	if err != nil {
		t.Fatal(err)
	}
	input, err := json.Marshal(map[string]any{"root": root, "cwd": cwd, "shell": exe, "env": overrides})
	if err != nil {
		t.Fatal(err)
	}
	node := exec.CommandContext(t.Context(), "node", "--input-type=module", "-e", `
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { pathToFileURL } from "node:url";
const { root, cwd, shell, env } = JSON.parse(readFileSync(0, "utf8"));
const load = (path) => import(pathToFileURL(join(root, "dist", ...path)).href);
const { NodeExecutionEnv } = await load(["harness", "env", "nodejs.js"]);
const { applyShellOutputUpdate } = await load(["harness", "utils", "output-capture.js"]);
let output;
const result = await new NodeExecutionEnv({ cwd, shellPath: shell }).exec("report", { env, onUpdate: (update) => { output = applyShellOutputUpdate(output, update); } }, {});
if (!result.ok) throw result.error;
if (result.value.exitCode !== 0) throw new Error("exit code " + result.value.exitCode);
process.stdout.write(output?.text ?? "");
`)
	node.Stdin = bytes.NewReader(input)
	out, err := node.Output()
	if err != nil {
		t.Fatalf("Pi NodeExecutionEnv: %v; output %s", err, out)
	}
	var want testenv.Startup
	if err := json.Unmarshal(out, &want); err != nil {
		t.Fatalf("decode Pi report %q: %v", out, err)
	}
	shell := NewNodeExecutionEnv(NodeExecutionEnvOptions{Cwd: cwd, ShellPath: exe})
	result, collected, err := execCollect(context.Background(), shell, "report", &harness.ShellExecOptions{Env: overrides})
	mustDo(t, err)
	if result.ExitCode != 0 {
		t.Fatalf("exit code %d, output %q", result.ExitCode, collected.text())
	}
	var got testenv.Startup
	if err := json.Unmarshal([]byte(collected.text()), &got); err != nil {
		t.Fatalf("decode %q: %v", collected.text(), err)
	}
	if got != want {
		t.Errorf("shell started with %+v, want Pi's %+v", got, want)
	}
}

// Pi's killProcessTree (harness/env/nodejs.ts) spawns System32 taskkill with
// stdio "ignore", detached: true, and windowsHide: true, for which libuv
// passes DETACHED_PROCESS, CREATE_NEW_PROCESS_GROUP, CREATE_NO_WINDOW, and
// SW_HIDE.
func TestTaskkillSpawnOptions(t *testing.T) {
	t.Setenv("SystemRoot", `C:\CustomWindows`)
	command := taskkillCommand(1234)
	wantPath := filepath.Join(`C:\CustomWindows`, "System32", "taskkill.exe")
	if command.Path != wantPath || !slices.Equal(command.Args, []string{wantPath, "/F", "/T", "/PID", "1234"}) {
		t.Fatalf("command = %q %q", command.Path, command.Args)
	}
	if command.Stdin != nil || command.Stdout != nil || command.Stderr != nil {
		t.Fatal("stdio is not ignored")
	}
	if command.SysProcAttr == nil || !command.SysProcAttr.HideWindow || command.SysProcAttr.CreationFlags != (windows.CREATE_NEW_PROCESS_GROUP|windows.DETACHED_PROCESS|windows.CREATE_NO_WINDOW) {
		t.Fatalf("spawn attributes = %+v", command.SysProcAttr)
	}
}

// Pi's killProcessTree joins process.env.SystemRoot ?? "C:\\Windows"
// (harness/env/nodejs.ts:265): ?? replaces only an unset SystemRoot, so an
// empty one yields the relative System32\taskkill.exe.
func TestTaskkillSystemRootUsesNullishDefault(t *testing.T) {
	t.Setenv("SystemRoot", "")
	if got := taskkillCommand(1).Path; got != filepath.Join("System32", "taskkill.exe") {
		t.Errorf("empty SystemRoot: path %q", got)
	}
	if err := os.Unsetenv("SystemRoot"); err != nil {
		t.Fatal(err)
	}
	if got := taskkillCommand(1).Path; got != filepath.Join(`C:\Windows`, "System32", "taskkill.exe") {
		t.Errorf("unset SystemRoot: path %q", got)
	}
}
