//go:build windows

package nodespawn

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"

	"golang.org/x/sys/windows"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// libuv's uv_spawn (src/win/process.c) sets STARTF_USESHOWWINDOW and SW_HIDE
// for UV_PROCESS_WINDOWS_HIDE, and CREATE_NO_WINDOW only when none of the
// options->stdio_count entries is UV_INHERIT_FD. HideWindow keeps the other
// creation flags.
func TestHideWindowFollowsLibuvStdioRule(t *testing.T) {
	cases := []struct {
		name     string
		stdio    []Stdio
		noWindow bool
	}{
		{"ignore pipe pipe", []Stdio{Ignore, Pipe, Pipe}, true},
		{"pipe pipe ignore", []Stdio{Pipe, Pipe, Ignore}, true},
		{"ignore ignore ignore", []Stdio{Ignore, Ignore, Ignore}, true},
		{"inherited stdin", []Stdio{Inherit, Pipe, Pipe}, false},
		{"inherited stderr", []Stdio{Ignore, Pipe, Inherit}, false},
		{"inherited everything", []Stdio{Inherit, Inherit, Inherit}, false},
		{"no stdio entries", nil, false},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			cmd := exec.Command("child.exe")
			cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_PROCESS_GROUP | windows.DETACHED_PROCESS}
			HideWindow(cmd, testCase.stdio...)
			want := uint32(windows.CREATE_NEW_PROCESS_GROUP | windows.DETACHED_PROCESS)
			if testCase.noWindow {
				want |= windows.CREATE_NO_WINDOW
			}
			if !cmd.SysProcAttr.HideWindow || cmd.SysProcAttr.CreationFlags != want {
				t.Fatalf("SysProcAttr = %+v, want HideWindow and CreationFlags %#x", cmd.SysProcAttr, want)
			}
		})
	}
	cmd := exec.Command("child.exe")
	HideWindow(cmd, Ignore, Pipe, Pipe)
	if cmd.SysProcAttr == nil || !cmd.SysProcAttr.HideWindow || cmd.SysProcAttr.CreationFlags != windows.CREATE_NO_WINDOW {
		t.Fatalf("SysProcAttr without prior attributes = %+v", cmd.SysProcAttr)
	}
}

// Node's spawn with windowsHide: true is the oracle for every stdio
// configuration that inherits no parent handle, the only kind Pi passes with
// windowsHide. The child reports its STARTUPINFO show state and whether it
// has a console of its own, so a spawn without CREATE_NO_WINDOW differs
// whether or not the test process has a console.
func TestHideWindowMatchesNodeWindowsHide(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	configurations := [][]string{{"ignore", "pipe", "pipe"}, {"pipe", "pipe", "ignore"}, {"ignore", "pipe", "ignore"}}
	input, err := json.Marshal(map[string]any{"file": exe, "configurations": configurations, "helper": testenv.StartupHelper})
	if err != nil {
		t.Fatal(err)
	}
	node := exec.CommandContext(t.Context(), "node", "-e", `
const { spawnSync } = require("node:child_process");
const { file, configurations, helper } = JSON.parse(require("node:fs").readFileSync(0, "utf8"));
const reports = [];
for (const stdio of configurations) {
	const result = spawnSync(file, [], { env: { ...process.env, [helper]: "1" }, stdio, encoding: "utf8", windowsHide: true });
	if (result.error || result.status !== 0) {
		process.stderr.write(String(result.error ?? result.stderr));
		process.exit(1);
	}
	reports.push(JSON.parse(result.stdout));
}
process.stdout.write(JSON.stringify(reports));
`)
	node.Stdin = bytes.NewReader(input)
	out, err := node.Output()
	if err != nil {
		t.Fatalf("node spawn: %v; output %s", err, out)
	}
	var want []testenv.Startup
	if err := json.Unmarshal(out, &want); err != nil || len(want) != len(configurations) {
		t.Fatalf("decode node reports %q: %v", out, err)
	}
	for i, configuration := range configurations {
		cmd := exec.CommandContext(t.Context(), exe)
		cmd.Env = append(os.Environ(), testenv.StartupHelper+"=1")
		stdio := make([]Stdio, len(configuration))
		for j, kind := range configuration {
			if kind == "pipe" {
				stdio[j] = Pipe
			}
		}
		if configuration[0] == "pipe" {
			cmd.Stdin = strings.NewReader("")
		}
		if configuration[2] == "pipe" {
			cmd.Stderr = new(bytes.Buffer)
		}
		HideWindow(cmd, stdio...)
		output, err := cmd.Output()
		if err != nil {
			t.Fatalf("%v: %v", configuration, err)
		}
		var got testenv.Startup
		if err := json.Unmarshal(output, &got); err != nil {
			t.Fatalf("decode %q: %v", output, err)
		}
		if got != want[i] {
			t.Errorf("stdio %v: child started with %+v, want Node's %+v", configuration, got, want[i])
		}
	}
}
