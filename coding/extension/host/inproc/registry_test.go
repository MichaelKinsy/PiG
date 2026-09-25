package inproc_test

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// extWithTool produces a fake extension at the given path, populated with
// one tool whose definition.Name = toolName. The Tools map keys by the
// tool name (matching upstream: see types.ts:1513-1523 commentary on
// how the loader populates this map).
func extWithTool(path, toolName, description string) extension.Extension {
	ext := newFakeExtension(path)
	ext.Tools[toolName] = extension.RegisteredTool{
		Definition: extension.ToolDefinition{
			Name:        toolName,
			Description: description,
			Parameters:  json.RawMessage(`{"type":"object"}`),
		},
	}
	return ext
}

// extWithHandler produces a fake extension at the given path, populated
// with one handler for the given event type. Handler is a no-op stub.
func extWithHandler(path, eventType string) extension.Extension {
	ext := newFakeExtension(path)
	ext.Handlers[eventType] = []extension.HandlerFn{
		func(args ...any) (any, error) { return nil, nil },
	}
	return ext
}

// ─── Tools() ──────────────────────────────────────────────────────────────

// TestTools_Empty: no extensions ⇒ empty slice.
func TestTools_Empty(t *testing.T) {
	r := inproc.NewRunner(nil, ".")
	if got := r.Tools(); len(got) != 0 {
		t.Errorf("Tools() len = %d, want 0", len(got))
	}
}

// TestTools_AggregatesAcrossExtensions: one tool per extension surfaces
// all of them in the result.
func TestTools_AggregatesAcrossExtensions(t *testing.T) {
	exts := []extension.Extension{
		extWithTool("/ext/a", "tool_a", "first"),
		extWithTool("/ext/b", "tool_b", "second"),
		extWithTool("/ext/c", "tool_c", "third"),
	}
	r := inproc.NewRunner(exts, ".")

	got := r.Tools()
	names := make([]string, len(got))
	for i, tool := range got {
		names[i] = tool.Definition.Name
	}
	slices.Sort(names) // map iteration on a single-tool extension is deterministic, but be explicit
	want := []string{"tool_a", "tool_b", "tool_c"}
	if !slices.Equal(names, want) {
		t.Errorf("Tools() names = %v, want %v", names, want)
	}
}

// TestTools_FirstWinsOnNameCollision: when two extensions register a
// tool with the same name, the earlier-loaded extension's registration
// is kept. Mirrors upstream runner.ts:374 (`if (!toolsByName.has(...))`).
func TestTools_FirstWinsOnNameCollision(t *testing.T) {
	exts := []extension.Extension{
		extWithTool("/ext/first", "shared_name", "from first"),
		extWithTool("/ext/second", "shared_name", "from second (should be hidden)"),
	}
	r := inproc.NewRunner(exts, ".")

	got := r.Tools()
	if len(got) != 1 {
		t.Fatalf("Tools() len = %d, want 1 (dedup)", len(got))
	}
	if got[0].Definition.Description != "from first" {
		t.Errorf("description = %q, want %q (first-wins)", got[0].Definition.Description, "from first")
	}
}

// TestTools_DoesNotDedupAcrossDifferentNames: two extensions registering
// tools with different names ⇒ both surface (no false dedup).
func TestTools_DoesNotDedupAcrossDifferentNames(t *testing.T) {
	exts := []extension.Extension{
		extWithTool("/ext/a", "tool_x", "x"),
		extWithTool("/ext/b", "tool_y", "y"),
	}
	r := inproc.NewRunner(exts, ".")

	if got := len(r.Tools()); got != 2 {
		t.Errorf("Tools() len = %d, want 2", got)
	}
}

// ─── GetToolDefinition() ──────────────────────────────────────────────────

// TestGetToolDefinition_Found: lookup finds the tool, ok = true.
func TestGetToolDefinition_Found(t *testing.T) {
	exts := []extension.Extension{
		extWithTool("/ext/a", "my_tool", "descr"),
	}
	r := inproc.NewRunner(exts, ".")

	def, ok := r.GetToolDefinition("my_tool")
	if !ok {
		t.Fatalf("GetToolDefinition(\"my_tool\") ok = false, want true")
	}
	if def.Name != "my_tool" || def.Description != "descr" {
		t.Errorf("def = %+v; want Name=\"my_tool\" Description=\"descr\"", def)
	}
}

// TestGetToolDefinition_NotFound: lookup returns zero value + ok=false.
func TestGetToolDefinition_NotFound(t *testing.T) {
	exts := []extension.Extension{
		extWithTool("/ext/a", "tool_a", "a"),
	}
	r := inproc.NewRunner(exts, ".")

	def, ok := r.GetToolDefinition("nonexistent")
	if ok {
		t.Errorf("GetToolDefinition(\"nonexistent\") ok = true, want false")
	}
	if def.Name != "" {
		t.Errorf("not-found def should be zero value; got Name=%q", def.Name)
	}
}

