//go:build linux

package nodespawn

import (
	"bytes"
	"errors"
	"os/exec"
	"testing"
)

// The block that libuv passes to execve keeps every property, in order, whether
// or not names share the text before their first "=". The initial environment
// of the shell, which /proc shows unchanged, is that block.
func TestSetEnvPropertiesPassesTheBlockNodePasses(t *testing.T) {
	nodeBinary, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("NODE_V8_COVERAGE", "")
	for _, c := range []struct {
		name string
		env  []EnvProperty
	}{
		{"names share their text", []EnvProperty{{"PATH", "/usr/bin:/bin"}, {"PIG_A", "one"}, {"PIG_A=B", "two"}, {"PIG_A=B=C", "three"}, {"PIG_Z", "z"}}},
		{"the longer name comes first", []EnvProperty{{"PATH", "/usr/bin:/bin"}, {"PIG_A=B", "two"}, {"PIG_A", "one"}}},
		{"no name shares its text", []EnvProperty{{"PATH", "/usr/bin:/bin"}, {"PIG_A=B", "two"}, {"PIG_C", "c"}}},
	} {
		t.Run(c.name, func(t *testing.T) {
			node := exec.CommandContext(t.Context(), nodeBinary, "-e", `
const { spawnSync } = require("node:child_process");
const env = JSON.parse(process.argv[1], (key, value) => value);
const result = spawnSync("/bin/sh", ["-c", "cat /proc/$$/environ"], { env });
if (result.error || result.status !== 0) throw result.error ?? new Error(String(result.stderr));
process.stdout.write(result.stdout);
`, orderedObject(t, c.env))
			want, err := node.Output()
			if err != nil {
				t.Fatalf("node spawn: %v", err)
			}
			cmd := exec.CommandContext(t.Context(), "/bin/sh", "-c", "cat /proc/$$/environ")
			SetEnvProperties(cmd, c.env)
			SetProgram(cmd)
			got, err := cmd.Output()
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, want) {
				t.Errorf("environment block\n got %q\nwant %q", got, want)
			}
		})
	}
}

// A child that starts through the trampoline keeps its arguments, which need
// not be UTF-8, its working directory, its standard streams, and its exit
// status, and finds its executable through the PATH of its environment.
func TestSetEnvPropertiesTrampolineKeepsTheSpawn(t *testing.T) {
	dir := t.TempDir()
	run := func(env []EnvProperty) (string, string, int) {
		cmd := exec.CommandContext(t.Context(), "sh", "-c", `printf '%s|%s|%s|' "$0" "$1" "$PWD"; cat; printf err >&2; exit 7`, "arg\xff0", "\xfe b\n")
		cmd.Dir = dir
		cmd.Stdin = bytes.NewReader([]byte("in"))
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		SetEnvProperties(cmd, env)
		SetProgram(cmd)
		err := cmd.Run()
		exitErr, ok := errors.AsType[*exec.ExitError](err)
		if !ok {
			t.Fatalf("Run error = %v", err)
		}
		return stdout.String(), stderr.String(), exitErr.ExitCode()
	}
	path := EnvProperty{"PATH", "/usr/bin:/bin"}
	plainOut, plainErr, plainCode := run([]EnvProperty{path, {"PWD", dir}})
	out, errOut, code := run([]EnvProperty{path, {"PWD", dir}, {"PIG_A", "one"}, {"PIG_A=B", "two"}})
	if out != plainOut || errOut != plainErr || code != plainCode || code != 7 {
		t.Errorf("through the trampoline: %q, %q, %d\nwithout it: %q, %q, %d", out, errOut, code, plainOut, plainErr, plainCode)
	}
}
