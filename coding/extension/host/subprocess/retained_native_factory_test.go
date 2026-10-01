package subprocess

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Package-level state in a compiled or interpreted factory module persists across /reload for the same reason an .mjs module's does in Pi 0.87.1: the factory is invoked again inside the process that holds the module. The probe tool reports how many times the factory has run in its process.
// retainedNativeCases runs the reload and the Session-replacement check on a fresh fixture each.
func retainedNativeCases(t *testing.T, config func() ExtConfig) {
	t.Run("reload", func(t *testing.T) { retainedNativeReloadTest(t, config) })
	t.Run("replacement", func(t *testing.T) { retainedNativeReplacementTest(t, config) })
}

func retainedNativeReloadTest(t *testing.T, config func() ExtConfig) {
	t.Helper()
	cfg := config()
	configs := []ExtConfig{cfg}
	h := NewHost(t.TempDir())
	h.SetConfigLoader(func() ([]ExtConfig, error) { return configs, nil })
	t.Cleanup(func() { h.Shutdown("test done") })
	ctx, cancel := t.Context(), func() {}
	defer cancel()
	if _, errs := h.LoadAll(ctx, configs); len(errs) > 0 {
		t.Fatal(errs)
	}
	first := retainedProbeOf(t, h, cfg.Name)
	if first.Calls != 1 {
		t.Fatalf("first load factory calls = %d, want 1", first.Calls)
	}
	for reload := 1; reload <= 2; reload++ {
		h.mu.Lock()
		retiring := h.exts[cfg.Name].connection()
		h.mu.Unlock()
		if _, err := h.Reload(ctx); err != nil {
			t.Fatal(err)
		}
		// The retiring generation closes its own connection after teardown. A runtime that never closes it is failed by the heartbeat instead, a fault the reload must not depend on.
		if _, unresponsive := errors.AsType[*ExtensionUnresponsiveError](retiring.failureError()); unresponsive {
			t.Fatalf("reload %d retired the old generation through the heartbeat: %v", reload, retiring.failureError())
		}
		got := retainedProbeOf(t, h, cfg.Name)
		if got.Pid != first.Pid || got.Calls != reload+1 {
			t.Fatalf("reload %d probe = %+v, want the retained process %d with %d factory calls", reload, got, first.Pid, reload+1)
		}
	}
	// Retiring the earlier generations ended their connections and left the process: the runner exits only with the last one.
	time.Sleep(200 * time.Millisecond)
	if got := retainedProbeOf(t, h, cfg.Name); got.Pid != first.Pid {
		t.Fatalf("the process changed to %d after the earlier generations retired", got.Pid)
	}
}

// A replacement Session's Host claims the parked process of a compiled or interpreted factory too. The hold is acknowledged by the runner before the outgoing Host retires its last generation, or the runner would exit with it.
func retainedNativeReplacementTest(t *testing.T, config func() ExtConfig) {
	t.Helper()
	cfg := config()
	configs := []ExtConfig{cfg}
	retention := NewRuntimeRetention()
	t.Cleanup(retention.Close)
	first := NewHost(t.TempDir())
	first.SetRuntimeRetention(retention)
	t.Cleanup(func() { first.Shutdown("test done") })
	if _, errs := first.LoadAll(t.Context(), configs); len(errs) > 0 {
		t.Fatal(errs)
	}
	before := retainedProbeOf(t, first, cfg.Name)
	first.Retain()
	first.Shutdown("session replaced")
	second := NewHost(t.TempDir())
	second.SetRuntimeRetention(retention)
	t.Cleanup(func() { second.Shutdown("test done") })
	if _, errs := second.LoadAll(t.Context(), configs); len(errs) > 0 {
		t.Fatal(errs)
	}
	if after := retainedProbeOf(t, second, cfg.Name); after.Pid != before.Pid || after.Calls != 2 {
		t.Fatalf("replacement probe = %+v, want the parked process %d with 2 factory calls", after, before.Pid)
	}
}

func TestGoFactoryReloadReinvokesFactoryInTheRetainedProcess(t *testing.T) {
	t.Parallel()
	goFactoryRetainedCases(t, "retained-go")
}

func goFactoryRetainedCases(t *testing.T, name string) {
	t.Helper()
	for _, isolation := range []string{"shared-ok", "isolated"} {
		t.Run(isolation, func(t *testing.T) {
			retainedNativeCases(t, func() ExtConfig {
				dir := t.TempDir()
				modulePath := "example.com/retained" + isolation
				if err := os.WriteFile(filepath.Join(dir, "go.mod"), fmt.Appendf(nil, "module %s\n\ngo 1.26\n\nrequire github.com/MichaelKinsy/PiG/extensions/sdk v0.0.0\n", modulePath), 0o644); err != nil {
					t.Fatal(err)
				}
				source := `package ext

import (
	"fmt"
	"os"

	"github.com/MichaelKinsy/PiG/extensions/sdk"
)

var factoryCalls int

func Extension() *sdk.Extension {
	factoryCalls++
	e := sdk.New(EXTENSION_NAME)
	e.Tool("probe", "probe", sdk.Schema{"type": "object"}, func(ctx sdk.Context, params map[string]any) (any, error) {
		return map[string]string{"content": fmt.Sprintf("{\"calls\":%d,\"pid\":%d}", factoryCalls, os.Getpid())}, nil
	})
	return e
}
`
				source = strings.Replace(source, "EXTENSION_NAME", strconv.Quote(name), 1)
				if err := os.WriteFile(filepath.Join(dir, "ext.go"), []byte(source), 0o644); err != nil {
					t.Fatal(err)
				}
				cfg := packedFactoryConfig(name, dir, modulePath, "retained-go-"+isolation)
				cfg.Isolation = isolation
				return cfg
			})
		})
	}
}

