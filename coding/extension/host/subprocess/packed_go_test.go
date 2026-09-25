package subprocess

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/coding/extension/host/runtimecell"
	"github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// Pi's loader awaits factories without a deadline. PiG's subprocess load must
// nevertheless stop promptly when its process exits. The two-second bound
// measures only local spawn/reap/error delivery: no compiler or factory work.
// The general test budget bounds hangs but must not replace this latency proof.
func TestPackedProcessExitFailsTheLoad(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("executable script fixture requires a Unix host")
	}
	script := filepath.Join(t.TempDir(), "exit-before-connect")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexit 7\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	host := NewHost(t.TempDir())
	t.Cleanup(func() { host.Shutdown("test done") })
	ctx := testbudget.Context(t)
	start := time.Now()
	_, err := host.LoadGoPackedCell(ctx, &runtimecell.GoPackedCell{
		Key:        "early-exit",
		Hash:       "early-exit",
		BinaryPath: script,
		Extensions: []runtimecell.GoExtension{{Name: "never-connects"}},
	})
	if elapsed := time.Since(start); elapsed >= 2*time.Second {
		t.Errorf("early process exit took %s to fail the load, want less than 2s", elapsed)
	} else {
		t.Logf("early process exit failed the load in %s", elapsed)
	}
	if ctx.Err() != nil {
		t.Errorf("load waited for caller cancellation: %v", ctx.Err())
	}
	var loadErr *LoadError
	if !errors.As(err, &loadErr) || loadErr.Code != "process_exited" {
		t.Fatalf("load error = %v, want process_exited", err)
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 7 {
		t.Fatalf("load error = %v, want child exit status 7", err)
	}
}

func TestPackedAcceptReportsExitBeforeListenerCompletes(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		listener := &packedExitTestListener{closed: make(chan struct{})}
		defer func() { _ = listener.Close() }()
		process := &packedProcessState{}
		// Own the reaper completion barrier so neither an OS scheduling delay
		// nor a timer can release the pending accept in this ordering proof.
		process.waitOnce.Do(func() { process.waitDone = make(chan struct{}) })
		me := &managedExt{config: ExtConfig{Name: "never-connects"}, packedProcess: process}
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		result := make(chan error, 1)
		host := NewHost(t.TempDir())
		go func() {
			_, err := host.acceptPackedExt(ctx, me, listener)
			result <- err
		}()
		synctest.Wait()
		select {
		case err := <-result:
			t.Fatalf("load completed before process exit: %v", err)
		default:
		}
		exitErr := errors.New("reaped child failure")
		process.waitErr = exitErr
		close(process.waitDone)
		synctest.Wait()
		select {
		case err := <-result:
			var loadErr *LoadError
			if !errors.As(err, &loadErr) || loadErr.Code != "process_exited" || !errors.Is(err, exitErr) {
				t.Fatalf("load error = %v, want original process_exited cause", err)
			}
		default:
			t.Fatal("load did not observe process exit while listener and caller remained pending")
		}
	})
}

type packedExitTestListener struct {
	closed chan struct{}
}

func (l *packedExitTestListener) Accept() (net.Conn, error) {
	<-l.closed
	return nil, net.ErrClosed
}

func (l *packedExitTestListener) Close() error {
	close(l.closed)
	return nil
}

func (*packedExitTestListener) Addr() net.Addr {
	return &net.UnixAddr{Name: "packed-exit-test", Net: "unix"}
}

func TestHost_QuarantinePackedCellRemovesMembers(t *testing.T) {
	h := NewHost(t.TempDir())
	var crashed []string
	var reasons []string
	h.SetCrashHandler(func(name string, delay time.Duration, disabled bool, reason string) {
		if disabled {
			crashed = append(crashed, name)
			reasons = append(reasons, reason)
		}
	})
	h.exts["a"] = &managedExt{config: ExtConfig{Name: "a"}, host: h, packedCellKey: "cell-a"}
	h.exts["b"] = &managedExt{config: ExtConfig{Name: "b"}, host: h, packedCellKey: "cell-a"}
	h.exts["c"] = &managedExt{config: ExtConfig{Name: "c"}, host: h}
	h.quarantinePackedCell("cell-a", "boom")
	if got := h.QuarantinedCells()["cell-a"]; got != "boom" {
		t.Fatalf("quarantine reason = %q", got)
	}
	if h.exts["a"] != nil || h.exts["b"] != nil || h.exts["c"] == nil {
		t.Fatalf("ext registry after quarantine = %+v", h.exts)
	}
	if len(crashed) != 2 {
		t.Fatalf("crash callbacks = %v, want 2 disabled callbacks", crashed)
	}
	// Every notice must say which extensions the crash stopped and that
	// /reload brings them back, never that the shared process is still
	// running (see disablePackedMember's removal for the connection-close
	// path in host.go).
	const want = "boom; a, b stopped; /reload restarts them"
	for i, reason := range reasons {
		if reason != want {
			t.Fatalf("crash reason for %s = %q, want %q", crashed[i], reason, want)
		}
	}
}

func packedCellSnapshot(host *Host, names ...string) ([]bool, []string) {
	host.mu.Lock()
	defer host.mu.Unlock()
	present := make([]bool, len(names))
	keys := make([]string, len(names))
	for i, name := range names {
		if managed := host.exts[name]; managed != nil {
			present[i] = true
			keys[i] = managed.packedCellKey
		}
	}
	return present, keys
}

func TestHost_ReloadPlansPackedPythonAndFissionsQuarantinedCell(t *testing.T) {
	// Packed toolchain builds are intentionally serial in this package. Running
	// Go, Rust, and Python cell builds together can exhaust CI process memory.
	python := findPythonExecutable(runtime.GOOS, exec.LookPath)
	if _, err := exec.LookPath(python); err != nil {
		t.Skipf("%s not found: %v", python, err)
	}
	rootA := writePackedPythonFactoryModule(t, "reload_py_a", "reload-py-a", "tool_a")
	rootB := writePackedPythonFactoryModule(t, "reload_py_b", "reload-py-b", "tool_b")
	configs := []ExtConfig{
		packedPythonFactoryConfig("reload-py-a", rootA, "reload_py_a", "ha"),
		packedPythonFactoryConfig("reload-py-b", rootB, "reload_py_b", "hb"),
	}
	h := NewHost(t.TempDir())
	h.SetConfigLoader(func() ([]ExtConfig, error) { return configs, nil })
	t.Cleanup(func() { h.Shutdown("test done") })
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	if _, err := h.Reload(ctx); err != nil {
		t.Fatal(err)
	}
	firstPresent, firstKeys := packedCellSnapshot(h, "reload-py-a", "reload-py-b")
	if !firstPresent[0] || !firstPresent[1] || firstKeys[0] == "" || firstKeys[0] != firstKeys[1] {
		t.Fatalf("expected python extensions in one packed cell, present=%v keys=%q", firstPresent, firstKeys)
	}
	assertPackedProjectTrust(t, h)
	packedKey := firstKeys[0]
	// CNC-002: a single crash's quarantine is released on the very next
	// explicit Reload (the packed cell gets one more chance to repack), so
	// only a cell key whose circuit breaker has actually tripped stays
	// fissioned. Quarantine it MaxCrashes times, matching the same budget
	// every isolated extension's own Supervisor already enforces, before
	// the Reload this test asserts on.
	for range DefaultSupervisorConfig().MaxCrashes {
		h.quarantinePackedCell(packedKey, "boom")
	}
	if _, err := h.Reload(ctx); err != nil {
		t.Fatal(err)
	}
	secondPresent, secondKeys := packedCellSnapshot(h, "reload-py-a", "reload-py-b")
	if !secondPresent[0] || !secondPresent[1] || secondKeys[0] == "" || secondKeys[1] == "" || secondKeys[0] == packedKey || secondKeys[1] == packedKey || secondKeys[0] == secondKeys[1] {
		t.Fatalf("expected python fission to separate generated isolated cells, old=%q present=%v keys=%q", packedKey, secondPresent, secondKeys)
	}
}

func TestHost_ReloadPlansPackedRustAndFissionsQuarantinedCell(t *testing.T) {
	if _, err := exec.LookPath("cargo"); err != nil {
		t.Skipf("cargo not found: %v", err)
	}
	rootA := writePackedRustFactoryCrate(t, "reload_rust_a", "reload-rust-a", "tool_a")
	rootB := writePackedRustFactoryCrate(t, "reload_rust_b", "reload-rust-b", "tool_b")
	configs := []ExtConfig{
		packedRustFactoryConfig("reload-rust-a", rootA, "reload_rust_a", "ha"),
		packedRustFactoryConfig("reload-rust-b", rootB, "reload_rust_b", "hb"),
	}
	h := NewHost(t.TempDir())
	h.SetConfigLoader(func() ([]ExtConfig, error) { return configs, nil })
	t.Cleanup(func() { h.Shutdown("test done") })
	// The reloads run three cargo builds: the packed cell, then one isolated
	// cell per extension. On a Windows host they take about 160s even alone,
	// and a fixed budget cancelled the last build under load, which left an
	// extension missing. The test binary's deadline bounds them instead.
	ctx := t.Context()
	if _, err := h.Reload(ctx); err != nil {
		t.Fatal(err)
	}
	h.mu.Lock()
	firstA := h.exts["reload-rust-a"]
	firstB := h.exts["reload-rust-b"]
	firstAKey, firstBKey := "", ""
	if firstA != nil {
		firstAKey = firstA.packedCellKey
	}
	if firstB != nil {
		firstBKey = firstB.packedCellKey
	}
	h.mu.Unlock()
	if firstA == nil || firstB == nil || firstAKey == "" || firstAKey != firstBKey {
		t.Fatalf("expected rust extensions in one packed cell, got present=%t/%t keys=%q/%q", firstA != nil, firstB != nil, firstAKey, firstBKey)
	}
	assertPackedProjectTrust(t, h)
	packedKey := firstAKey
	// CNC-002: a single crash's quarantine is released on the very next
	// explicit Reload (the packed cell gets one more chance to repack), so
	// only a cell key whose circuit breaker has actually tripped stays
	// fissioned. Quarantine it MaxCrashes times, matching the same budget
	// every isolated extension's own Supervisor already enforces, before
	// the Reload this test asserts on.
	for range DefaultSupervisorConfig().MaxCrashes {
		h.quarantinePackedCell(packedKey, "boom")
	}
	if _, err := h.Reload(ctx); err != nil {
		t.Fatal(err)
	}
	h.mu.Lock()
	secondA := h.exts["reload-rust-a"]
	secondB := h.exts["reload-rust-b"]
	secondAKey, secondBKey := "", ""
	if secondA != nil {
		secondAKey = secondA.packedCellKey
	}
	if secondB != nil {
		secondBKey = secondB.packedCellKey
	}
	h.mu.Unlock()
	if secondA == nil || secondB == nil || secondAKey == "" || secondBKey == "" || secondAKey == packedKey || secondBKey == packedKey || secondAKey == secondBKey {
		t.Fatalf("expected rust fission to separate generated isolated cells, old=%q new present=%t/%t keys=%q/%q", packedKey, secondA != nil, secondB != nil, secondAKey, secondBKey)
	}
}

