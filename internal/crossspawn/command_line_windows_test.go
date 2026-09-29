//go:build windows

package crossspawn

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// commandLineHelper makes the test binary report the command line
// CreateProcessW gave it, before any C runtime parsing.
const commandLineHelper = "PIG_CROSSSPAWN_COMMAND_LINE_HELPER"

func receivedCommandLine(t *testing.T, cmd *exec.Cmd) string {
	t.Helper()
	cmd.Env = append(os.Environ(), commandLineHelper+"=1", "TOKEN=EXPANDED")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("%v: %v; output %s", cmd.Args, err, out)
	}
	var line string
	if err := json.Unmarshal(out, &line); err != nil {
		t.Fatalf("decode %q: %v", out, err)
	}
	return line
}

// nodeCommandLines runs each request through the pinned cross-spawn 7.0.6 (or,
// for a shell request, Node's spawn with shell: true) and returns the command
// line the receiver got.
func nodeCommandLines(t *testing.T, cwd string, requests []map[string]any) []string {
	t.Helper()
	input, err := json.Marshal(map[string]any{
		"crossSpawn": filepath.Join("..", "..", "coding", "extension", "host", "subprocess", "runtime-node", "shims", "cross-spawn"),
		"cwd":        cwd,
		"helper":     commandLineHelper,
		"requests":   requests,
	})
	if err != nil {
		t.Fatal(err)
	}
	node := exec.CommandContext(t.Context(), "node", "-e", `
const { spawnSync } = require("node:child_process");
const path = require("node:path");
const input = JSON.parse(require("node:fs").readFileSync(0, "utf8"));
const crossSpawn = require(path.resolve(input.crossSpawn));
const env = { ...process.env, [input.helper]: "1", TOKEN: "EXPANDED" };
const lines = input.requests.map(({ name, args, shell }) => {
	const options = { cwd: input.cwd, env, encoding: "utf8", windowsHide: true };
	const result = shell ? spawnSync(name, { ...options, shell: true }) : crossSpawn.sync(name, args, options);
	if (result.error || result.status !== 0) throw result.error ?? new Error(result.stderr);
	return JSON.parse(result.stdout);
});
process.stdout.write(JSON.stringify(lines));
`)
	node.Stdin = bytes.NewReader(input)
	out, err := node.Output()
	if err != nil {
		t.Fatalf("node: %v; output %s", err, out)
	}
	var lines []string
	if err := json.Unmarshal(out, &lines); err != nil || len(lines) != len(requests) {
		t.Fatalf("decode node lines %s: %v", out, err)
	}
	return lines
}

// cross-spawn hands an executable, including a shebang interpreter, to Node's
// spawn without windowsVerbatimArguments, and Node's shell: true runs a
// non-cmd ComSpec with ["-c", command] the same way. The receiver must get
// libuv's command line byte for byte: an MSYS2 or Cygwin receiver parses it
// with its own rules. ComSpec names the receiver, so for a .cmd shim it
// reports the escaped line cross-spawn gives cmd.exe.
func TestWindowsCommandLineMatchesNodeCrossSpawn(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(t.TempDir(), "cwd & space")
	receiver := filepath.Join(root, "bin", "receiver.exe")
	writeExecutable(t, receiver, string(data))
	shims := []string{filepath.Join("bin", "shim&(data)!%TOKEN%^x.cmd"), filepath.Join("bin", "shim & data.cmd")}
	for _, shim := range shims {
		writeExecutable(t, filepath.Join(root, shim), "@\"%~dp0receiver.exe\" %*\r\n")
	}
	writeExecutable(t, filepath.Join(root, "source & data.cmd"), "#!/usr/bin/env receiver.exe\r\n@echo INJECTED\r\n")
	t.Setenv("PATH", filepath.Dir(receiver)+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("ComSpec", exe)

	args := []string{`a"b`, `"`, `x\"y`, `trailing\`, ``, `a b`, "tab\there", `quote"value\`, `%TOKEN%`, `a\\"b c`}
	names := append([]string{receiver, "receiver.exe", filepath.Join(root, "source & data.cmd")}, shims...)
	sources := []string{`a"b`, `editor "file & name" %TOKEN%`}
	var requests []map[string]any
	for _, name := range names {
		requests = append(requests, map[string]any{"name": name, "args": args})
	}
	for _, source := range sources {
		requests = append(requests, map[string]any{"name": source, "shell": true})
	}
	want := nodeCommandLines(t, root, requests)

	for i, name := range names {
		if got := receivedCommandLine(t, Command(t.Context(), root, name, args...)); got != want[i] {
			t.Errorf("Command(%q)\n got %q\nwant %q", name, got, want[i])
		}
	}
	for i, source := range sources {
		if got := receivedCommandLine(t, ShellCommand(t.Context(), source)); got != want[len(names)+i] {
			t.Errorf("ShellCommand(%q)\n got %q\nwant %q", source, got, want[len(names)+i])
		}
	}
}
