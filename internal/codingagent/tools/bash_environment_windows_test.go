//go:build windows

package tools

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// Pi's createLocalShellOperations (core/tools/bash.ts:96-102) spawns the shell
// with env ?? getShellEnv(), where getShellEnv (utils/shell.ts:138) is
// {...process.env} with the agent bin directory prepended to the first
// PATH-like key. process.env skips Windows' hidden "=" names such as
// "=ExitCode" (src/node_env_var.cc); Node's normalizeSpawnArguments sorts the
// env names and keeps the first of names that differ only in case, and libuv
// adds the required variables an env lacks (src/win/process.c). Node keeps
// both "PIG_K" and "PIG_K" (KELVIN SIGN), whose toUpperCase values
// differ, though os/exec's lowercasing would merge them. The test binary
// stands in for the shell and prints the environment it received; Pi's own
// operations are the oracle.
func TestLocalShellOperationsGiveTheShellPisEnvironment(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	agentDir := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", agentDir)
	t.Setenv(testenv.EnvironHelper, "1")
	cwd := t.TempDir()
	// A spawn hook's env: names that differ only in case and no system
	// variables.
	hookEnv := []string{testenv.EnvironHelper + "=1", "Path=/pig-mixed", "PATH=/pig-upper", "path=/pig-lower", "pig_b=2", "PIG_A=1"}
	kelvinEnv := []string{testenv.EnvironHelper + "=1", "PIG_K=letter", "PIG_K=kelvin"}
	envs := [][]string{nil, hookEnv, kelvinEnv}
	root, err := filepath.EvalSymlinks("../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent")
	if err != nil {
		t.Fatal(err)
	}
	objects := make([]map[string]string, len(envs))
	for i, env := range envs {
		if env == nil {
			continue
		}
		objects[i] = map[string]string{}
		for _, pair := range env {
			name, value, _ := strings.Cut(pair, "=")
			objects[i][name] = value
		}
	}
	input, err := json.Marshal(map[string]any{"module": filepath.Join(root, "dist", "core", "tools", "bash.js"), "shell": exe, "cwd": cwd, "envs": objects})
	if err != nil {
		t.Fatal(err)
	}
	node := exec.CommandContext(t.Context(), "node", "--input-type=module", "-e", `
import { readFileSync } from "node:fs";
import { pathToFileURL } from "node:url";
const { module, shell, cwd, envs } = JSON.parse(readFileSync(0, "utf8"));
const { createLocalShellOperations } = await import(pathToFileURL(module).href);
const reports = [];
for (const env of envs) {
	const operations = createLocalShellOperations("bash", () => ({ shell, args: [] }));
	const chunks = [];
	const result = await operations.exec("report", cwd, { onData: (chunk) => chunks.push(Buffer.from(chunk)), env: env ?? undefined });
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
	var want [][]string
	if err := json.Unmarshal(out, &want); err != nil || len(want) != len(envs) {
		t.Fatalf("decode Pi reports %q: %v", out, err)
	}
	operations := &LocalShellOperations{ShellName: "bash", BinDir: filepath.Join(agentDir, "bin"), ResolveShell: func() (ShellConfig, error) {
		return ShellConfig{Path: exe}, nil
	}}
	for i, env := range envs {
		var (
			mu     sync.Mutex
			output bytes.Buffer
		)
		result, err := operations.Exec(t.Context(), "report", cwd, BashOperationsExecOptions{Env: env, OnData: func(data []byte) {
			mu.Lock()
			defer mu.Unlock()
			output.Write(data)
		}})
		if err != nil || result.ExitCode == nil || *result.ExitCode != 0 {
			t.Fatalf("env %d: result %+v, err %v, output %q", i, result, err, output.String())
		}
		var got []string
		if err := json.Unmarshal(output.Bytes(), &got); err != nil {
			t.Fatalf("env %d: decode %q: %v", i, output.String(), err)
		}
		if !slices.Equal(got, want[i]) {
			t.Errorf("env %d: shell environment differs from Pi's\n got %q\nwant %q", i, got, want[i])
		}
	}
}