func TestHost_ReloadPlansPackedGoAndFissionsQuarantinedCell(t *testing.T) {
	rootA := writePackedFactoryModule(t, "example.com/reloadpack/a", "reload-a", "tool_a")
	rootB := writePackedFactoryModule(t, "example.com/reloadpack/b", "reload-b", "tool_b")
	configs := []ExtConfig{
		packedFactoryConfig("reload-a", rootA, "example.com/reloadpack/a", "ha"),
		packedFactoryConfig("reload-b", rootB, "example.com/reloadpack/b", "hb"),
	}
	h := NewHostWithConfigRoot(t.TempDir(), t.TempDir())
	h.SetConfigLoader(func() ([]ExtConfig, error) { return configs, nil })
	t.Cleanup(func() { h.Shutdown("test done") })
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	if _, err := h.Reload(ctx); err != nil {
		t.Fatal(err)
	}
	h.mu.Lock()
	firstA := h.exts["reload-a"]
	firstB := h.exts["reload-b"]
	firstAKey, firstBKey := "", ""
	if firstA != nil {
		firstAKey = firstA.packedCellKey
	}
	if firstB != nil {
		firstBKey = firstB.packedCellKey
	}
	h.mu.Unlock()
	if firstA == nil || firstB == nil || firstAKey == "" || firstAKey != firstBKey {
		t.Fatalf("expected reload-a/reload-b in one packed cell, got present=%t/%t keys=%q/%q", firstA != nil, firstB != nil, firstAKey, firstBKey)
	}
	assertPackedProjectTrust(t, h)
	packedKey := firstAKey
	// CNC-002: a single crash's quarantine is released on the very next
	// explicit Reload (the packed cell gets one more chance to repack), so
	// only a cell key whose circuit breaker has actually tripped stays
	// fissioned. Quarantine it MaxCrashes times, matching the same budget
	// every isolated extension's own Supervisor already enforces, before
	// the Reload this test asserts on.
	for range DefaultSupervisorConfig().MaxCrashes {
		h.quarantinePackedCell(packedKey, "boom")
	}
	if _, err := h.Reload(ctx); err != nil {
		t.Fatal(err)
	}
	h.mu.Lock()
	secondA := h.exts["reload-a"]
	secondB := h.exts["reload-b"]
	secondAKey, secondBKey := "", ""
	if secondA != nil {
		secondAKey = secondA.packedCellKey
	}
	if secondB != nil {
		secondBKey = secondB.packedCellKey
	}
	h.mu.Unlock()
	if secondA == nil || secondB == nil {
		t.Fatalf("expected fissioned extensions after reload, got present=%t/%t", secondA != nil, secondB != nil)
	}
	if secondAKey == "" || secondBKey == "" || secondAKey == packedKey || secondBKey == packedKey || secondAKey == secondBKey {
		t.Fatalf("expected fission to separate generated isolated cells, old=%q new=%q/%q", packedKey, secondAKey, secondBKey)
	}
}

// TestHost_KillingPackedProcessNeverClaimsItRemainedAlive is the direct
// regression test for ATTACK-POINTS #5: killing a real packed Go process
// used to race two notice paths (each member's own connection-close handler
// vs. watchPackedProcess), and whichever member's handler won the race
// falsely reported "packed member connection closed while shared process
// remained alive" even though the whole process had just been killed. It
// also covers #9: the notice must surface the packed cell's stderr log path.
func TestHost_KillingPackedProcessNeverClaimsItRemainedAlive(t *testing.T) {
	rootA := writePackedFactoryModule(t, "example.com/killpack/a", "kill-a", "tool_a")
	rootB := writePackedFactoryModule(t, "example.com/killpack/b", "kill-b", "tool_b")
	configs := []ExtConfig{
		packedFactoryConfig("kill-a", rootA, "example.com/killpack/a", "ha"),
		packedFactoryConfig("kill-b", rootB, "example.com/killpack/b", "hb"),
	}
	h := NewHostWithConfigRoot(t.TempDir(), t.TempDir())
	h.SetConfigLoader(func() ([]ExtConfig, error) { return configs, nil })
	t.Cleanup(func() { h.Shutdown("test done") })

	var mu sync.Mutex
	reasons := make(map[string]string)
	h.SetCrashHandler(func(name string, _ time.Duration, disabled bool, reason string) {
		if !disabled {
			return
		}
		mu.Lock()
		reasons[name] = reason
		mu.Unlock()
	})

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	if _, err := h.Reload(ctx); err != nil {
		t.Fatal(err)
	}
	h.mu.Lock()
	memberA := h.exts["kill-a"]
	memberB := h.exts["kill-b"]
	h.mu.Unlock()
	if memberA == nil || memberB == nil || memberA.packedCellKey == "" || memberA.packedCellKey != memberB.packedCellKey {
		t.Fatalf("expected kill-a/kill-b in one packed cell, got %#v/%#v", memberA, memberB)
	}
	packedKey := memberA.packedCellKey
	stderrLogPath := memberA.stderrLogPath
	if stderrLogPath == "" {
		t.Fatal("packed member has no captured stderr log path")
	}

	proc := memberA.proc
	if proc == nil {
		t.Fatal("packed member has no live process to kill")
	}
	if err := proc.Kill(); err != nil {
		t.Fatal(err)
	}
	waitForPackedQuarantine(t, h, packedKey)

	mu.Lock()
	defer mu.Unlock()
	if len(reasons) != 2 {
		t.Fatalf("crash notices = %#v, want notices for both kill-a and kill-b", reasons)
	}
	// Which of the two racing detectors (this member's own connection-close
	// handler, or watchPackedProcess reaping the killed process) reaches a
	// given member first is nondeterministic. A member's notice is therefore
	// either the plain connection-close form, a quarantine notice naming all
	// members still attached when the process exit is observed, or (when the
	// sibling handler removed itself first) a quarantine notice naming only
	// this remaining member. Every form is accurate about what that detector
	// stopped. What must never appear is the ATTACK-POINTS #5 lie that the
	// shared process "remained alive".
	logSuffix := "(stderr: " + stderrLogPath + ")"
	for name, reason := range reasons {
		if strings.Contains(reason, "remained alive") {
			t.Fatalf("%s crash reason falsely claims the shared process remained alive: %q", name, reason)
		}
		if !strings.HasSuffix(reason, logSuffix) {
			t.Fatalf("%s crash reason %q does not surface the stderr log path %q", name, reason, stderrLogPath)
		}
		plain := "packed member connection closed " + logSuffix
		quarantined := strings.Contains(reason, "/reload restarts them") &&
			(strings.Contains(reason, "kill-a, kill-b stopped") || strings.Contains(reason, name+" stopped"))
		if reason != plain && !quarantined {
			t.Fatalf("%s crash reason %q is neither the plain connection-closed notice nor an accurate quarantine notice", name, reason)
		}
	}
}

func TestHost_LoadGoPackedCellRegistersAllExtensionsAtomically(t *testing.T) {
	rootA := writePackedFactoryModule(t, "example.com/packedhost/a", "pack-a", "tool_a")
	rootB := writePackedFactoryModule(t, "example.com/packedhost/b", "pack-b", "tool_b")
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	cell, err := runtimecell.BuildGoPackedCell(ctx, t.TempDir(), "cell:host-test", []runtimecell.GoExtension{
		{Name: "pack-a", Root: rootA, ModulePath: "example.com/packedhost/a", Package: "example.com/packedhost/a", Factory: "Extension", Hash: "ha"},
		{Name: "pack-b", Root: rootB, ModulePath: "example.com/packedhost/b", Package: "example.com/packedhost/b", Factory: "Extension", Hash: "hb"},
	})
	if err != nil {
		t.Fatal(err)
	}
	h := NewHost(t.TempDir())
	t.Cleanup(func() { h.Shutdown("test done") })
	exts, err := h.LoadGoPackedCell(ctx, cell)
	if err != nil {
		t.Fatal(err)
	}
	if len(exts) != 2 || h.ExtensionCount() != 2 {
		t.Fatalf("extensions len/count = %d/%d", len(exts), h.ExtensionCount())
	}
	tools := map[string]bool{}
	for _, ext := range h.Extensions() {
		if len(ext.Tools) != 1 {
			t.Fatalf("extension %s tools = %v", ext.Path, ext.Tools)
		}
		for name := range ext.Tools {
			tools[name] = true
		}
	}
	if !tools["tool_a"] || !tools["tool_b"] {
		t.Fatalf("registered tools = %v", tools)
	}
}

func inputLimitsTestModel() map[string]any {
	return map[string]any{"id": "limits", "provider": "conformance", "inputLimits": map[string]any{"maxRequestBytes": 12345.0, "images": map[string]any{"resize": map[string]any{"maxWidth": 321.0}}}}
}