// TestGetToolDefinition_FirstWins: same dedup semantics as Tools().
// Lookup returns the earlier-loaded extension's tool.
func TestGetToolDefinition_FirstWins(t *testing.T) {
	exts := []extension.Extension{
		extWithTool("/ext/first", "shared", "from first"),
		extWithTool("/ext/second", "shared", "from second"),
	}
	r := inproc.NewRunner(exts, ".")

	def, ok := r.GetToolDefinition("shared")
	if !ok {
		t.Fatalf("GetToolDefinition(\"shared\") ok = false")
	}
	if !strings.Contains(def.Description, "first") {
		t.Errorf("description = %q, want to contain \"first\" (first-wins)", def.Description)
	}
}

// ─── HasHandlers() ────────────────────────────────────────────────────────

// TestHasHandlers_NoExtensions: returns false.
func TestHasHandlers_NoExtensions(t *testing.T) {
	r := inproc.NewRunner(nil, ".")
	if r.HasHandlers("tool_call") {
		t.Errorf("HasHandlers on empty runner = true, want false")
	}
}

// TestHasHandlers_ExtensionWithoutHandlerForEvent: an extension exists
// but has no handler for this event type ⇒ false.
func TestHasHandlers_ExtensionWithoutHandlerForEvent(t *testing.T) {
	exts := []extension.Extension{
		extWithHandler("/ext/a", "session_start"),
	}
	r := inproc.NewRunner(exts, ".")

	if r.HasHandlers("tool_call") {
		t.Errorf("HasHandlers(\"tool_call\") = true, want false (no extension handles it)")
	}
	if !r.HasHandlers("session_start") {
		t.Errorf("HasHandlers(\"session_start\") = false, want true")
	}
}

// TestHasHandlers_EmptyHandlerSliceCountsAsAbsent: an extension where
// the Handlers map has the key but the slice is empty must return
// false. Mirrors upstream `handlers.length > 0` check (runner.ts:489).
func TestHasHandlers_EmptyHandlerSliceCountsAsAbsent(t *testing.T) {
	ext := newFakeExtension("/ext/a")
	ext.Handlers["tool_call"] = []extension.HandlerFn{} // present but empty
	r := inproc.NewRunner([]extension.Extension{ext}, ".")

	if r.HasHandlers("tool_call") {
		t.Errorf("HasHandlers with empty slice = true, want false (upstream runner.ts:489 length>0 check)")
	}
}

// TestHasHandlers_AcrossMultipleExtensions: any one extension having a
// handler for the event type ⇒ true (short-circuit).
func TestHasHandlers_AcrossMultipleExtensions(t *testing.T) {
	exts := []extension.Extension{
		extWithHandler("/ext/a", "session_start"),
		extWithHandler("/ext/b", "tool_call"),
		extWithHandler("/ext/c", "session_shutdown"),
	}
	r := inproc.NewRunner(exts, ".")

	for _, eventType := range []string{"session_start", "tool_call", "session_shutdown"} {
		if !r.HasHandlers(eventType) {
			t.Errorf("HasHandlers(%q) = false, want true", eventType)
		}
	}
	if r.HasHandlers("nonexistent_event") {
		t.Errorf("HasHandlers(\"nonexistent_event\") = true, want false")
	}
}

// TestStubSurfaces_OnlyExpectedAreStubbed: \u03b5.2c lights up Commands,
// Flags, Shortcuts (in addition to \u03b5.2b's Tools, GetToolDefinition,
// HasHandlers). The remaining stub is MessageRenderer for \u03b5.2e.
//
// This test pins the contract progression: each sub-row converts a
// specific subset of stubs to real impls; an accidental conversion
// would break this assertion.
func TestStubSurfaces_OnlyExpectedAreStubbed(t *testing.T) {
	exts := []extension.Extension{
		extWithTool("/ext/a", "tool_a", "a"),
	}
	r := inproc.NewRunner(exts, ".")

	// Now-real surfaces (\u03b5.2b + \u03b5.2c):
	if got := len(r.Tools()); got != 1 {
		t.Errorf("Tools() len = %d, want 1 (\u03b5.2b real impl)", got)
	}
	if _, ok := r.GetToolDefinition("tool_a"); !ok {
		t.Errorf("GetToolDefinition: tool not found (\u03b5.2b real impl)")
	}
	if r.Commands() == nil {
		t.Errorf("Commands() = nil, want non-nil empty slice (\u03b5.2c real impl)")
	}
	if r.Flags() == nil {
		t.Errorf("Flags() = nil, want non-nil empty map (\u03b5.2c real impl)")
	}
	if r.Shortcuts(nil) == nil {
		t.Errorf("Shortcuts() = nil, want non-nil empty map (\u03b5.2c real impl)")
	}

	// Still-stub surface (\u03b5.2e):
	if got := r.MessageRenderer("any"); got != nil {
		t.Errorf("MessageRenderer() = %v, want nil (still stub for \u03b5.2e)", got)
	}
}
