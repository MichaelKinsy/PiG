package tools

// pi: packages/coding-agent/src/core/tools/bash.ts

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// stdioProbe is a command run by a shell that takes it in argv ("") or on stdin ("stdin"), as a ShellConfig's CommandTransport says.
type stdioProbe struct {
	Transport string `json:"transport"`
	Command   string `json:"command"`
}

// piStdioOutputs runs each probe through Pi's createLocalShellOperations (core/tools/bash.ts) over the shell and returns what the command wrote.
func piStdioOutputs(t *testing.T, shell string, probes []stdioProbe) []string {
	t.Helper()
	root, err := filepath.EvalSymlinks("../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent")
	if err != nil {
		t.Skipf("skipped: no Pi oracle (%v)", err)
	}
	input, err := json.Marshal(map[string]any{"module": filepath.Join(root, "dist", "core", "tools", "bash.js"), "shell": shell, "cwd": t.TempDir(), "probes": probes})
	if err != nil {
		t.Fatal(err)
	}
	node := exec.CommandContext(t.Context(), "node", "--input-type=module", "-e", `
import { readFileSync } from "node:fs";
import { pathToFileURL } from "node:url";
const { module, shell, cwd, probes } = JSON.parse(readFileSync(0, "utf8"));
const { createLocalShellOperations } = await import(pathToFileURL(module).href);
const outputs = [];
for (const probe of probes) {
	const stdin = probe.transport === "stdin";
	const operations = createLocalShellOperations("bash", () => ({ shell, args: [stdin ? "-s" : "-c"], ...(stdin ? { commandTransport: "stdin" } : {}) }));
	const chunks = [];
	const result = await operations.exec(probe.command, cwd, { onData: (chunk) => chunks.push(Buffer.from(chunk)) });
	if (result.exitCode !== 0) throw new Error("exit code " + result.exitCode);
	outputs.push(Buffer.concat(chunks).toString("utf8"));
}
process.stdout.write(JSON.stringify(outputs));
`)
	var stderr bytes.Buffer
	node.Stdin = bytes.NewReader(input)
	node.Stderr = &stderr
	out, err := node.Output()
	if err != nil {
		t.Fatalf("Pi createLocalShellOperations: %v\n%s", err, &stderr)
	}
	var outputs []string
	if err := json.Unmarshal(out, &outputs); err != nil || len(outputs) != len(probes) {
		t.Fatalf("decode Pi outputs %q: %v", out, err)
	}
	return outputs
}

// D631: what a command of the bash tool sees of its standard streams. Pi's child gets sockets for stdout and stderr (libuv's socket pair, which a socket refuses to open
// again through /dev/stdout: ENXIO) and, for a shell that takes the command on stdin, one for stdin; the bash tool gives it pipes. With the command in argv, stdin is
// /dev/null on both sides. Each cell is run through Pi and through the bash tool, and both columns are the record's table; a cell that differs from it fails.
func TestBashToolStdioKindsAgainstPi(t *testing.T) {
	shell, err := exec.LookPath("bash")
	if err != nil {
		t.Skipf("skipped: no bash (%v)", err)
	}
	const (
		reopen = `{ echo x > /dev/stdout; } 2>/dev/null; echo "rc=$?"`
		fds    = `for fd in 0 1 2; do stat -L -c "$fd %F" /proc/self/fd/$fd; done`
		stdin  = `if test -p /dev/stdin; then echo fifo; elif test -S /dev/stdin; then echo socket; elif test -c /dev/stdin; then echo char; else echo other; fi`
	)
	cells := []struct {
		name      string
		transport string
		command   string
		pi, pig   string
	}{
		{"argv: > /dev/stdout", "", reopen, "rc=1\n", "x\nrc=0\n"},
		{"argv: kind of the streams", "", fds, "0 character special file\n1 socket\n2 socket\n", "0 character special file\n1 fifo\n2 fifo\n"},
		{"argv: kind of stdin", "", stdin, "char\n", "char\n"},
		{"stdin: > /dev/stdout", "stdin", reopen, "rc=1\n", "x\nrc=0\n"},
		{"stdin: kind of the streams", "stdin", fds, "0 socket\n1 socket\n2 socket\n", "0 fifo\n1 fifo\n2 fifo\n"},
		{"stdin: kind of stdin", "stdin", stdin, "socket\n", "fifo\n"},
	}
	probes := make([]stdioProbe, len(cells))
	for i, cell := range cells {
		probes[i] = stdioProbe{Transport: cell.transport, Command: cell.command}
	}
	pi := piStdioOutputs(t, shell, probes)
	for i, cell := range cells {
		t.Run(cell.name, func(t *testing.T) {
			if pi[i] != cell.pi {
				t.Errorf("Pi wrote %q, the record says %q", pi[i], cell.pi)
			}
			config := ShellConfig{Path: shell, Args: []string{"-c"}}
			if cell.transport == "stdin" {
				config = ShellConfig{Path: shell, Args: []string{"-s"}, CommandTransport: "stdin"}
			}
			operations := &LocalShellOperations{ShellName: "bash", ResolveShell: func() (ShellConfig, error) { return config, nil }}
			var (
				mu  sync.Mutex
				out strings.Builder
			)
			result, err := operations.Exec(t.Context(), cell.command, t.TempDir(), BashOperationsExecOptions{OnData: func(data []byte) {
				mu.Lock()
				defer mu.Unlock()
				out.Write(data)
			}})
			if err != nil || result.ExitCode == nil || *result.ExitCode != 0 {
				t.Fatalf("PiG exec = %+v, %v", result, err)
			}
			mu.Lock()
			defer mu.Unlock()
			if out.String() != cell.pig {
				t.Errorf("the bash tool wrote %q, the record says %q", out.String(), cell.pig)
			}
		})
	}
}