func TestHost_MixedFusedPackedAndIsolatedExtensions(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skipf("node not found: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	packedA := writePackedFactoryModule(t, "example.com/mixed/a", "mixed-packed-a", "packed-a")
	packedB := writePackedFactoryModule(t, "example.com/mixed/b", "mixed-packed-b", "packed-b")
	cell, err := runtimecell.BuildGoPackedCell(ctx, t.TempDir(), "cell:mixed", []runtimecell.GoExtension{
		{Name: "mixed-packed-a", Root: packedA, ModulePath: "example.com/mixed/a", Package: "example.com/mixed/a", Factory: "Extension", Hash: "ha"},
		{Name: "mixed-packed-b", Root: packedB, ModulePath: "example.com/mixed/b", Package: "example.com/mixed/b", Factory: "Extension", Hash: "hb"},
	})
	if err != nil {
		t.Fatal(err)
	}

	isolatedSource := filepath.Join(t.TempDir(), "mixed-isolated.mjs")
	isolatedModule := `export default function (pi) {
  pi.registerTool({
    name: "isolated-tool",
    label: "Isolated Tool",
    description: "Return isolated health",
    parameters: {type: "object", properties: {}},
    async execute() { return {content: [{type: "text", text: "isolated ok"}]}; },
  });
  pi.registerCommand("isolated-crash", {
    description: "Exit the isolated runtime",
    handler: async () => process.exit(17),
  });
  pi.on("agent_before_settle", async (event, ctx) => {
    ctx.ui.notify("agent-before-settle=" + event.outcome + ":" + event.entries.length + ":" + Boolean(event.continue) + ":" + event.context.contextEntries.length + ":" + Boolean(event.context.canContinue), "info");
    return {entries: [{type: "custom", customType: "packed-boundary"}], continue: true};
  });
  pi.on("context", async (event) => ({messages: event.messages.slice(1)}));
  pi.on("context_with_system", async (event) => { event.messages[0].content = "request-system"; event.messages[0].toolsAdded.shift(); });
  pi.on("turn_end", async (event, ctx) => ctx.ui.notify("turn-end=" + event.messageEntryId + ":" + event.toolResultEntryIds[0], "info"));
  for (const name of ["ui_prompt_start", "ui_prompt_end"]) {
    pi.on(name, async (event, ctx) => ctx.ui.notify("ui-prompt=" + event.type + ":" + event.kind + ":" + ("title" in event ? event.title : "(none)"), "info"));
  }
}`
	if err := os.WriteFile(isolatedSource, []byte(isolatedModule), 0o644); err != nil {
		t.Fatal(err)
	}

	fused := sdk.New("mixed-fused")
	fused.OnEvent("context", func(_ sdk.Context, data map[string]any) (any, error) {
		return map[string]any{"messages": data["messages"].([]any)[1:]}, nil
	})
	fused.OnEvent("context_with_system", func(_ sdk.Context, data map[string]any) (any, error) {
		head := data["messages"].([]any)[0].(map[string]any)
		head["content"] = "request-system"
		head["toolsAdded"] = head["toolsAdded"].([]any)[1:]
		return nil, nil
	})
	fused.Tool("fused-tool", "Return fused health", sdk.Schema{"type": "object"}, func(sdk.Context, map[string]any) (any, error) {
		return sdk.ToolResult{Content: "fused ok"}, nil
	})

	var promptMu sync.Mutex
	var prompts []string
	recordPrompt := func(message string) {
		promptMu.Lock()
		prompts = append(prompts, message)
		promptMu.Unlock()
	}
	fused.OnEvent("agent_before_settle", func(_ sdk.Context, data map[string]any) (any, error) {
		entries, _ := data["entries"].([]any)
		preview, _ := data["context"].(map[string]any)
		contextEntries, _ := preview["contextEntries"].([]any)
		recordPrompt(fmt.Sprintf("agent-before-settle=%v:%d:%v:%d:%v", data["outcome"], len(entries), data["continue"], len(contextEntries), preview["canContinue"]))
		return map[string]any{"entries": []any{map[string]any{"type": "custom", "customType": "packed-boundary"}}, "continue": true}, nil
	})
	fused.OnEvent("turn_end", func(_ sdk.Context, data map[string]any) (any, error) {
		ids, _ := data["toolResultEntryIds"].([]any)
		recordPrompt(fmt.Sprintf("turn-end=%v:%v", data["messageEntryId"], ids[0]))
		return nil, nil
	})
	for _, name := range []string{"ui_prompt_start", "ui_prompt_end"} {
		fused.OnEvent(name, func(_ sdk.Context, data map[string]any) (any, error) {
			recordPrompt(fmt.Sprintf("fused=%v:%v:%v", data["type"], data["kind"], data["title"]))
			return nil, nil
		})
	}
	fused.Command("fused-prompt", "Open a fused dialog", func(ctx sdk.Context, _ string) error {
		_, _, err := ctx.Select("Fused", []string{"yes"})
		return err
	})

	fused.Command("fused-model", "Exercise fused model streaming", func(ctx sdk.Context, _ string) error {
		active := ctx.GetModelInfo()
		if active == nil || active.InputLimits["maxRequestBytes"] != float64(12345) {
			return fmt.Errorf("fused inputLimits = %#v", active)
		}
		images, _ := active.InputLimits["images"].(map[string]any)
		resize, _ := images["resize"].(map[string]any)
		if resize["maxWidth"] != float64(321) {
			return fmt.Errorf("fused resize = %#v", resize)
		}
		model := map[string]any{"provider": "conformance", "modelId": "fused"}
		request := map[string]any{"messages": []any{map[string]any{"role": "user", "content": "hello"}}}
		stream := ctx.ModelRegistry().Stream(model, request, nil)
		var types []string
		for event := range stream.Events(context.Background()) {
			types = append(types, fmt.Sprint(event["type"]))
		}
		if strings.Join(types, ",") != "start,text_start,text_delta,text_end,done" {
			return fmt.Errorf("fused model events = %v", types)
		}
		if fmt.Sprint(stream.Result()["stopReason"]) != "stop" {
			return fmt.Errorf("fused model result = %#v", stream.Result())
		}
		return nil
	})

	host := NewHost(t.TempDir())
	bridge := NewUIBridge(func() {})
	bridge.SetHostAction("getModelInfo", inputLimitsTestModel)
	bridge.SetHostAction("streamModel", func(_ context.Context, model map[string]any, _ map[string]any) (*ai.AssistantMessageEventStream, error) {
		if model["provider"] != "conformance" || model["modelId"] != "fused" {
			return nil, fmt.Errorf("fused model identity = %#v", model)
		}
		return packedTestModelStream()
	})
	mixedUI := newTestUIContext()
	mixedUI.onNotify = func(message, _ string) {
		if strings.HasPrefix(message, "ui-prompt=") || strings.HasPrefix(message, "agent-before-settle=") || strings.HasPrefix(message, "turn-end=") {
			recordPrompt(message)
		}
	}
	bridge.SetUIContext(mixedUI)
	host.SetUIBridge(bridge)
	t.Cleanup(func() { host.Shutdown("test done") })
	fusedLoaded, err := host.LoadInProcess(ctx, ExtConfig{Name: "mixed-fused", Enabled: true}, fused.RunWithConn)
	if err != nil {
		t.Fatal(err)
	}
	if err := fusedLoaded.Commands["fused-model"].Handler(ctx, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := host.LoadGoPackedCell(ctx, cell); err != nil {
		t.Fatal(err)
	}
	isolated, err := host.Load(ctx, ExtConfig{Name: "mixed-isolated", Source: isolatedSource, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if isolated == nil {
		t.Fatal("isolated extension did not load")
	}
	if host.ExtensionCount() != 4 {
		t.Fatalf("extension count = %d, want 4", host.ExtensionCount())
	}

	for _, ext := range host.Extensions() {
		assertRealizedContextTransform(t, ext)
	}

	mixedRunner := inproc.NewRunner(host.Extensions(), t.TempDir())
	boundary, err := mixedRunner.EmitBoundary(ctx, "agent_before_settle", extension.AgentActivityCompleted,
		func(entries []extension.SessionBoundaryDraft) (extension.BoundaryContextPreview, error) {
			return extension.BoundaryContextPreview{ContextEntries: make([]extension.ProjectedSessionEntry, len(entries)), CanContinue: len(entries) > 0}, nil
		})
	if err != nil {
		t.Fatal(err)
	}
	if len(boundary.Entries) != 1 || !boundary.Continue {
		t.Fatalf("mixed boundary result = %+v", boundary)
	}
	boundaryDeadline := time.Now().Add(10 * time.Second)
	for {
		promptMu.Lock()
		count := len(prompts)
		promptMu.Unlock()
		if count == 4 {
			break
		}
		if time.Now().After(boundaryDeadline) {
			promptMu.Lock()
			got := slices.Clone(prompts)
			promptMu.Unlock()
			t.Fatalf("mixed boundary reports = %v, want four realizations", got)
		}
		time.Sleep(10 * time.Millisecond)
	}

	promptMu.Lock()
	prompts = nil
	promptMu.Unlock()
	if _, err := mixedRunner.Emit(ctx, extension.TurnEndEvent{Type: "turn_end", MessageEntryID: "assistant-entry", ToolResultEntryIds: []string{"tool-entry"}}); err != nil {
		t.Fatal(err)
	}
	turnDeadline := time.Now().Add(10 * time.Second)
	for {
		promptMu.Lock()
		got := slices.Clone(prompts)
		promptMu.Unlock()
		if len(got) == 4 && !slices.ContainsFunc(got, func(value string) bool { return value != "turn-end=assistant-entry:tool-entry" }) {
			break
		}
		if time.Now().After(turnDeadline) {
			t.Fatalf("mixed turn_end reports = %v, want four realizations", got)
		}
		time.Sleep(10 * time.Millisecond)
	}

	// One fused dialog reports ui_prompt_start/ui_prompt_end to every
	// realization in the Piglet: fused, both packed members, and isolated.
	promptMu.Lock()
	prompts = nil
	promptMu.Unlock()
	bridge.SetUIPromptScope(mixedRunner)
	if err := fusedLoaded.Commands["fused-prompt"].Handler(ctx, ""); err != nil {
		t.Fatal(err)
	}
	wantPrompts := []string{
		"fused=ui_prompt_end:select:Fused", "fused=ui_prompt_start:select:Fused",
		"ui-prompt=ui_prompt_end:select:Fused", "ui-prompt=ui_prompt_end:select:Fused", "ui-prompt=ui_prompt_end:select:Fused",
		"ui-prompt=ui_prompt_start:select:Fused", "ui-prompt=ui_prompt_start:select:Fused", "ui-prompt=ui_prompt_start:select:Fused",
	}
	promptDeadline := time.Now().Add(10 * time.Second)
	for {
		promptMu.Lock()
		got := slices.Sorted(slices.Values(prompts))
		promptMu.Unlock()
		if slices.Equal(got, wantPrompts) {
			break
		}
		if time.Now().After(promptDeadline) {
			t.Fatalf("mixed ui_prompt reports = %v, want %v", got, wantPrompts)
		}
		time.Sleep(10 * time.Millisecond)
	}

	wantTools := map[string]bool{"fused-tool": false, "packed-a": false, "packed-b": false, "isolated-tool": false}
	tools := make(map[string]extension.RegisteredTool, len(wantTools))
	for _, loaded := range host.Extensions() {
		for name, tool := range loaded.Tools {
			if _, ok := wantTools[name]; !ok {
				continue
			}
			if _, err := tool.Definition.Execute(ctx, "mixed-call", json.RawMessage(`{}`), nil); err != nil {
				t.Fatalf("execute %s: %v", name, err)
			}
			wantTools[name] = true
			tools[name] = tool
		}
	}
	for name, ran := range wantTools {
		if !ran {
			t.Errorf("tool %s did not register and execute", name)
		}
	}

	crash, ok := isolated.Commands["isolated-crash"]
	if !ok {
		t.Fatal("isolated-crash command did not register")
	}
	if err := crash.Handler(ctx, ""); err == nil {
		t.Fatal("isolated crash returned no error")
	}
	for _, name := range []string{"fused-tool", "packed-a", "packed-b"} {
		if _, err := tools[name].Definition.Execute(ctx, "after-crash", json.RawMessage(`{}`), nil); err != nil {
			t.Fatalf("execute %s after isolated crash: %v", name, err)
		}
	}
}

func packedTestModelStream() (*ai.AssistantMessageEventStream, error) {
	partial := &ai.AssistantMessage{Content: []ai.AssistantContentBlock{ai.TextContent{Text: "streamed"}}, StopReason: ai.StopReasonPending}
	final := &ai.AssistantMessage{Content: []ai.AssistantContentBlock{ai.TextContent{Text: "streamed"}}, StopReason: ai.StopReasonStop}
	stream := ai.NewAssistantMessageEventStream()
	for _, event := range []ai.AssistantMessageEvent{
		ai.StartEvent{Partial: partial},
		ai.TextStartEvent{ContentIndex: 0, Partial: partial},
		ai.TextDeltaEvent{ContentIndex: 0, Delta: "streamed", Partial: partial},
		ai.TextEndEvent{ContentIndex: 0, Content: "streamed", Partial: partial},
		ai.DoneEvent{Reason: ai.StopReasonStop, Message: final},
	} {
		if err := stream.Push(event); err != nil {
			return nil, err
		}
	}
	return stream, nil
}

func TestAC59PackedSDKFocusedComponentsOwnInput(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping packed SDK builds in short mode")
	}
	moduleRoot := findModuleRoot(t)
	t.Setenv("PIG_SDK_GO_ROOT", filepath.Join(moduleRoot, "extensions", "sdk"))
	t.Setenv("PIG_SDK_PY_ROOT", filepath.Join(moduleRoot, "extensions", "sdk-py"))
	t.Setenv("PIG_SDK_RS_ROOT", filepath.Join(moduleRoot, "extensions", "sdk-rs"))
	tests := []struct {
		name    string
		configs func(*testing.T) []ExtConfig
	}{
		{"go", func(t *testing.T) []ExtConfig {
			return []ExtConfig{
				packedFactoryConfig("focused-go-a", writePackedFactoryModule(t, "example.com/focused/goa", "focused-go-a", "tool-a", true), "example.com/focused/goa", "ha"),
				packedFactoryConfig("focused-go-b", writePackedFactoryModule(t, "example.com/focused/gob", "focused-go-b", "tool-b", true), "example.com/focused/gob", "hb"),
			}
		}},
		{"python", func(t *testing.T) []ExtConfig {
			python := findPythonExecutable(runtime.GOOS, exec.LookPath)
			if _, err := exec.LookPath(python); err != nil {
				t.Skipf("%s is not installed", python)
			}
			return []ExtConfig{
				packedPythonFactoryConfig("focused-py-a", writePackedPythonFactoryModule(t, "focused_py_a", "focused-py-a", "tool-a"), "focused_py_a", "ha"),
				packedPythonFactoryConfig("focused-py-b", writePackedPythonFactoryModule(t, "focused_py_b", "focused-py-b", "tool-b"), "focused_py_b", "hb"),
			}
		}},
		{"rust", func(t *testing.T) []ExtConfig {
			if _, err := exec.LookPath("cargo"); err != nil {
				t.Skip("cargo is not installed")
			}
			return []ExtConfig{
				packedRustFactoryConfig("focused-rs-a", writePackedRustFactoryCrate(t, "focused_rs_a", "focused-rs-a", "tool-a"), "focused_rs_a", "ha"),
				packedRustFactoryConfig("focused-rs-b", writePackedRustFactoryCrate(t, "focused_rs_b", "focused-rs-b", "tool-b"), "focused_rs_b", "hb"),
			}
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			configs := tc.configs(t)
			fakeUI := newTestUIContext()
			notified := make(chan string, 1)
			fakeUI.onNotify = func(message, _ string) { notified <- message }
			configRoot := t.TempDir()
			host := NewHostWithConfigRoot(t.TempDir(), configRoot)
			bridge := NewUIBridge(func() {})
			bridge.SetUIContext(fakeUI)
			bridge.SetHostAction("streamModel", func(context.Context, map[string]any, map[string]any) (*ai.AssistantMessageEventStream, error) {
				return packedTestModelStream()
			})
			host.SetUIBridge(bridge)
			defer host.Shutdown("test done")

			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
			defer cancel()
			loaded, errs := host.LoadAll(ctx, configs)
			if len(errs) > 0 || len(loaded) != 2 {
				t.Fatalf("LoadAll loaded=%d errors=%v", len(loaded), errs)
			}
			present, keys := packedCellSnapshot(host, configs[0].Name, configs[1].Name)
			if !present[0] || !present[1] || keys[0] == "" || keys[0] != keys[1] {
				t.Fatalf("extensions did not share one packed cell: present=%v keys=%q", present, keys)
			}
			command := loaded[0].Commands["focused"]
			commandDone := make(chan error, 1)
			go func() { commandDone <- command.Handler(context.Background(), "") }()
			handle, input := fakeUI.awaitOverlay(t, 5*time.Second)
			host.NotifyWidth(91)
			deadline := time.Now().Add(5 * time.Second)
			for {
				lines := handle.Lines()
				if len(lines) == 3 && lines[0] == "focused width=91" {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("resized focused lines = %v", lines)
				}
				time.Sleep(10 * time.Millisecond)
			}
			input.OnInput("\x1b[6~")
			input.OnInput("\r")
			select {
			case err := <-commandDone:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("focused command did not complete")
			}
			select {
			case message := <-notified:
				if message != "focused=beta" {
					t.Fatalf("notification = %q", message)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("focused result notification missing")
			}

			if err := loaded[0].Commands["model-runtime"].Handler(context.Background(), ""); err != nil {
				t.Fatal(err)
			}
			select {
			case message := <-notified:
				if message != "model=streamed" {
					t.Fatalf("model notification = %q", message)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("model runtime notification missing")
			}

			cancelDone := make(chan error, 1)
			go func() { cancelDone <- command.Handler(context.Background(), "") }()
			cancelHandle, cancelInput := fakeUI.awaitOverlay(t, 5*time.Second)
			cancelInput.OnInput("\x1b")
			select {
			case <-cancelHandle.Done():
			case <-time.After(5 * time.Second):
				t.Fatal("Escape did not close focused overlay")
			}
			select {
			case err := <-cancelDone:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("cancelled focused command did not complete")
			}
			select {
			case message := <-notified:
				if message != "focused=" {
					t.Fatalf("cancel notification = %q", message)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("focused cancel notification missing")
			}

			timerDone := make(chan error, 1)
			go func() { timerDone <- loaded[0].Commands["timer"].Handler(context.Background(), "") }()
			timerHandle, timerInput := fakeUI.awaitOverlay(t, 5*time.Second)
			deadline = time.Now().Add(5 * time.Second)
			for {
				lines := timerHandle.Lines()
				var frame int
				if len(lines) > 0 {
					_, _ = fmt.Sscanf(lines[0], "timer frame=%d", &frame)
				}
				if frame >= 2 {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("packed timer did not advance without input: %v", lines)
				}
				time.Sleep(10 * time.Millisecond)
			}
			timerInput.OnInput("\r")
			if err := <-timerDone; err != nil {
				t.Fatal(err)
			}
			select {
			case message := <-notified:
				var frame int
				var disposed, detached bool
				if _, err := fmt.Sscanf(message, "timer=%d disposed=%t detached=%t", &frame, &disposed, &detached); err != nil || frame < 2 || !disposed || !detached {
					t.Fatalf("packed timer notification = %q", message)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("packed timer lifecycle notification missing")
			}

			reloadDone := make(chan error, 1)
			go func() { reloadDone <- command.Handler(context.Background(), "") }()
			reloadHandle, _ := fakeUI.awaitOverlay(t, 5*time.Second)
			host.SetConfigLoader(func() ([]ExtConfig, error) { return nil, nil })
			if _, err := host.Reload(ctx); err != nil {
				t.Fatal(err)
			}
			select {
			case <-reloadHandle.Done():
			case <-time.After(5 * time.Second):
				t.Fatal("reload did not close focused overlay")
			}
			select {
			case err := <-reloadDone:
				if err == nil {
					t.Fatal("focused command reported success after reload removed its extension")
				}
			case <-time.After(5 * time.Second):
				t.Fatal("focused command remained blocked after reload")
			}
		})
	}
}

func TestPackedSDKFocusedComponentAndSessionActionsMatch(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping packed SDK builds in short mode")
	}
	moduleRoot := findModuleRoot(t)
	t.Setenv("PIG_SDK_GO_ROOT", filepath.Join(moduleRoot, "extensions", "sdk"))
	t.Setenv("PIG_SDK_PY_ROOT", filepath.Join(moduleRoot, "extensions", "sdk-py"))
	t.Setenv("PIG_SDK_RS_ROOT", filepath.Join(moduleRoot, "extensions", "sdk-rs"))

	tests := []struct {
		name    string
		configs func(*testing.T) []ExtConfig
	}{
		{"go", func(t *testing.T) []ExtConfig {
			return []ExtConfig{
				packedFactoryConfig("timer-go-a", writePackedFactoryModule(t, "example.com/timer/goa", "timer-go-a", "tool-a"), "example.com/timer/goa", "ha"),
				packedFactoryConfig("timer-go-b", writePackedFactoryModule(t, "example.com/timer/gob", "timer-go-b", "tool-b"), "example.com/timer/gob", "hb"),
			}
		}},
		{"python", func(t *testing.T) []ExtConfig {
			return []ExtConfig{
				packedPythonFactoryConfig("timer-py-a", writePackedPythonFactoryModule(t, "timer_py_a", "timer-py-a", "tool-a"), "timer_py_a", "ha"),
				packedPythonFactoryConfig("timer-py-b", writePackedPythonFactoryModule(t, "timer_py_b", "timer-py-b", "tool-b"), "timer_py_b", "hb"),
			}
		}},
		{"rust", func(t *testing.T) []ExtConfig {
			if _, err := exec.LookPath("cargo"); err != nil {
				t.Skip("cargo is not installed")
			}
			return []ExtConfig{
				packedRustFactoryConfig("timer-rs-a", writePackedRustFactoryCrate(t, "timer_rs_a", "timer-rs-a", "tool-a"), "timer_rs_a", "ha"),
				packedRustFactoryConfig("timer-rs-b", writePackedRustFactoryCrate(t, "timer_rs_b", "timer-rs-b", "tool-b"), "timer_rs_b", "hb"),
			}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fakeUI := newTestUIContext()
			notified := make(chan string, 8)
			fakeUI.onNotify = func(message, _ string) { notified <- message }
			host := NewHostWithConfigRoot(t.TempDir(), t.TempDir())
			actions := make(chan string, 2)
			bridge := NewUIBridge(func() {})
			bridge.SetUIContext(fakeUI)
			bridge.SetActions(&HostCallbacks{
				GetModelInfo: inputLimitsTestModel,
				AppendEntry: func(customType string, data any) error {
					actions <- fmt.Sprintf("appendEntry:%s:%v", customType, data)
					return nil
				},
				SetSessionName: func(name string) error {
					actions <- "setSessionName:" + name
					return nil
				},
			})
			host.SetUIBridge(bridge)
			defer host.Shutdown("test done")

			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
			defer cancel()
			configs := test.configs(t)
			loaded, errs := host.LoadAll(ctx, configs)
			if len(errs) > 0 || len(loaded) != 2 {
				t.Fatalf("LoadAll loaded=%d errors=%v", len(loaded), errs)
			}
			present, keys := packedCellSnapshot(host, configs[0].Name, configs[1].Name)
			if !present[0] || !present[1] || keys[0] == "" || keys[0] != keys[1] {
				t.Fatalf("timer extensions did not share a packed cell: present=%v keys=%q", present, keys)
			}
			for _, ext := range loaded {
				assertRealizedContextTransform(t, ext)
			}
			sessionCommand, ok := loaded[0].Commands["session"]
			if !ok {
				t.Fatal("packed session action command not registered")
			}
			if err := sessionCommand.Handler(ctx, ""); err != nil {
				t.Fatalf("packed session action command: %v", err)
			}
			for _, want := range []string{"appendEntry:packed-entry:map[value:hello]", "setSessionName:packed-session"} {
				select {
				case got := <-actions:
					if got != want {
						t.Fatalf("packed session action = %q, want %q", got, want)
					}
				case <-time.After(5 * time.Second):
					t.Fatalf("packed session action %q missing", want)
				}
			}
			runner := inproc.NewRunner(loaded, t.TempDir())
			bridge.SetUIPromptScope(runner)
			for _, event := range []struct {
				value any
				want  string
			}{
				{extension.SessionInfoChangedEvent{Type: "session_info_changed", Name: "packed-session"}, "session-event=packed-session"},
				{extension.SessionBeforeCompactEvent{Type: "session_before_compact", Reason: "threshold", WillRetry: true}, "session-before-compact=threshold:true"},
				{extension.SessionCompactEvent{Type: "session_compact", Reason: "threshold", WillRetry: true, FromExtension: true}, "session-compact=threshold:true:true"},
				{extension.SessionCompactFailedEvent{Type: "session_compact_failed", Reason: "overflow", ErrorMessage: "recovery failed", FromExtension: true}, "session-compact-failed=overflow:recovery failed:false:false:true"},
				{extension.TurnEndEvent{Type: "turn_end", MessageEntryID: "assistant-entry", ToolResultEntryIds: []string{"tool-entry"}}, "turn-end=assistant-entry:tool-entry"},
			} {
				if _, err := runner.Emit(ctx, event.value); err != nil {
					t.Fatalf("packed event %T: %v", event.value, err)
				}
				for range 2 {
					select {
					case got := <-notified:
						if got != event.want {
							t.Fatalf("packed event notification = %q, want %q", got, event.want)
						}
					case <-time.After(5 * time.Second):
						t.Fatalf("packed event notification %q missing", event.want)
					}
				}
			}
			boundary, err := runner.EmitBoundary(ctx, "agent_before_settle", extension.AgentActivityCompleted,
				func(entries []extension.SessionBoundaryDraft) (extension.BoundaryContextPreview, error) {
					return extension.BoundaryContextPreview{ContextEntries: make([]extension.ProjectedSessionEntry, len(entries)), CanContinue: len(entries) > 0}, nil
				})
			if err != nil {
				t.Fatalf("packed agent_before_settle: %v", err)
			}
			for _, want := range []string{
				"agent-before-settle=completed:0:false:0:false",
				"agent-before-settle=completed:1:true:1:true",
			} {
				select {
				case got := <-notified:
					if got != want {
						t.Fatalf("packed boundary notification = %q, want %q", got, want)
					}
				case <-time.After(5 * time.Second):
					t.Fatalf("packed boundary notification %q missing", want)
				}
			}
			if len(boundary.Entries) != 1 || !boundary.Continue {
				t.Fatalf("packed boundary result = %+v", boundary)
			}
			commandDone := make(chan error, 1)
			go func() { commandDone <- loaded[0].Commands["timer"].Handler(context.Background(), "") }()
			handle, input := fakeUI.awaitOverlay(t, 5*time.Second)
			deadline := time.Now().Add(5 * time.Second)
			for {
				lines := handle.Lines()
				var frame int
				if len(lines) > 0 {
					_, _ = fmt.Sscanf(lines[0], "timer frame=%d", &frame)
				}
				if frame >= 2 {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("packed timer did not advance without input: %v", lines)
				}
				time.Sleep(10 * time.Millisecond)
			}
			input.OnInput("\r")
			if err := <-commandDone; err != nil {
				t.Fatal(err)
			}
			// The timer overlay is a ui.custom prompt: both packed members
			// receive one unawaited start/end pair, starts before ends.
			var timerMessage string
			var prompts []string
			for len(prompts) < 4 || timerMessage == "" {
				select {
				case message := <-notified:
					if strings.HasPrefix(message, "ui-prompt=") {
						prompts = append(prompts, message)
					} else {
						timerMessage = message
					}
				case <-time.After(5 * time.Second):
					t.Fatalf("packed timer notifications missing: timer=%q prompts=%v", timerMessage, prompts)
				}
			}
			var frame int
			var disposed, detached bool
			if _, err := fmt.Sscanf(timerMessage, "timer=%d disposed=%t detached=%t", &frame, &disposed, &detached); err != nil || frame < 2 || !disposed || !detached {
				t.Fatalf("packed timer notification = %q", timerMessage)
			}
			wantPrompts := []string{
				"ui-prompt=ui_prompt_start:custom:(none)", "ui-prompt=ui_prompt_start:custom:(none)",
				"ui-prompt=ui_prompt_end:custom:(none)", "ui-prompt=ui_prompt_end:custom:(none)",
			}
			// Upstream void emit permits cross-event completion to overlap.
			slices.Sort(prompts)
			slices.Sort(wantPrompts)
			if !slices.Equal(prompts, wantPrompts) {
				t.Fatalf("packed ui_prompt notifications = %v, want %v", prompts, wantPrompts)
			}
		})
	}
}

func TestHost_LoadEmbeddedCellsStartsPrebuiltBinaryWithoutSource(t *testing.T) {
	rootA := writePackedFactoryModule(t, "example.com/embedded/a", "embed-a", "tool_a")
	rootB := writePackedFactoryModule(t, "example.com/embedded/b", "embed-b", "tool_b")
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	cell, err := runtimecell.BuildGoPackedCell(ctx, t.TempDir(), "cell:embedded-test", []runtimecell.GoExtension{
		{Name: "embed-a", Root: rootA, ModulePath: "example.com/embedded/a", Package: "example.com/embedded/a", Factory: "Extension", Hash: "ha"},
		{Name: "embed-b", Root: rootB, ModulePath: "example.com/embedded/b", Package: "example.com/embedded/b", Factory: "Extension", Hash: "hb"},
	})
	if err != nil {
		t.Fatal(err)
	}

	h := NewHost(t.TempDir())
	t.Cleanup(func() { h.Shutdown("test done") })
	loaded, errs := h.LoadEmbeddedCells(ctx, []EmbeddedCell{{
		Language:   "go",
		Key:        "cell:embedded-test",
		BinaryPath: cell.BinaryPath,
		Extensions: []EmbeddedExtension{{Name: "embed-a", Hash: "ha"}, {Name: "embed-b", Hash: "hb"}},
	}})
	if len(errs) > 0 {
		t.Fatalf("LoadEmbeddedCells errors: %v", errs)
	}
	if len(loaded) != 2 || h.ExtensionCount() != 2 {
		t.Fatalf("extensions len/count = %d/%d", len(loaded), h.ExtensionCount())
	}
	tools := map[string]bool{}
	for _, ext := range h.Extensions() {
		for name := range ext.Tools {
			tools[name] = true
		}
	}
	if !tools["tool_a"] || !tools["tool_b"] {
		t.Fatalf("registered tools = %v", tools)
	}
}

func assertPackedProjectTrust(t *testing.T, host *Host) {
	t.Helper()
	runner := inproc.NewRunner(host.Extensions(), t.TempDir())
	result, handlerErrors, err := inproc.EmitProjectTrust(runner, context.Background(), extension.ProjectTrustEvent{
		Type: "project_trust",
		Cwd:  t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(handlerErrors) != 0 {
		t.Fatalf("project_trust handler errors: %+v", handlerErrors)
	}
	if result == nil || result.Trusted != extension.ProjectTrustYes || result.Remember == nil || !*result.Remember {
		t.Fatalf("project_trust result = %+v", result)
	}
}

func packedPythonFactoryConfig(name, root, packageName, hash string) ExtConfig {
	return ExtConfig{
		Name:            name,
		Enabled:         true,
		Source:          root,
		RuntimeKind:     "subprocess",
		RuntimeLanguage: "python",
		SDKName:         "pig-sdk-py",
		Isolation:       "shared-ok",
		EntrypointKind:  "factory",
		Package:         packageName,
		Factory:         "new_extension",
		ContentHash:     hash,
	}
}

func packedRustFactoryConfig(name, root, packageName, hash string) ExtConfig {
	return ExtConfig{
		Name:            name,
		Enabled:         true,
		Source:          root,
		RuntimeKind:     "subprocess",
		RuntimeLanguage: "rust",
		SDKName:         "pig-sdk",
		Isolation:       "shared-ok",
		EntrypointKind:  "factory",
		Package:         packageName,
		Factory:         "new_extension",
		ContentHash:     hash,
	}
}

func packedFactoryConfig(name, root, modulePath, hash string) ExtConfig {
	return ExtConfig{
		Name:            name,
		Enabled:         true,
		Source:          root,
		RuntimeKind:     "subprocess",
		RuntimeLanguage: "go",
		SDKName:         "github.com/MichaelKinsy/PiG/extensions/sdk",
		Isolation:       "shared-ok",
		EntrypointKind:  "factory",
		ModulePath:      modulePath,
		Package:         modulePath,
		Factory:         "Extension",
		ContentHash:     hash,
	}
}

func writePackedPythonFactoryModule(t *testing.T, moduleName, extName, toolName string) string {
	t.Helper()
	dir := t.TempDir()
	src := fmt.Sprintf(`import pig_sdk
import threading


class Focused:
    def __init__(self):
        self.selected = 0
    def render(self, width):
        return [f"focused width={width}", "> alpha" if self.selected == 0 else "  alpha", "> beta" if self.selected == 1 else "  beta"]
    def handle_input(self, data):
        if data == "\x1b[B": self.selected = 1
        if data == "\x1b[6~": self.selected = 1
        if data == "\r": return pig_sdk.RemoteComponentResult(done=True, value="beta")
        if data == "\x1b": return pig_sdk.RemoteComponentResult(done=True)
        return pig_sdk.RemoteComponentResult()


class TimerFocused:
    def __init__(self):
        self.frame = 0
        self.invalidate = None
        self.disposed = False
        self.detached = False
        self.stop = threading.Event()
        self.worker = threading.Thread(target=self.tick, daemon=True)
        self.worker.start()
    def tick(self):
        while not self.stop.wait(0.02):
            self.frame += 1
            if self.invalidate is not None: self.invalidate()
    def render(self, width):
        return [f"timer frame={self.frame} width={width}"]
    def handle_input(self, data):
        if data == "\r": return pig_sdk.RemoteComponentResult(done=True, value=self.frame)
        return pig_sdk.RemoteComponentResult()
    def set_invalidate(self, invalidate):
        self.invalidate = invalidate
        self.detached = invalidate is None
    def dispose(self):
        self.stop.set()
        self.worker.join()
        self.disposed = True


def timer_command(ctx, _args):
    component = TimerFocused()
    value = ctx.custom(component)
    ctx.notify(f"timer={value} disposed={str(component.disposed).lower()} detached={str(component.detached).lower()}", "info")


def session_command(ctx, _args):
    model = ctx.get_model_info()
    if model is None or model.get("inputLimits") != {"maxRequestBytes":12345,"images":{"resize":{"maxWidth":321}}}:
        raise RuntimeError(f"packed inputLimits = {model}")
    ctx.append_entry("packed-entry", {"value": "hello"})
    ctx.set_session_name("packed-session")


def model_runtime_command(ctx, _args):
    model = {"provider":"openrouter", "modelId":"org/model/name"}
    request = {"messages":[{"role":"user", "content":"hello"}]}
    stream = ctx.model_registry.stream(model, request)
    types = [event["type"] for event in stream.events()]
    if types != ["start", "text_start", "text_delta", "text_end", "done"]: raise RuntimeError(f"stream events = {types}")
    if stream.result()["content"][0]["text"] != "streamed": raise RuntimeError("bad stream result")
    simple = ctx.model_registry.stream_simple(model, request)
    simple_types = [event["type"] for event in simple.events()]
    if simple_types != types: raise RuntimeError(f"simple events = {simple_types}")
    if simple.result()["content"][0]["text"] != "streamed": raise RuntimeError("bad simple result")
    ctx.notify("model=streamed", "info")


def context_event(ctx, data):
    del data["messages"][0]


def full_context_event(ctx, data):
    data["messages"][0]["content"] = "request-system"
    del data["messages"][0]["toolsAdded"][0]


prompt_lock = threading.Lock()
prompt_sequence = 0

def ui_prompt_event(ctx, data):
    global prompt_sequence
    with prompt_lock:
        sequence = prompt_sequence
        prompt_sequence += 1
    title = data.get("title", "(none)")
    if title.startswith("fifo:"):
        ctx.notify("fifo:%%d:%%s:%%s" %% (sequence, data["type"], title), "info")
        return
    ctx.notify("ui-prompt=%%s:%%s:%%s" %% (data.get("type"), data.get("kind"), title), "info")


def agent_before_settle(ctx, data):
    entries = data.get("entries") or []
    preview = data.get("context") or {}
    ctx.notify("agent-before-settle=%%s:%%d:%%s:%%d:%%s" %% (data.get("outcome", ""), len(entries), str(data.get("continue", False)).lower(), len(preview.get("contextEntries") or []), str(preview.get("canContinue", False)).lower()), "info")
    data["entries"].append({"type":"custom", "customType":"kept"})


def boundary_error(ctx, data):
    data["entries"].append({"type":"custom", "customType":"before-error"})
    raise RuntimeError("boundary failed")


def boundary_result(ctx, data):
    assert [e["customType"] for e in data["entries"][-2:]] == ["kept", "before-error"]
    assert len(data["context"]["contextEntries"]) == len(data["entries"])
    return {"entries":[{"type":"custom","customType":"packed-boundary"}], "continue":True}


def new_extension():
    ext = pig_sdk.Extension(%q)
    ext.tool(%q, "test tool", {"type": "object"}, lambda ctx, params: {"content": "ok"})
    ext.command("focused", "focused component", lambda ctx, args: ctx.notify("focused=" + str(ctx.custom(Focused()) or ""), "info"))
    ext.command("timer", "timer component", timer_command)
    ext.command("session", "session actions", session_command)
    ext.command("send_user_content", "send user content", lambda ctx, _args: ctx.send_user_message([{"type":"text","text":"first"},{"type":"image","data":"aW1hZ2U=","mimeType":"image/png"},{"type":"text","text":"second"}], "steer"))
    ext.command("model-runtime", "model runtime", model_runtime_command)
    ext.on_event("agent_before_settle", agent_before_settle)
    ext.on_event("agent_before_settle", boundary_error)
    ext.on_event("agent_before_settle", boundary_result)
    ext.on_event("session_info_changed", lambda ctx, data: ctx.notify("session-event=" + str(data.get("name", "")), "info"))
    ext.on_event("session_before_compact", lambda ctx, data: ctx.notify("session-before-compact=%%s:%%s" %% (data.get("reason", ""), str(data.get("willRetry", False)).lower()), "info"))
    ext.on_event("session_compact", lambda ctx, data: ctx.notify("session-compact=%%s:%%s:%%s" %% (data.get("reason", ""), str(data.get("willRetry", False)).lower(), str(data.get("fromExtension", False)).lower()), "info"))
    ext.on_event("session_compact_failed", lambda ctx, data: ctx.notify("session-compact-failed=%%s:%%s:%%s:%%s:%%s" %% (data.get("reason", ""), data.get("errorMessage", ""), str(data.get("aborted", False)).lower(), str(data.get("willRetry", False)).lower(), str(data.get("fromExtension", False)).lower()), "info"))
    ext.on_event("turn_end", lambda ctx, data: ctx.notify("turn-end=%%s:%%s" %% (data.get("messageEntryId", ""), data.get("toolResultEntryIds", [""])[0]), "info"))
    ext.on_event("context", context_event)
    ext.on_event("context_with_system", full_context_event)
    ext.on_event("ui_prompt_start", ui_prompt_event)
    ext.on_event("ui_prompt_end", ui_prompt_event)
    ext.on_project_trust(lambda _ctx, _data: {"trusted": "undecided"})
    ext.on_project_trust(lambda _ctx, _data: {"trusted": "yes", "remember": True})
    return ext
`, extName, toolName)
	if err := os.WriteFile(filepath.Join(dir, moduleName+".py"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func writePackedRustFactoryCrate(t *testing.T, packageName, extName, toolName string) string {
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
	src := fmt.Sprintf(`use pig_sdk::{empty_schema, CommandResult, Extension, ProjectTrustDecision, ProjectTrustResult, RemoteComponent, RemoteComponentInvalidate, RemoteComponentResult, ToolResult};
use serde_json::json;
use std::sync::{Arc, Mutex, atomic::{AtomicBool, Ordering}};
use std::{thread, time::Duration};

struct Focused { selected: usize }
impl RemoteComponent for Focused {
    fn render(&self, width: u32) -> Vec<String> { vec![format!("focused width={width}"), if self.selected == 0 { "> alpha".into() } else { "  alpha".into() }, if self.selected == 1 { "> beta".into() } else { "  beta".into() }] }
    fn handle_input(&mut self, data: &str) -> Result<RemoteComponentResult, String> {
        if data == "\u{1b}[B" { self.selected = 1; }
        if data == "\u{1b}[6~" { self.selected = 1; }
        if data == "\r" { return Ok(RemoteComponentResult::done(Some(json!("beta")))); }
        if data == "\u{1b}" { return Ok(RemoteComponentResult::done(None)); }
        Ok(RemoteComponentResult::pending())
    }
}

struct TimerFocused {
    frame: Arc<Mutex<u64>>,
    invalidate: Arc<Mutex<Option<RemoteComponentInvalidate>>>,
    stop: Arc<AtomicBool>,
    disposed: Arc<AtomicBool>,
    detached: Arc<AtomicBool>,
    worker: Option<thread::JoinHandle<()>>,
}
impl TimerFocused {
    fn new() -> (Self, Arc<AtomicBool>, Arc<AtomicBool>) {
        let frame = Arc::new(Mutex::new(0));
        let invalidate = Arc::new(Mutex::new(None::<RemoteComponentInvalidate>));
        let stop = Arc::new(AtomicBool::new(false));
        let disposed = Arc::new(AtomicBool::new(false));
        let detached = Arc::new(AtomicBool::new(false));
        let worker = {
            let frame = frame.clone(); let invalidate = invalidate.clone(); let stop = stop.clone();
            thread::spawn(move || while !stop.load(Ordering::Acquire) { thread::sleep(Duration::from_millis(20)); if stop.load(Ordering::Acquire) { return; } *frame.lock().unwrap() += 1; if let Some(callback) = invalidate.lock().unwrap().clone() { callback(); } })
        };
        (Self { frame, invalidate, stop, disposed: disposed.clone(), detached: detached.clone(), worker: Some(worker) }, disposed, detached)
    }
}
impl RemoteComponent for TimerFocused {
    fn render(&self, width: u32) -> Vec<String> { vec![format!("timer frame={} width={width}", *self.frame.lock().unwrap())] }
    fn handle_input(&mut self, data: &str) -> Result<RemoteComponentResult, String> { if data == "\r" { return Ok(RemoteComponentResult::done(Some(json!(*self.frame.lock().unwrap())))); } Ok(RemoteComponentResult::pending()) }
    fn set_invalidate(&mut self, invalidate: Option<RemoteComponentInvalidate>) { self.detached.store(invalidate.is_none(), Ordering::Release); *self.invalidate.lock().unwrap() = invalidate; }
    fn dispose(&mut self) { self.stop.store(true, Ordering::Release); if let Some(worker) = self.worker.take() { let _ = worker.join(); } self.disposed.store(true, Ordering::Release); }
}

pub fn new_extension() -> Extension {
    let mut ext = Extension::new(%q);
    ext.tool(%q, "test tool", empty_schema(), |_ctx, _params| ToolResult::text("ok"));
    ext.command("focused", "focused component", |ctx, _| match ctx.custom_component(Focused { selected: 0 }, json!({})) { Ok(value) => { ctx.notify(&format!("focused={}", value.and_then(|v| v.as_str().map(str::to_string)).unwrap_or_default()), "info"); CommandResult::Ok }, Err(err) => CommandResult::Error(err.to_string()) });
    ext.command("timer", "timer component", |ctx, _| { let (component, disposed, detached) = TimerFocused::new(); match ctx.custom_component(component, json!({})) { Ok(value) => { ctx.notify(&format!("timer={} disposed={} detached={}", value.and_then(|v| v.as_u64()).unwrap_or(0), disposed.load(Ordering::Acquire), detached.load(Ordering::Acquire)), "info"); CommandResult::Ok }, Err(err) => CommandResult::Error(err.to_string()) } });
    ext.command("send_user_content", "send user content", |ctx, _| match ctx.send_user_message(json!([{"type":"text","text":"first"},{"type":"image","data":"aW1hZ2U=","mimeType":"image/png"},{"type":"text","text":"second"}]), "steer") { Ok(()) => CommandResult::Ok, Err(err) => CommandResult::Error(err.to_string()) });
    ext.command("session", "session actions", |ctx, _| { if ctx.get_model_info().and_then(|model| model.input_limits) != Some(json!({"maxRequestBytes":12345,"images":{"resize":{"maxWidth":321}}})) { return CommandResult::Error("packed inputLimits mismatch".into()); } if let Err(err) = ctx.append_entry("packed-entry", json!({"value":"hello"})) { return CommandResult::Error(err.to_string()); } match ctx.set_session_name("packed-session") { Ok(()) => CommandResult::Ok, Err(err) => CommandResult::Error(err.to_string()) } });
    ext.command("model-runtime", "model runtime", |ctx, _| { let model = json!({"provider":"openrouter","modelId":"org/model/name"}); let request = json!({"messages":[{"role":"user","content":"hello"}]}); let stream = ctx.model_registry().stream(model.clone(), request.clone(), json!({})); let mut types = Vec::new(); while let Some(event) = stream.next() { types.push(event["type"].as_str().unwrap_or_default().to_string()); } if types.join(",") != "start,text_start,text_delta,text_end,done" { return CommandResult::Error(format!("stream events = {types:?}")); } if stream.result().unwrap_or_default()["content"][0]["text"] != "streamed" { return CommandResult::Error("bad stream result".into()); } let simple = ctx.model_registry().stream_simple(model, request, json!({})); let mut simple_types = Vec::new(); while let Some(event) = simple.next() { simple_types.push(event["type"].as_str().unwrap_or_default().to_string()); } if simple_types != types { return CommandResult::Error(format!("simple events = {simple_types:?}")); } if simple.result().unwrap_or_default()["content"][0]["text"] != "streamed" { return CommandResult::Error("bad simple result".into()); } ctx.notify("model=streamed", "info"); CommandResult::Ok });
    ext.on_event("agent_before_settle", false, |ctx, mut data| { let preview = data.get("context").cloned().unwrap_or_default(); ctx.notify(&format!("agent-before-settle={}:{}:{}:{}:{}", data.get("outcome").and_then(|v| v.as_str()).unwrap_or_default(), data.get("entries").and_then(|v| v.as_array()).map_or(0, Vec::len), data.get("continue").and_then(|v| v.as_bool()).unwrap_or(false), preview.get("contextEntries").and_then(|v| v.as_array()).map_or(0, Vec::len), preview.get("canContinue").and_then(|v| v.as_bool()).unwrap_or(false)), "info"); data["entries"].as_array_mut().unwrap().push(json!({"type":"custom","customType":"kept"})); None });
    ext.on_event("agent_before_settle", false, |_, mut data| { data["entries"].as_array_mut().unwrap().push(json!({"type":"custom","customType":"before-error"})); panic!("boundary failed"); });
    ext.on_event("agent_before_settle", false, |_, data| { let entries = data["entries"].as_array().unwrap(); assert!(entries.len() >= 2); assert_eq!(entries[entries.len()-2]["customType"], "kept"); assert_eq!(entries[entries.len()-1]["customType"], "before-error"); assert_eq!(data["context"]["contextEntries"].as_array().unwrap().len(), entries.len()); Some(json!({"entries":[{"type":"custom","customType":"packed-boundary"}],"continue":true})) });
    ext.on_event("session_info_changed", false, |ctx, data| { ctx.notify(&format!("session-event={}", data.get("name").and_then(|v| v.as_str()).unwrap_or_default()), "info"); None });
    ext.on_event("session_before_compact", false, |ctx, data| { ctx.notify(&format!("session-before-compact={}:{}", data.get("reason").and_then(|v| v.as_str()).unwrap_or_default(), data.get("willRetry").and_then(|v| v.as_bool()).unwrap_or(false)), "info"); None });
    ext.on_event("session_compact", false, |ctx, data| { ctx.notify(&format!("session-compact={}:{}:{}", data.get("reason").and_then(|v| v.as_str()).unwrap_or_default(), data.get("willRetry").and_then(|v| v.as_bool()).unwrap_or(false), data.get("fromExtension").and_then(|v| v.as_bool()).unwrap_or(false)), "info"); None });
    ext.on_event("session_compact_failed", false, |ctx, data| { ctx.notify(&format!("session-compact-failed={}:{}:{}:{}:{}", data.get("reason").and_then(|v| v.as_str()).unwrap_or_default(), data.get("errorMessage").and_then(|v| v.as_str()).unwrap_or_default(), data.get("aborted").and_then(|v| v.as_bool()).unwrap_or(false), data.get("willRetry").and_then(|v| v.as_bool()).unwrap_or(false), data.get("fromExtension").and_then(|v| v.as_bool()).unwrap_or(false)), "info"); None });
    ext.on_event("turn_end", false, |ctx, data| { ctx.notify(&format!("turn-end={}:{}", data.get("messageEntryId").and_then(|v| v.as_str()).unwrap_or_default(), data.get("toolResultEntryIds").and_then(|v| v.as_array()).and_then(|ids| ids.first()).and_then(|v| v.as_str()).unwrap_or_default()), "info"); None });
    ext.on_event("context", true, |_, mut data| { data["messages"].as_array_mut().unwrap().remove(0); Some(json!({"messages":data["messages"]})) });
    ext.on_event("context_with_system", true, |_, mut data| { data["messages"][0]["content"]=json!("request-system"); data["messages"][0]["toolsAdded"].as_array_mut().unwrap().remove(0); Some(json!({"messages":data["messages"]})) });
    let prompt_sequence = Arc::new(Mutex::new(0));
    for name in ["ui_prompt_start", "ui_prompt_end"] { let prompt_sequence = prompt_sequence.clone(); ext.on_event(name, false, move |ctx, data| { let sequence = { let mut n = prompt_sequence.lock().unwrap(); let value = *n; *n += 1; value }; let title = data.get("title").and_then(|v| v.as_str()).unwrap_or("(none)"); if title.starts_with("fifo:") { ctx.notify(&format!("fifo:{sequence}:{}:{title}", data["type"].as_str().unwrap()), "info"); return None; } ctx.notify(&format!("ui-prompt={}:{}:{title}", data.get("type").and_then(|v| v.as_str()).unwrap_or_default(), data.get("kind").and_then(|v| v.as_str()).unwrap_or_default()), "info"); None }); }
    ext.on_project_trust(|_, _| Ok(ProjectTrustResult { trusted: ProjectTrustDecision::Undecided, remember: None }));
    ext.on_project_trust(|_, _| Ok(ProjectTrustResult { trusted: ProjectTrustDecision::Yes, remember: Some(true) }));
    ext
}
`, extName, toolName)
	if err := os.WriteFile(filepath.Join(dir, "src", "lib.rs"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func writePackedFactoryModule(t testing.TB, modulePath, extName, toolName string, includeModelRuntime ...bool) string {
	t.Helper()
	t.Setenv("PIG_SDK_GO_ROOT", filepath.Join(findModuleRoot(t), "extensions", "sdk"))
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), fmt.Appendf(nil, "module %s\n\ngo 1.26\n\nrequire github.com/MichaelKinsy/PiG/extensions/sdk v0.0.0\n", modulePath), 0o644); err != nil {
		t.Fatal(err)
	}
	src := fmt.Sprintf(`package ext

import (
    "context"
    "fmt"
    "slices"
    "strings"
    "sync"
    "time"
    "github.com/MichaelKinsy/PiG/extensions/sdk"
)

type focused struct { selected int }
func (f *focused) Render(width int) []string { return []string{fmt.Sprintf("focused width=%%d", width), []string{"> alpha", "  alpha"}[f.selected], []string{"  beta", "> beta"}[f.selected]} }
func (f *focused) HandleInput(data string) (sdk.RemoteComponentResult, error) {
    if data == "\x1b[B" { f.selected = 1 }
    if data == "\x1b[6~" { f.selected = 1 }
    if data == "\r" { return sdk.RemoteComponentResult{Done:true, Value:"beta"}, nil }
    if data == "\x1b" { return sdk.RemoteComponentResult{Done:true}, nil }
    return sdk.RemoteComponentResult{}, nil
}

type timerFocused struct { mu sync.Mutex; frame int; invalidate func(); stop chan struct{}; done chan struct{}; stopped sync.Once; disposed bool }
func newTimerFocused() *timerFocused { c := &timerFocused{stop:make(chan struct{}), done:make(chan struct{})}; go func(){ defer close(c.done); ticker := time.NewTicker(20*time.Millisecond); defer ticker.Stop(); for { select { case <-ticker.C: c.mu.Lock(); c.frame++; invalidate := c.invalidate; c.mu.Unlock(); if invalidate != nil { invalidate() }; case <-c.stop: return } } }(); return c }
func (c *timerFocused) Render(width int) []string { c.mu.Lock(); defer c.mu.Unlock(); return []string{fmt.Sprintf("timer frame=%%d width=%%d", c.frame, width)} }
func (c *timerFocused) HandleInput(data string) (sdk.RemoteComponentResult, error) { c.mu.Lock(); defer c.mu.Unlock(); if data == "\r" { return sdk.RemoteComponentResult{Done:true, Value:c.frame}, nil }; return sdk.RemoteComponentResult{}, nil }
func (c *timerFocused) SetInvalidate(invalidate func()) { c.mu.Lock(); c.invalidate = invalidate; c.mu.Unlock() }
func (c *timerFocused) Dispose() { c.stopped.Do(func(){close(c.stop)}); <-c.done; c.mu.Lock(); c.disposed = true; c.mu.Unlock() }
func (c *timerFocused) lifecycle() (bool, bool) { c.mu.Lock(); defer c.mu.Unlock(); return c.disposed, c.invalidate == nil }

func Extension() *sdk.Extension {
	e := sdk.New(%q)
	e.Tool(%q, "test tool", sdk.Schema{"type":"object"}, func(ctx sdk.Context, params map[string]any) (any, error) {
		return map[string]string{"content":"ok"}, nil
	})
	e.Command("focused", "focused component", func(ctx sdk.Context, _ string) error { value, err := ctx.Custom(&focused{}, nil); if err != nil { return err }; text := ""; if value != nil { text = fmt.Sprint(value) }; ctx.Notify("focused="+text, "info"); return nil })
	e.Command("timer", "timer component", func(ctx sdk.Context, _ string) error { component := newTimerFocused(); value, err := ctx.Custom(component, nil); if err != nil { return err }; disposed, detached := component.lifecycle(); ctx.Notify(fmt.Sprintf("timer=%%v disposed=%%t detached=%%t", value, disposed, detached), "info"); return nil })
	e.Command("send_user_content", "send user content", func(ctx sdk.Context, _ string) error { return ctx.SendUserMessage([]any{map[string]any{"type":"text","text":"first"},map[string]any{"type":"image","data":"aW1hZ2U=","mimeType":"image/png"},map[string]any{"type":"text","text":"second"}}, "steer") })
	e.Command("session", "session actions", func(ctx sdk.Context, _ string) error { active := ctx.GetModelInfo(); if active == nil || active.InputLimits["maxRequestBytes"] != float64(12345) { return fmt.Errorf("packed inputLimits = %%#v", active) }; images, _ := active.InputLimits["images"].(map[string]any); resize, _ := images["resize"].(map[string]any); if resize["maxWidth"] != float64(321) {return fmt.Errorf("packed resize = %%#v",resize)}; if err := ctx.AppendEntry("packed-entry", map[string]any{"value":"hello"}); err != nil { return err }; return ctx.SetSessionName("packed-session") })
	e.Command("model-runtime", "model runtime", func(ctx sdk.Context, _ string) error { model := map[string]any{"provider":"openrouter","modelId":"org/model/name"}; request := map[string]any{"messages":[]any{map[string]any{"role":"user","content":"hello"}}}; stream := ctx.ModelRegistry().Stream(model, request, nil); var types []string; for event := range stream.Events(context.Background()) { types = append(types, fmt.Sprint(event["type"])) }; if strings.Join(types, ",") != "start,text_start,text_delta,text_end,done" { return fmt.Errorf("stream events = %%v", types) }; result := stream.Result(); content, _ := result["content"].([]any); block, _ := content[0].(map[string]any); if fmt.Sprint(block["text"]) != "streamed" { return fmt.Errorf("stream result = %%#v", result) }; simple := ctx.ModelRegistry().StreamSimple(model, request, nil); var simpleTypes []string; for event := range simple.Events(context.Background()) { simpleTypes = append(simpleTypes, fmt.Sprint(event["type"])) }; if !slices.Equal(simpleTypes, types) { return fmt.Errorf("simple events = %%v", simpleTypes) }; if fmt.Sprint(simple.Result()["stopReason"]) != "stop" { return fmt.Errorf("simple result = %%#v", simple.Result()) }; ctx.Notify("model=streamed", "info"); return nil })
	e.OnEvent("agent_before_settle", func(ctx sdk.Context, data map[string]any) (any, error) { entries,_:=data["entries"].([]any); preview,_:=data["context"].(map[string]any); contextEntries,_:=preview["contextEntries"].([]any); ctx.Notify(fmt.Sprintf("agent-before-settle=%%v:%%d:%%v:%%d:%%v", data["outcome"], len(entries), data["continue"], len(contextEntries), preview["canContinue"]), "info"); data["entries"] = append(entries, map[string]any{"type":"custom","customType":"kept"}); return nil,nil })
	e.OnEvent("agent_before_settle", func(_ sdk.Context, data map[string]any) (any, error) { data["entries"] = append(data["entries"].([]any), map[string]any{"type":"custom","customType":"before-error"}); return nil,fmt.Errorf("boundary failed") })
	e.OnEvent("agent_before_settle", func(_ sdk.Context, data map[string]any) (any, error) { entries := data["entries"].([]any); preview := data["context"].(map[string]any); if len(entries)<2 || entries[len(entries)-2].(map[string]any)["customType"] != "kept" || entries[len(entries)-1].(map[string]any)["customType"] != "before-error" || len(preview["contextEntries"].([]any)) != len(entries) { return nil, fmt.Errorf("lost boundary mutation or preview") }; return map[string]any{"entries":[]any{map[string]any{"type":"custom","customType":"packed-boundary"}},"continue":true}, nil })
	e.OnEvent("session_info_changed", func(ctx sdk.Context, data map[string]any) (any, error) { ctx.Notify("session-event="+fmt.Sprint(data["name"]), "info"); return nil, nil })
	e.OnEvent("session_before_compact", func(ctx sdk.Context, data map[string]any) (any, error) { ctx.Notify(fmt.Sprintf("session-before-compact=%%v:%%v", data["reason"], data["willRetry"]), "info"); return nil, nil })
	e.OnEvent("session_compact", func(ctx sdk.Context, data map[string]any) (any, error) { ctx.Notify(fmt.Sprintf("session-compact=%%v:%%v:%%v", data["reason"], data["willRetry"], data["fromExtension"]), "info"); return nil, nil })
	e.OnEvent("session_compact_failed", func(ctx sdk.Context, data map[string]any) (any, error) { ctx.Notify(fmt.Sprintf("session-compact-failed=%%v:%%v:%%v:%%v:%%v", data["reason"], data["errorMessage"], data["aborted"], data["willRetry"], data["fromExtension"]), "info"); return nil, nil })
	e.OnEvent("turn_end", func(ctx sdk.Context, data map[string]any) (any, error) { ids, _ := data["toolResultEntryIds"].([]any); ctx.Notify(fmt.Sprintf("turn-end=%%v:%%v", data["messageEntryId"], ids[0]), "info"); return nil, nil })
 e.OnEvent("context", func(_ sdk.Context, data map[string]any) (any,error) { return map[string]any{"messages":data["messages"].([]any)[1:]},nil })
 e.OnEvent("context_with_system", func(_ sdk.Context, data map[string]any) (any,error) { head:=data["messages"].([]any)[0].(map[string]any); head["content"]="request-system"; head["toolsAdded"]=head["toolsAdded"].([]any)[1:]; return nil,nil })
	var promptMu sync.Mutex
	promptSequence := 0
	for _, name := range []string{"ui_prompt_start", "ui_prompt_end"} { e.OnEvent(name, func(ctx sdk.Context, data map[string]any) (any, error) { promptMu.Lock(); sequence := promptSequence; promptSequence++; promptMu.Unlock(); title := "(none)"; if value, ok := data["title"]; ok { title = fmt.Sprint(value) }; if strings.HasPrefix(title,"fifo:") { ctx.Notify(fmt.Sprintf("fifo:%%d:%%v:%%s",sequence,data["type"],title), "info"); return nil,nil }; ctx.Notify(fmt.Sprintf("ui-prompt=%%v:%%v:%%s", data["type"], data["kind"], title), "info"); return nil, nil }) }
	e.OnProjectTrust(func(sdk.Context, map[string]any) (sdk.ProjectTrustResult, error) { return sdk.ProjectTrustResult{Trusted:sdk.ProjectTrustUndecided}, nil })
	e.OnProjectTrust(func(sdk.Context, map[string]any) (sdk.ProjectTrustResult, error) { return sdk.ProjectTrustResult{Trusted:sdk.ProjectTrustYes, Remember:true}, nil })
	return e
}
`, extName, toolName)
	if len(includeModelRuntime) == 0 || !includeModelRuntime[0] {
		src = strings.Replace(src, "    \"context\"\n", "", 1)
		src = strings.Replace(src, "    \"slices\"\n", "", 1)
		start := strings.Index(src, "\te.Command(\"model-runtime\"")
		if start >= 0 {
			if end := strings.Index(src[start:], "\n"); end >= 0 {
				src = src[:start] + src[start+end+1:]
			}
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "ext.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// Source: runner.ts emitContext runs conversation transforms before full-system
// transforms. Exercise the actual loaded member, including packed routing and
// fused RunWithConn, rather than a mock of its event transport.
func assertRealizedContextTransform(t *testing.T, ext extension.Extension) {
	t.Helper()
	runner := inproc.NewRunner([]extension.Extension{ext}, t.TempDir())
	messages := []extension.AgentMessage{
		map[string]any{"role": "system", "content": "original", "timestamp": 0, "toolsAdded": []any{map[string]any{"name": "drop", "parameters": map[string]any{"type": "object"}}, map[string]any{"name": "keep", "parameters": map[string]any{"type": "object"}}}},
		map[string]any{"role": "user", "content": "old", "timestamp": 0},
		map[string]any{"role": "user", "content": "keep", "timestamp": 0},
	}
	conversation, err := runner.EmitContext(t.Context(), messages[1:])
	if err != nil {
		t.Fatalf("%s context: %v", ext.Path, err)
	}
	withSystem := make([]extension.AgentMessage, 0, len(conversation)+1)
	withSystem = append(withSystem, messages[0])
	withSystem = append(withSystem, conversation...)
	output, err := runner.EmitContextWithSystem(t.Context(), withSystem)
	if err != nil {
		t.Fatalf("%s context_with_system: %v", ext.Path, err)
	}
	encoded, err := json.Marshal(output)
	if err != nil {
		t.Fatal(err)
	}
	var decoded []map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded) != 2 || decoded[0]["role"] != "system" || decoded[0]["content"] != "request-system" || decoded[1]["content"] != "keep" {
		t.Fatalf("%s context = %s", ext.Path, encoded)
	}
	tools := decoded[0]["toolsAdded"].([]any)
	if len(tools) != 1 || tools[0].(map[string]any)["name"] != "keep" {
		t.Fatalf("%s tools = %s", ext.Path, encoded)
	}
	if messages[0].(map[string]any)["content"] != "original" {
		t.Fatal("context changed persisted transcript")
	}
}
