//go:build linux

package cli

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// Public issue #360: an extension's ctx.Mode() read "print" in the interactive TUI because the host never told the extension the run mode. Pi's ctx.mode is "tui" in the interactive UI and "rpc", "json" and "print" in those runs. These tests run the real binary in every mode with a real Go, Python and Rust extension and the Node fixture, each reporting its ctx mode from session_start.

const goModeProbeSource = `package goprobe

import (
	"os"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

func Extension() *sdk.Extension {
	ext := sdk.New("goprobe")
	ext.OnSessionStart(func(ctx sdk.Context, _ map[string]any) (any, error) {
		f, err := os.OpenFile(os.Getenv("MODE_REPORT"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		_, err = f.WriteString("go=" + ctx.Mode() + "\n")
		return nil, err
	})
	return ext
}
`

const pyModeProbeSource = `import os
import sys

sys.path.insert(0, os.environ["PIG_SDK_PY_ROOT"])
import pig_sdk


def new_extension() -> pig_sdk.Extension:
    ext = pig_sdk.Extension("pyprobe")

    def start(ctx, _event):
        with open(os.environ["MODE_REPORT"], "a") as report:
            report.write("py=" + ctx.mode + "\n")

    ext.on_event("session_start", start)
    return ext
`

const rsModeProbeSource = `use pig_sdk::Extension;
use std::io::Write;

pub fn new_extension() -> Extension {
    let mut ext = Extension::new("rsprobe");
    ext.on_event("session_start", |ctx, _data| {
        let mut report = std::fs::OpenOptions::new().append(true).create(true).open(std::env::var("MODE_REPORT").unwrap()).unwrap();
        let _ = writeln!(report, "rs={}", ctx.mode());
        None
    });
    ext
}
`

const nodeModeProbeSource = `import { appendFileSync } from "node:fs";

export default function (pi) {
  pi.on("session_start", (_event, ctx) => {
    appendFileSync(process.env.MODE_REPORT, "node=" + ctx.mode + "\n");
  });
}
`

// writeModeProbes writes each language's probe extension and returns their paths.
func writeModeProbes(t *testing.T) []string {
	t.Helper()
	var paths []string
	for _, language := range []string{"go", "py", "rs", "node"} {
		root := filepath.Join(t.TempDir(), language+"probe")
		if err := os.MkdirAll(filepath.Join(root, "src"), 0o755); err != nil {
			t.Fatal(err)
		}
		write := func(name, content string) {
			if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		switch language {
		case "go":
			write("go.mod", fmt.Sprintf("module example.com/goprobe\n\ngo 1.26\n\nrequire github.com/MichaelKinsy/PiG/extensions/sdk v0.3.0\n\nreplace github.com/MichaelKinsy/PiG/extensions/sdk => %s\n", filepath.Join(fixtureSourceRoot, "extensions", "sdk")))
			write("extension.go", goModeProbeSource)
		case "py":
			write("pyprobe.py", pyModeProbeSource)
		case "rs":
			write("Cargo.toml", fmt.Sprintf("[package]\nname = \"rsprobe\"\nversion = \"0.1.0\"\nedition = \"2024\"\npublish = false\n\n[dependencies]\npig-sdk = { path = %q }\n", filepath.Join(fixtureSourceRoot, "extensions", "sdk-rs")))
			write("src/lib.rs", rsModeProbeSource)
		case "node":
			write("nodeprobe.mjs", nodeModeProbeSource)
			root = filepath.Join(root, "nodeprobe.mjs")
		}
		paths = append(paths, root)
	}
	return paths
}

func modeProbeEnv(t *testing.T, report string, extra ...string) []string {
	t.Helper()
	home := t.TempDir()
	return append(append(os.Environ(), "PIG_HOME="+filepath.Join(home, ".pig"), "MODE_REPORT="+report), extra...)
}

func readModeReport(t *testing.T, report string) []string {
	t.Helper()
	data, err := os.ReadFile(report)
	if err != nil {
		return nil
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	slices.Sort(lines)
	return lines
}

// TestExtensionCtxModeMatchesRunMode proves every SDK sees the run mode as Pi reports it in print, JSON and RPC mode.
func TestExtensionCtxModeMatchesRunMode(t *testing.T) {
	binary := buildPigBinaryForSignalTest(t)
	probes := writeModeProbes(t)
	pigHome := t.TempDir()
	for _, mode := range []string{"print", "json", "rpc"} {
		t.Run(mode, func(t *testing.T) {
			report := filepath.Join(t.TempDir(), "report")
			args := []string{"--no-extensions", "--no-skills", "--no-prompt-templates", "--session-dir", t.TempDir()}
			for _, p := range probes {
				args = append(args, "-e", p)
			}
			if mode == "print" {
				args = append(args, "--print")
			} else {
				args = append(args, "--mode", mode)
			}
			cmd := exec.CommandContext(t.Context(), binary, args...)
			cmd.Dir = t.TempDir()
			cmd.Env = append(modeProbeEnv(t, report, "PIG_CODING_AGENT_DIR="+seededAgentDir(t)), "PIG_HOME="+pigHome)
			cmd.Stdin = strings.NewReader("")
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("%s: %v\n%s", mode, err, out)
			}
			want := []string{"go=" + mode, "node=" + mode, "py=" + mode, "rs=" + mode}
			if got := readModeReport(t, report); strings.Join(got, ",") != strings.Join(want, ",") {
				t.Fatalf("ctx modes = %v, want %v", got, want)
			}
		})
	}
}

// TestExtensionCtxModeIsTUIInInteractiveMode proves every SDK sees "tui" in the interactive TUI, not the SDK's "print" default.
func TestExtensionCtxModeIsTUIInInteractiveMode(t *testing.T) {
	binary := buildPigBinaryForSignalTest(t)
	probes := writeModeProbes(t)
	report := filepath.Join(t.TempDir(), "report")
	master, slave := openPTY(t, 30, 128)
	defer func() { _ = master.Close() }()
	args := []string{"--no-extensions", "--model", "test-faux/faux-1", "--tui-mode", "regular"}
	for _, p := range probes {
		args = append(args, "-e", p)
	}
	cmd := exec.Command(binary, args...)
	cmd.Dir = t.TempDir()
	cmd.Env = append(modeProbeEnv(t, report, "PIG_CODING_AGENT_DIR="+seededAgentDir(t), "PIG_TEST_FAUX=1", "PIG_TEST_FAUX_SCENARIO=parity-basic", "TERM=xterm-256color"))
	cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start pig: %v", err)
	}
	_ = slave.Close()
	exited := make(chan struct{})
	go func() { _ = cmd.Wait(); close(exited) }()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		<-exited
	})
	output := &ptyOutput{}
	go func() { _, _ = io.Copy(output, master) }()
	want := "go=tui,node=tui,py=tui,rs=tui"
	deadline := time.Now().Add(testbudget.Wait(t))
	for time.Now().Before(deadline) {
		got := readModeReport(t, report)
		if len(got) >= 4 {
			if strings.Join(got, ",") != want {
				t.Fatalf("ctx modes = %v, want %s", got, want)
			}
			return
		}
		select {
		case <-exited:
			t.Fatalf("pig exited early; ctx modes = %v; output: %q", got, output.since(0))
		default:
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("extensions never reported; ctx modes = %v", readModeReport(t, report))
}
