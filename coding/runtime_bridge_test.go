// Runtime bridge tests cover runner construction, extension loading, and
// invalidation during Runtime.Close.

package coding

import (
	"context"
	"errors"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// TestRuntime_NewExtensionRunner_NonNilWithEmptyExtensions confirms that a
// Runtime owns a runner even when no extensions are configured.
func TestRuntime_NewExtensionRunner_NonNilWithEmptyExtensions(t *testing.T) {
	svcs := newTestServices(t)
	rt, err := NewRuntime(RuntimeOptions{Services: svcs})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rt.Close() }()

	r := rt.NewExtensionRunner()
	if r == nil {
		t.Fatal("NewExtensionRunner() returned nil")
	}
	if r.IsStale() {
		t.Errorf("freshly-constructed runner should not be stale")
	}
	if got := r.ExtensionPaths(); len(got) != 0 {
		t.Errorf("ExtensionPaths() = %v, want empty", got)
	}
}

// TestRuntime_NewExtensionRunner_LoadsPassedExtensions confirms the
// list passed via NewExtensions reaches the runner. Locks the
// integration point that extension loading depends on: subprocess
// extensions discovered at startup are passed here.
func TestRuntime_NewExtensionRunner_LoadsPassedExtensions(t *testing.T) {
	svcs := newTestServices(t)
	exts := []extension.Extension{
		{Path: "/fixture/a", ResolvedPath: "/fixture/a"},
		{Path: "/fixture/b", ResolvedPath: "/fixture/b"},
	}
	rt, err := NewRuntime(RuntimeOptions{
		Services:      svcs,
		NewExtensions: exts,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rt.Close() }()

	got := rt.NewExtensionRunner().ExtensionPaths()
	if len(got) != 2 || got[0] != "/fixture/a" || got[1] != "/fixture/b" {
		t.Errorf("ExtensionPaths() = %v, want [/fixture/a /fixture/b]", got)
	}
}

// TestRuntime_Close_InvalidatesNewRunner confirms Close() invalidates
// the new-style runner. Mirrors the legacy AbortFunc semantic so any
// extension.Context references captured before Close reject calls.
func TestRuntime_Close_InvalidatesNewRunner(t *testing.T) {
	svcs := newTestServices(t)
	rt, err := NewRuntime(RuntimeOptions{Services: svcs})
	if err != nil {
		t.Fatal(err)
	}
	r := rt.NewExtensionRunner()
	if r.IsStale() {
		t.Fatal("runner stale before Close")
	}

	if err := rt.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if !r.IsStale() {
		t.Errorf("runner not stale after Close()")
	}
	if msg := r.StaleMessage(); msg != "runtime closed" {
		t.Errorf("StaleMessage = %q, want %q", msg, "runtime closed")
	}

	// And: any dispatch through the runner now returns the canonical
	// stale-context sentinel.
	_, err = r.EmitToolCall(context.Background(), extension.BashToolCallEvent{})
	if !errors.Is(err, extension.ErrStaleContext) {
		t.Errorf("EmitToolCall after Close: got err=%v, want errors.Is(extension.ErrStaleContext)", err)
	}
}

// TestRuntime_ToolsSurfaceThroughBridge verifies that registered extension
// tools reach the agent loop through Runtime.startSession.
func TestRuntime_ToolsSurfaceThroughBridge(t *testing.T) {
	svcs := newTestServices(t)

	// Build a fixture extension that registers two tools.
	fixtureExt := extension.Extension{
		Path:         "/fixture/tool-ext",
		ResolvedPath: "/fixture/tool-ext",
		Tools: map[string]extension.RegisteredTool{
			"test_tool_a": {Definition: extension.ToolDefinition{Name: "test_tool_a", Description: "fixture tool A"}},
			"test_tool_b": {Definition: extension.ToolDefinition{Name: "test_tool_b", Description: "fixture tool B"}},
		},
	}

	rt, err := NewRuntime(RuntimeOptions{
		Services:      svcs,
		NewExtensions: []extension.Extension{fixtureExt},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rt.Close() }()

	// Bridge the tools.
	runner := rt.NewExtensionRunner()
	rts := runner.Tools()
	bridged, diags := BridgeNewRunnerTools(rts)
	if len(diags) > 0 {
		t.Fatalf("bridge diagnostics: %v", diags)
	}

	// Verify both tools surface.
	got := map[string]bool{}
	for _, tool := range bridged {
		got[tool.Name()] = true
	}
	for _, want := range []string{"test_tool_a", "test_tool_b"} {
		if !got[want] {
			t.Errorf("tool %q not found in bridged tools (got: %v)", want, got)
		}
	}
}
