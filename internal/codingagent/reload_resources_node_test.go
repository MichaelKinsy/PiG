//go:build !pig_strip_node_extensions

package codingagent

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
)

func TestReloadRefreshesRealSubprocessShortcuts(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is required for the subprocess shortcut fixture")
	}
	root := t.TempDir()
	output := filepath.Join(root, "calls.txt")
	t.Setenv("PIG_SHORTCUT_TEST_OUTPUT", output)
	t.Setenv("PIG_HOME", filepath.Join(root, "pig-home"))
	source := filepath.Join(root, "extension")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	// A .ts entry, because Pi 0.87.1's /reload re-evaluates only what jiti (moduleCache: false) transpiles: a native ESM .js or .mjs stays in Node's module cache, so an edit to it, even a syntax error, is not seen until the process restarts (loader.ts loadExtensionModule, resource-loader.ts reload).
	if err := os.WriteFile(filepath.Join(source, "package.json"), []byte(`{"type":"module","main":"main.ts"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	writeNodeShortcutExtension(t, source, "old")

	host := subprocess.NewHost(root)
	host.SetMode("tui")
	config := subprocess.ExtConfig{Name: "reload-shortcut", Source: source, Enabled: true}
	host.SetConfigLoader(func() ([]subprocess.ExtConfig, error) { return []subprocess.ExtConfig{config}, nil })
	// The context bounds the whole test, which starts four node extension processes in sequence; a Windows runner needs most of a minute for those, and an expired context makes a reload drop the extension instead of failing.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	ext, err := host.Load(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer host.Shutdown("test complete")

	m := &InteractiveMode{runCtx: ctx, opts: InteractiveModeOptions{CWD: root, SubprocessHost: host}}
	m.newRunner = inproc.NewRunner([]extension.Extension{*ext}, root)
	m.setupExtensionShortcutListener(ctx)
	if !m.notifyTerminalInput("\x1b[1;6C") {
		t.Fatal("initial subprocess shortcut was not consumed")
	}
	waitFileContains(t, output, "old")

	writeNodeShortcutExtension(t, source, "new")
	reloaded, err := host.Reload(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if errs := m.replaceExtensionRunner(reloaded); len(errs) != 0 {
		t.Fatalf("replace after subprocess reload = %v", errs)
	}
	if !m.notifyTerminalInput("\x1b[1;6C") {
		t.Fatal("replacement subprocess shortcut was not consumed")
	}
	waitFileContains(t, output, "old\nnew")

	if err := os.WriteFile(filepath.Join(source, "main.ts"), []byte("export default function register(pi) { pi.registerShortcut(\"ctrl+shift+right\", { handler: () => {\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Like upstream reload, an extension whose replacement fails to load is
	// not kept: its shortcut goes away and the failure is reported.
	reloaded, err = host.Reload(ctx)
	if err != nil {
		t.Fatalf("reload failed as a whole on one failing extension: %v", err)
	}
	if report := host.LastReloadReport(); len(report.Issues) != 1 || !strings.Contains(report.Issues[0], "Failed to load extension: ") {
		t.Fatalf("reload issues = %q, want the failing extension", report.Issues)
	}
	if errs := m.replaceExtensionRunner(reloaded); len(errs) != 0 {
		t.Fatalf("replace after failed extension = %v", errs)
	}
	if m.notifyTerminalInput("\x1b[1;6C") {
		t.Fatal("failed extension's previous shortcut is still consumed")
	}
	writeNodeShortcutExtension(t, source, "fixed")
	reloaded, err = host.Reload(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if errs := m.replaceExtensionRunner(reloaded); len(errs) != 0 {
		t.Fatalf("replace after repaired extension = %v", errs)
	}
	if !m.notifyTerminalInput("\x1b[1;6C") {
		t.Fatal("repaired extension shortcut was not consumed")
	}
	waitFileContains(t, output, "old\nnew\nfixed")

	host.SetConfigLoader(func() ([]subprocess.ExtConfig, error) { return nil, nil })
	reloaded, err = host.Reload(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if errs := m.replaceExtensionRunner(reloaded); len(errs) != 0 {
		t.Fatalf("replace removed subprocess set = %v", errs)
	}
	if m.notifyTerminalInput("\x1b[1;6C") {
		t.Fatal("removed subprocess shortcut still consumed input")
	}
}