func TestPythonFactoryReloadReinvokesFactoryInTheRetainedProcess(t *testing.T) {
	pythonFactoryRetainedCases(t, "retained-py")
}

func pythonFactoryRetainedCases(t *testing.T, name string) {
	t.Helper()
	python := findPythonExecutable(runtime.GOOS, exec.LookPath)
	if _, err := exec.LookPath(python); err != nil {
		t.Skipf("%s not found: %v", python, err)
	}
	for _, isolation := range []string{"shared-ok", "isolated"} {
		t.Run(isolation, func(t *testing.T) {
			retainedNativeCases(t, func() ExtConfig {
				dir := t.TempDir()
				source := `import json
import os

import pig_sdk

factory_calls = 0
# Module state that outlives a generation, as a cache or registry does, keeps its socket reachable: only the SDK closing it ends the connection.
generations = []


def new_extension():
    global factory_calls
    factory_calls += 1
    ext = pig_sdk.Extension(EXTENSION_NAME)
    generations.append(ext)
    ext.tool("probe", "probe", {"type": "object"}, lambda ctx, params: {"content": json.dumps({"calls": factory_calls, "pid": os.getpid()})})
    return ext
`
				source = strings.Replace(source, "EXTENSION_NAME", strconv.Quote(name), 1)
				if err := os.WriteFile(filepath.Join(dir, "retained_py_"+isolation[:3]+".py"), []byte(source), 0o644); err != nil {
					t.Fatal(err)
				}
				cfg := packedPythonFactoryConfig(name, dir, "retained_py_"+isolation[:3], "retained-py-"+isolation)
				cfg.Isolation = isolation
				return cfg
			})
		})
	}
}

func TestRustFactoryReloadReinvokesFactoryInTheRetainedProcess(t *testing.T) {
	t.Parallel()
	rustFactoryRetainedCases(t, "retained-rs")
}

func rustFactoryRetainedCases(t *testing.T, name string) {
	t.Helper()
	if _, err := exec.LookPath("cargo"); err != nil {
		t.Skipf("cargo not found: %v", err)
	}
	for _, isolation := range []string{"shared-ok", "isolated"} {
		t.Run(isolation, func(t *testing.T) {
			retainedNativeCases(t, func() ExtConfig {
				dir := t.TempDir()
				sdkRoot, err := filepath.Abs(filepath.Join("..", "..", "..", "..", "extensions", "sdk-rs"))
				if err != nil {
					t.Fatal(err)
				}
				crate := "retained_rs_" + isolation[:3]
				cargo := fmt.Sprintf("[package]\nname = %q\nversion = \"0.0.0\"\nedition = \"2024\"\n\n[dependencies]\npig-sdk = { path = %q }\nserde_json = \"1\"\n", crate, filepath.ToSlash(sdkRoot))
				if err := os.WriteFile(filepath.Join(dir, "Cargo.toml"), []byte(cargo), 0o644); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(filepath.Join(dir, "src"), 0o755); err != nil {
					t.Fatal(err)
				}
				source := `use pig_sdk::{empty_schema, Context, Extension, ToolResult};
use std::sync::Mutex;
use std::sync::atomic::{AtomicUsize, Ordering};

static FACTORY_CALLS: AtomicUsize = AtomicUsize::new(0);
// A Context kept in static state outlives its generation and keeps its connection reachable: only the SDK ending the socket closes it.
static CONTEXTS: Mutex<Vec<Context>> = Mutex::new(Vec::new());

pub fn new_extension() -> Extension {
    FACTORY_CALLS.fetch_add(1, Ordering::SeqCst);
    let mut ext = Extension::new(EXTENSION_NAME);
    ext.tool("probe", "probe", empty_schema(), move |ctx, _params| {
        CONTEXTS.lock().unwrap().push(ctx.clone());
        ToolResult::text(&format!("{{\"calls\":{},\"pid\":{}}}", FACTORY_CALLS.load(Ordering::SeqCst), std::process::id()))
    });
    ext
}
`
				source = strings.Replace(source, "EXTENSION_NAME", strconv.Quote(name), 1)
				if err := os.WriteFile(filepath.Join(dir, "src", "lib.rs"), []byte(source), 0o644); err != nil {
					t.Fatal(err)
				}
				cfg := packedRustFactoryConfig(name, dir, crate, "retained-rs-"+isolation)
				cfg.Isolation = isolation
				return cfg
			})
		})
	}
}
