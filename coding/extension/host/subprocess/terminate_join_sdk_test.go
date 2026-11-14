//go:build !pig_strip_node_extensions

package subprocess

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/toolchain"
)

// terminateJoinSDKRows runs one SDK in both placements: isolated gives the extension its own process, shared-ok packs it into a cell process. The extension's factory writes one line to stderr and one to stdout as the SDK writes them natively, so the lines are in the host's copy of the process output when TerminateProcesses runs against a terminal that is stalled.
func terminateJoinSDKRows(t *testing.T, config func(t *testing.T, name, isolation string) ExtConfig) {
	t.Helper()
	for _, isolation := range []string{"isolated", "shared-ok"} {
		placement := map[string]string{"isolated": "isolated", "shared-ok": "packed"}[isolation]
		t.Run(placement, func(t *testing.T) {
			name := "join-" + placement
			cfg := config(t, name, isolation)
			release, finish := stalledStderr(t)
			h := NewHost(t.TempDir())
			t.Cleanup(func() { h.Shutdown("test complete") })
			if _, errs := h.LoadAll(t.Context(), []ExtConfig{cfg}); len(errs) != 0 {
				t.Fatal(errs)
			}
			release(200 * time.Millisecond)
			h.TerminateProcesses()
			got := finish()
			for _, line := range []string{"stderr line of " + name + "\n", "stdout line of " + name + "\n"} {
				if !strings.Contains(got, line) {
					t.Errorf("terminal lacks %q after TerminateProcesses returned", line)
				}
			}
		})
	}
}

func TestTerminateProcessesDeliversTheOutputOfGoExtensions(t *testing.T) {
	terminateJoinSDKRows(t, func(t *testing.T, name, isolation string) ExtConfig {
		dir := t.TempDir()
		modulePath := "example.com/terminatejoin/" + isolation
		if err := os.WriteFile(filepath.Join(dir, "go.mod"), fmt.Appendf(nil, "module %s\n\ngo 1.26\n\nrequire github.com/MichaelKinsy/PiG/extensions/sdk v0.0.0\n", modulePath), 0o644); err != nil {
			t.Fatal(err)
		}
		source := `package ext

import (
	"os"

	"github.com/MichaelKinsy/PiG/extensions/sdk"
)

func Extension() *sdk.Extension {
	_, _ = os.Stderr.WriteString("stderr line of ` + name + `\n")
	_, _ = os.Stdout.WriteString("stdout line of ` + name + `\n")
	return sdk.New(` + strconv.Quote(name) + `)
}
`
		if err := os.WriteFile(filepath.Join(dir, "ext.go"), []byte(source), 0o644); err != nil {
			t.Fatal(err)
		}
		cfg := packedFactoryConfig(name, dir, modulePath, "terminate-join-go-"+isolation)
		cfg.Isolation = isolation
		return cfg
	})
}

func TestTerminateProcessesDeliversTheOutputOfPythonExtensions(t *testing.T) {
	python := toolchain.PythonExecutable(runtime.GOOS, exec.LookPath)
	if _, err := exec.LookPath(python); err != nil {
		t.Skipf("%s not found: %v", python, err)
	}
	terminateJoinSDKRows(t, func(t *testing.T, name, isolation string) ExtConfig {
		dir := t.TempDir()
		module := "terminate_join_py_" + strings.ReplaceAll(isolation, "-", "_")
		source := `import sys

import pig_sdk


def new_extension():
    sys.stderr.write("stderr line of ` + name + `\n")
    sys.stderr.flush()
    print("stdout line of ` + name + `", flush=True)
    return pig_sdk.Extension(` + strconv.Quote(name) + `)
`
		if err := os.WriteFile(filepath.Join(dir, module+".py"), []byte(source), 0o644); err != nil {
			t.Fatal(err)
		}
		cfg := packedPythonFactoryConfig(name, dir, module, "terminate-join-py-"+isolation)
		cfg.Isolation = isolation
		return cfg
	})
}

// Rust builds are slow: this test runs last by its name.
func TestTerminateProcessesDeliversTheOutputOfZRustExtensions(t *testing.T) {
	if _, err := exec.LookPath("cargo"); err != nil {
		t.Skipf("cargo not found: %v", err)
	}
	terminateJoinSDKRows(t, func(t *testing.T, name, isolation string) ExtConfig {
		dir := t.TempDir()
		sdkRoot, err := filepath.Abs(filepath.Join("..", "..", "..", "..", "extensions", "sdk-rs"))
		if err != nil {
			t.Fatal(err)
		}
		crate := "terminate_join_rs_" + strings.ReplaceAll(isolation, "-", "_")
		cargo := fmt.Sprintf("[package]\nname = %q\nversion = \"0.0.0\"\nedition = \"2024\"\n\n[dependencies]\npig-sdk = { path = %q }\nserde_json = \"1\"\n", crate, filepath.ToSlash(sdkRoot))
		if err := os.WriteFile(filepath.Join(dir, "Cargo.toml"), []byte(cargo), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(filepath.Join(dir, "src"), 0o755); err != nil {
			t.Fatal(err)
		}
		source := `use pig_sdk::Extension;
use std::io::Write;

pub fn new_extension() -> Extension {
    eprintln!("stderr line of ` + name + `");
    println!("stdout line of ` + name + `");
    let _ = std::io::stdout().flush();
    Extension::new(` + strconv.Quote(name) + `)
}
`
		if err := os.WriteFile(filepath.Join(dir, "src", "lib.rs"), []byte(source), 0o644); err != nil {
			t.Fatal(err)
		}
		cfg := packedRustFactoryConfig(name, dir, crate, "terminate-join-rs-"+isolation)
		cfg.Isolation = isolation
		return cfg
	})
}

// The extension's log holds what the extension wrote to stderr once TerminateProcesses returned: extensionConsole.Write puts a line in the log before it blocks on the terminal, and the log is the record that outlives a terminal that dropped the line.
func TestTerminateProcessesLeavesTheStderrOfNodeExtensionsInTheirLog(t *testing.T) {
	for _, packed := range []bool{false, true} {
		t.Run(map[bool]string{false: "isolated", true: "packed"}[packed], func(t *testing.T) {
			release, finish := stalledStderr(t)
			h := NewHost(t.TempDir())
			t.Cleanup(func() { h.Shutdown("test complete") })
			cfg := ExtConfig{Name: "logged", Source: writeNodeConsoleExtension(t, "logged"), Enabled: true}
			if packed {
				if _, errs := h.LoadAll(t.Context(), []ExtConfig{cfg}); len(errs) != 0 {
					t.Fatal(errs)
				}
			} else if _, err := h.Load(t.Context(), cfg); err != nil {
				t.Fatal(err)
			}
			h.mu.Lock()
			me := h.exts["logged"]
			log := me.stderrLog
			if me.packedProcess != nil {
				log = me.packedProcess.stderrLog
			}
			h.mu.Unlock()
			if log == nil || log.path == "" {
				t.Fatalf("the extension has no stderr log: %+v", me)
			}
			// A crash diagnostic that names the file retains it; the file of an ended process is otherwise removed as soon as its holders let go.
			if retained := log.retain(); retained != log.path {
				t.Fatalf("retain = %q, want %q", retained, log.path)
			}
			release(200 * time.Millisecond)
			h.TerminateProcesses()
			_ = finish()
			data, err := os.ReadFile(log.path)
			if err != nil {
				t.Fatal(err)
			}
			if want := "stderr line of logged\n"; !strings.Contains(string(data), want) {
				t.Errorf("log %s lacks %q after TerminateProcesses returned, holds %q", log.path, want, data)
			}
		})
	}
}
