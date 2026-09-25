package subprocess

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestNodeRuntimePreflightRejectsUnsupportedNodeBeforeLaunchingWarmExtension(t *testing.T) {
	launcher := buildWarmNodeLauncher(t)
	binDir := t.TempDir()
	invocations := filepath.Join(t.TempDir(), "node-invocations")
	writeFakeNode(t, binDir, invocations, "v20.18.1")
	t.Setenv("PATH", binDir)

	host := NewHostWithConfigRoot(t.TempDir(), t.TempDir())
	_, err := host.Load(t.Context(), ExtConfig{Name: "old-node", Path: launcher, Enabled: true, RuntimeLanguage: "node"})
	if err == nil {
		t.Fatal("Node 20 unexpectedly launched a TypeScript extension")
	}
	want := "TypeScript extensions need Node.js 22.13 or newer; found v20.18.1"
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("load error = %q, want %q", err, want)
	}
	if strings.Contains(err.Error(), "stderr:") || strings.Contains(err.Error(), "exit status") {
		t.Fatalf("unsupported-node error hid the requirement behind process diagnostics: %v", err)
	}
	invoked, readErr := os.ReadFile(invocations)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if got := strings.ReplaceAll(string(invoked), "\r\n", "\n"); got != "--version\n" {
		t.Fatalf("node invocations = %q, want version preflight only", got)
	}
}

func TestNodeRuntimePreflightReportsMissingNodeForWarmExtension(t *testing.T) {
	launcher := buildWarmNodeLauncher(t)
	t.Setenv("PATH", "")

	host := NewHostWithConfigRoot(t.TempDir(), t.TempDir())
	_, err := host.Load(t.Context(), ExtConfig{Name: "missing-node", Path: launcher, Enabled: true, RuntimeLanguage: "node"})
	if err == nil {
		t.Fatal("TypeScript extension unexpectedly launched without node")
	}
	want := "TypeScript extensions need Node.js 22.13 or newer; node was not found on PATH"
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("load error = %q, want %q", err, want)
	}
	if strings.Contains(err.Error(), "exit status 127") {
		t.Fatalf("missing-node error leaked shell status: %v", err)
	}
}

func TestHostCachesSuccessfulNodeRuntimePreflight(t *testing.T) {
	binDir := t.TempDir()
	invocations := filepath.Join(t.TempDir(), "node-invocations")
	writeFakeNode(t, binDir, invocations, "v22.13.0")
	t.Setenv("PATH", binDir)

	host := NewHostWithConfigRoot(t.TempDir(), t.TempDir())
	for range 2 {
		if err := host.ensureNodeRuntime(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	invoked, err := os.ReadFile(invocations)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.ReplaceAll(string(invoked), "\r\n", "\n"); got != "--version\n" {
		t.Fatalf("node invocations = %q, want one shared version preflight", got)
	}
}

func buildWarmNodeLauncher(t *testing.T) string {
	t.Helper()
	source := filepath.Join(t.TempDir(), "extension.mjs")
	if err := os.WriteFile(source, []byte("export default function () {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	builder := NewBuilderWithConfigRoot(t.TempDir(), t.TempDir())
	first, err := builder.Build("warm-node", source)
	if err != nil {
		t.Fatal(err)
	}
	second, err := builder.Build("warm-node", source)
	if err != nil {
		t.Fatal(err)
	}
	if !second.Cached || second.BinaryPath != first.BinaryPath {
		t.Fatalf("second build = %+v, want warm cache hit for %s", second, first.BinaryPath)
	}
	return second.BinaryPath
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

// writeFakeNode writes a node on binDir that appends its arguments to
// invocations and prints version: a POSIX script, or node.cmd on Windows,
// where only files with an executable extension start. cmd's echo ends lines
// with CRLF.
func writeFakeNode(t *testing.T, binDir, invocations, version string) {
	t.Helper()
	name, script := "node", "#!/bin/sh\nprintf '%s\\n' \"$*\" >> "+shellQuote(invocations)+"\nprintf '"+version+"\\n'\n"
	if runtime.GOOS == "windows" {
		name, script = "node.cmd", "@echo %*>>\""+invocations+"\"\r\n@echo "+version+"\r\n"
	}
	if err := os.WriteFile(filepath.Join(binDir, name), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}
