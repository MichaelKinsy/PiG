//go:build windows

package tools

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// Pi's createLocalShellOperations (core/tools/bash.ts) spawns the shell with
// windowsHide: true and stdio that inherits nothing, so libuv starts it with
// SW_HIDE and CREATE_NO_WINDOW. The test binary stands in for the shell under
// both command transports and reports how Pi's operations and PiG's started
// it.
func TestLocalShellOperationsHideTheShellWindowAsPiDoes(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(testenv.StartupHelper, "1")
	cwd := t.TempDir()
	root, err := filepath.EvalSymlinks("../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent")
	if err != nil {
		t.Fatal(err)
	}
	transports := []string{"", "stdin"}
	input, err := json.Marshal(map[string]any{"module": filepath.Join(root, "dist", "core", "tools", "bash.js"), "shell": exe, "cwd": cwd, "transports": transports})
	if err != nil {
		t.Fatal(err)
	}
	node := exec.CommandContext(t.Context(), "node", "--input-type=module", "-e", `
import { readFileSync } from "node:fs";
import { pathToFileURL } from "node:url";
const { module, shell, cwd, transports } = JSON.parse(readFileSync(0, "utf8"));
const { createLocalShellOperations } = await import(pathToFileURL(module).href);
const reports = [];
for (const commandTransport of transports) {
	const operations = createLocalShellOperations("bash", () => ({ shell, args: [], ...(commandTransport ? { commandTransport } : {}) }));
	const chunks = [];
	const result = await operations.exec("report", cwd, { onData: (chunk) => chunks.push(Buffer.from(chunk)) });
	if (result.exitCode !== 0) throw new Error("exit code " + result.exitCode);
	reports.push(JSON.parse(Buffer.concat(chunks).toString("utf8")));
}
process.stdout.write(JSON.stringify(reports));
`)
	node.Stdin = bytes.NewReader(input)
	out, err := node.Output()
	if err != nil {
		t.Fatalf("Pi createLocalShellOperations: %v; output %s", err, out)
	}
	var want []testenv.Startup
	if err := json.Unmarshal(out, &want); err != nil || len(want) != len(transports) {
		t.Fatalf("decode Pi reports %q: %v", out, err)
	}
	for i, transport := range transports {
		operations := &LocalShellOperations{ShellName: "bash", ResolveShell: func() (ShellConfig, error) {
			return ShellConfig{Path: exe, CommandTransport: transport}, nil
		}}
		var (
			mu     sync.Mutex
			output bytes.Buffer
		)
		result, err := operations.Exec(t.Context(), "report", cwd, BashOperationsExecOptions{OnData: func(data []byte) {
			mu.Lock()
			defer mu.Unlock()
			output.Write(data)
		}})
		if err != nil || result.ExitCode == nil || *result.ExitCode != 0 {
			t.Fatalf("transport %q: result %+v, err %v, output %q", transport, result, err, output.String())
		}
		var got testenv.Startup
		if err := json.Unmarshal(output.Bytes(), &got); err != nil {
			t.Fatalf("transport %q: decode %q: %v", transport, output.String(), err)
		}
		if got != want[i] {
			t.Errorf("transport %q: shell started with %+v, want Pi's %+v", transport, got, want[i])
		}
	}
}
