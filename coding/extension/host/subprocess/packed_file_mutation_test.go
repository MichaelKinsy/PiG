package subprocess

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/toolchain"
)

// writePackedFileMutationModule writes a Go extension whose tool takes a turn in the file mutation queue with the SDK's free function (WithFileMutationQueue has no
// Context, so in a packed cell it rides a running member's connection), tells the test it is inside by creating <gate>.in, and keeps the turn until <gate>.out exists.
func writePackedFileMutationModule(t *testing.T, modulePath, extName, toolName string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), fmt.Appendf(nil, "module %s\n\ngo 1.26\n\nrequire github.com/MichaelKinsy/PiG/extensions/sdk v0.0.0\n", modulePath), 0o644); err != nil {
		t.Fatal(err)
	}
	src := fmt.Sprintf(`package ext

import (
	"errors"
	"os"
	"time"

	"github.com/MichaelKinsy/PiG/extensions/sdk"
)

func Extension() *sdk.Extension {
	e := sdk.New(%q)
	e.Tool(%q, "holds a turn in the file mutation queue until its gate opens", sdk.Schema{"type": "object"}, func(ctx sdk.Context, params map[string]any) (any, error) {
		path, _ := params["path"].(string)
		gate, _ := params["gate"].(string)
		_, err := sdk.WithFileMutationQueue(path, func() (struct{}, error) {
			if err := os.WriteFile(gate+".in", nil, 0o600); err != nil {
				return struct{}{}, err
			}
			for deadline := time.Now().Add(60 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
				if _, err := os.Stat(gate + ".out"); err == nil {
					return struct{}{}, nil
				}
			}
			return struct{}{}, errors.New("the gate never opened")
		})
		if err != nil {
			return nil, err
		}
		return map[string]any{"content": "held"}, nil
	})
	return e
}
`, extName, toolName)
	if err := os.WriteFile(filepath.Join(dir, "ext.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// writePackedPythonFileMutationModule is the Python counterpart of writePackedFileMutationModule: pig_sdk.with_file_mutation_queue is its free function.
func writePackedPythonFileMutationModule(t *testing.T, moduleName, extName, toolName string) string {
	t.Helper()
	dir := t.TempDir()
	src := fmt.Sprintf(`import os
import time

import pig_sdk


def hold(ctx, params):
    gate = params["gate"]

    def task():
        open(gate + ".in", "w").close()
        deadline = time.time() + 60
        while time.time() < deadline:
            if os.path.exists(gate + ".out"):
                return
            time.sleep(0.005)
        raise RuntimeError("the gate never opened")

    pig_sdk.with_file_mutation_queue(params["path"], task)
    return {"content": "held"}


def new_extension():
    ext = pig_sdk.Extension(%q)
    ext.tool(%q, "holds a turn in the file mutation queue until its gate opens", {"type": "object"}, hold)
    return ext
`, extName, toolName)
	if err := os.WriteFile(filepath.Join(dir, moduleName+".py"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// writePackedRustFileMutationCrate is the Rust counterpart of writePackedFileMutationModule: pig_sdk::with_file_mutation_queue is its free function.
func writePackedRustFileMutationCrate(t *testing.T, packageName, extName, toolName string) string {
	t.Helper()
	dir := t.TempDir()
	sdkRoot, err := filepath.Abs(filepath.Join("..", "..", "..", "..", "extensions", "sdk-rs"))
	if err != nil {
		t.Fatal(err)
	}
	cargo := fmt.Sprintf("[package]\nname = %q\nversion = \"0.0.0\"\nedition = \"2024\"\n\n[dependencies]\npig-sdk = { path = %q }\nserde_json = \"1\"\n", packageName, filepath.ToSlash(sdkRoot))
	if err := os.WriteFile(filepath.Join(dir, "Cargo.toml"), []byte(cargo), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	src := fmt.Sprintf(`use pig_sdk::{empty_schema, Extension, ToolResult};
use std::time::{Duration, Instant};

pub fn new_extension() -> Extension {
    let mut ext = Extension::new(%q);
    ext.tool(%q, "holds a turn in the file mutation queue until its gate opens", empty_schema(), |_ctx, params| {
        let path = params["path"].as_str().unwrap_or_default().to_string();
        let gate = params["gate"].as_str().unwrap_or_default().to_string();
        let held = pig_sdk::with_file_mutation_queue(&path, || {
            std::fs::write(format!("{gate}.in"), "").map_err(|e| e.to_string())?;
            let deadline = Instant::now() + Duration::from_secs(60);
            while Instant::now() < deadline {
                if std::path::Path::new(&format!("{gate}.out")).exists() {
                    return Ok(());
                }
                std::thread::sleep(Duration::from_millis(5));
            }
            Err("the gate never opened".to_string())
        });
        match held {
            Ok(Ok(())) => ToolResult::text("held"),
            Ok(Err(message)) => ToolResult::error(message),
            Err(error) => ToolResult::error(error.to_string()),
        }
    });
    ext
}
`, extName, toolName)
	if err := os.WriteFile(filepath.Join(dir, "src", "lib.rs"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// A packed cell runs several extensions in one process, and an SDK's free function withFileMutationQueue has no Context, so its turn rides the connection of a
// member that is running. When that member stops (its connection closes while the process lives on, as one packed member's does: disablePackedMember) while ANOTHER
// member's task holds the turn, the turn must stay held: it is the process's, and the host's queue keeps the file for the task, as Pi's queue does, until the task
// ends. Which member carries the turn is the SDK's choice (it varies from process to process), so the test first asks the host which connection a turn rides, then has
// the other member hold the turn and stops the carrier. Go, Rust and Python packed cells share the host's ticket registry and have the same free function.
func TestPackedMemberStoppingDoesNotReleaseAnotherMembersFileMutationTurn(t *testing.T) {
	type language struct {
		name    string
		prepare func(t *testing.T) []ExtConfig
	}
	languages := []language{
		{"go", func(t *testing.T) []ExtConfig {
			return []ExtConfig{
				packedFactoryConfig("fm-a", writePackedFileMutationModule(t, "example.com/fmpack/a", "fm-a", "hold_a"), "example.com/fmpack/a", "ha"),
				packedFactoryConfig("fm-b", writePackedFileMutationModule(t, "example.com/fmpack/b", "fm-b", "hold_b"), "example.com/fmpack/b", "hb"),
			}
		}},
		{"python", func(t *testing.T) []ExtConfig {
			python := toolchain.PythonExecutable(runtime.GOOS, exec.LookPath)
			if _, err := exec.LookPath(python); err != nil {
				t.Skipf("%s not found: %v", python, err)
			}
			return []ExtConfig{
				packedPythonFactoryConfig("fm-a", writePackedPythonFileMutationModule(t, "fm_py_a", "fm-a", "hold_a"), "fm_py_a", "ha"),
				packedPythonFactoryConfig("fm-b", writePackedPythonFileMutationModule(t, "fm_py_b", "fm-b", "hold_b"), "fm_py_b", "hb"),
			}
		}},
		{"rust", func(t *testing.T) []ExtConfig {
			if _, err := exec.LookPath("cargo"); err != nil {
				t.Skipf("cargo not found: %v", err)
			}
			return []ExtConfig{
				packedRustFactoryConfig("fm-a", writePackedRustFileMutationCrate(t, "fm_a", "fm-a", "hold_a"), "fm_a", "ha"),
				packedRustFactoryConfig("fm-b", writePackedRustFileMutationCrate(t, "fm_b", "fm-b", "hold_b"), "fm_b", "hb"),
			}
		}},
	}
	for _, lang := range languages {
		t.Run(lang.name, func(t *testing.T) {
			// Packed toolchain builds are intentionally serial in this package: Go, Rust and Python cell builds together can exhaust CI process memory.
			configs := lang.prepare(t)
			tools := map[string]string{"fm-a": "hold_a", "fm-b": "hold_b"}
			ctx, cancel := context.WithTimeout(context.Background(), 360*time.Second)
			defer cancel()
			h := NewHost(t.TempDir())
			h.SetConfigLoader(func() ([]ExtConfig, error) { return configs, nil })
			t.Cleanup(func() { h.Shutdown("test done") })
			enters := make(chan string, 8)
			h.SetInboundObserver(func(extName string, env *Envelope) {
				if env.Type == MsgCall && env.Call != nil && env.Call.Method == CallFileMutationEnter {
					select {
					case enters <- extName:
					default:
					}
				}
			})
			if _, err := h.Reload(ctx); err != nil {
				t.Fatal(err)
			}
			present, keys := packedCellSnapshot(h, "fm-a", "fm-b")
			if !present[0] || !present[1] || keys[0] == "" || keys[0] != keys[1] {
				t.Fatalf("expected both extensions in one packed cell, present=%v keys=%q", present, keys)
			}
			execute := func(member string, params map[string]string) error {
				t.Helper()
				raw, err := json.Marshal(params)
				if err != nil {
					t.Fatal(err)
				}
				for _, ext := range h.Extensions() {
					if def, ok := ext.Tools[tools[member]]; ok {
						_, err := def.Definition.Execute(ctx, "tc-"+member, raw, nil)
						return err
					}
				}
				t.Fatalf("no extension has tool %s", tools[member])
				return nil
			}
			dir := t.TempDir()

			// Which connection does a turn ride? A task whose gate is open takes the turn and gives it back at once.
			probeGate := filepath.Join(dir, "probe-gate")
			if err := os.WriteFile(probeGate+".out", nil, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := execute("fm-a", map[string]string{"path": filepath.Join(dir, "probe.txt"), "gate": probeGate}); err != nil {
				t.Fatal(err)
			}
			carrier := <-enters
			holder := "fm-a"
			if carrier == holder {
				holder = "fm-b"
			}
			t.Logf("a turn rides member %s's connection; member %s holds one and member %s stops", carrier, holder, carrier)

			file, gate := filepath.Join(dir, "held.txt"), filepath.Join(dir, "gate")
			done := make(chan error, 1)
			go func() { done <- execute(holder, map[string]string{"path": file, "gate": gate}) }()
			awaitFile(t, gate+".in")
			if queueFree(file) {
				t.Fatal("the file was free while a task held its turn")
			}
			h.mu.Lock()
			victim := h.exts[carrier]
			h.mu.Unlock()
			if victim == nil || victim.connection() == nil {
				t.Fatalf("member %s is not loaded", carrier)
			}
			if err := victim.connection().Close("test: the member stopped"); err != nil {
				t.Logf("close: %v", err)
			}
			deadline := time.Now().Add(20 * time.Second)
			for {
				h.mu.Lock()
				gone := h.exts[carrier] == nil
				h.mu.Unlock()
				if gone {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("member %s was never detached after its connection closed", carrier)
				}
				time.Sleep(10 * time.Millisecond)
			}
			// The other member's task still runs inside its turn: a host mutation of the file waits.
			if queueFree(file) {
				t.Fatalf("stopping member %s released the file member %s's task still held: the turn rode the stopped member's connection", carrier, holder)
			}
			if err := os.WriteFile(gate+".out", nil, 0o600); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-done:
				if err != nil {
					t.Fatalf("the task failed: %v", err)
				}
			case <-time.After(30 * time.Second):
				t.Fatal("the task never finished")
			}
			// The task gave its turn back, over the connection of the member that still runs: the file is free again.
			deadline = time.Now().Add(20 * time.Second)
			for !queueFree(file) {
				if time.Now().After(deadline) {
					t.Fatal("the file stayed held after the task ended: its exit never reached the host")
				}
			}
		})
	}
}

func awaitFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for {
		if _, err := os.Stat(path); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s never appeared", path)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
