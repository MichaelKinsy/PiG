// These tests cover the adapter from registered extension tools to the agent
// tool interface.

package coding

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// fixtureTool is a minimal extension.RegisteredTool for bridge tests.
// Calling tests can override Execute via the captured fields.
func fixtureTool(name, desc string, params string, exec extension.ToolExecuteFunc) extension.RegisteredTool {
	return extension.RegisteredTool{
		Definition: extension.ToolDefinition{
			Name:        name,
			Description: desc,
			Parameters:  json.RawMessage(params),
			Execute:     exec,
		},
	}
}

// fixtureExtension wraps a single tool into an extension.Extension
// state container (the shape NewRuntime accepts via NewExtensions).
func fixtureExtension(path string, tools ...extension.RegisteredTool) extension.Extension {
	tm := make(map[string]extension.RegisteredTool, len(tools))
	for _, t := range tools {
		tm[t.Definition.Name] = t
	}
	return extension.Extension{
		Path:         path,
		ResolvedPath: path,
		Tools:        tm,
	}
}

// TestBridgeTool_NameAndSchema verifies that bridge construction parses the
// schema once and preserves tool identity.
func TestBridgeTool_NameAndSchema(t *testing.T) {
	rt := fixtureTool("greet", "say hi",
		`{"type":"object","properties":{"name":{"type":"string"}}}`, nil)
	bt, err := newBridgeTool(rt)
	if err != nil {
		t.Fatal(err)
	}
	if bt.Name() != "greet" {
		t.Errorf("Name() = %q, want greet", bt.Name())
	}
	s := bt.Schema()
	if s.Name != "greet" || s.Description != "say hi" {
		t.Errorf("Schema name/desc = %q/%q", s.Name, s.Description)
	}
	if s.Parameters["type"] != "object" {
		t.Errorf("Parameters not parsed: %v", s.Parameters)
	}
}

// TestBridgeTool_ConstrainedSamplingGrammar locks the producer path: a tool
// declared with a grammar constrained-sampling request surfaces that request on
// the ai.ToolSchema the agent loop hands to the provider (which the openai
// consumers turn into a custom grammar tool). Without this threading no
// extension can reach the constrained-sampling wire behavior.
func TestBridgeTool_ConstrainedSamplingGrammar(t *testing.T) {
	rt := fixtureTool("calc", "arithmetic", `{"type":"object"}`, nil)
	rt.Definition.ConstrainedSampling = json.RawMessage(
		`{"type":"grammar","variants":{"openai_lark":"start: NUMBER"}}`)
	bt, err := newBridgeTool(rt)
	if err != nil {
		t.Fatal(err)
	}
	got := bt.Schema().ConstrainedSampling
	if got == nil {
		t.Fatal("Schema().ConstrainedSampling = nil, want grammar config")
	}
	if got.Type != "grammar" || got.Variants["openai_lark"] != "start: NUMBER" {
		t.Fatalf("ConstrainedSampling = %+v, want grammar/openai_lark", got)
	}
}

// TestBridgeTool_ConstrainedSamplingDisabled locks that an absent, false, or
// null request all disable constrained sampling (upstream's `false` is
// equivalent to undefined).
func TestBridgeTool_ConstrainedSamplingDisabled(t *testing.T) {
	for _, raw := range []string{"", "false", "null"} {
		rt := fixtureTool("t", "d", `{"type":"object"}`, nil)
		if raw != "" {
			rt.Definition.ConstrainedSampling = json.RawMessage(raw)
		}
		bt, err := newBridgeTool(rt)
		if err != nil {
			t.Fatalf("raw %q: %v", raw, err)
		}
		if got := bt.Schema().ConstrainedSampling; got != nil {
			t.Fatalf("raw %q: ConstrainedSampling = %+v, want nil", raw, got)
		}
	}
}

// TestBridgeTool_ConstrainedSamplingMalformed locks that a non-null, non-false
// value that is not a valid config is surfaced as an error rather than silently
// dropped.
func TestBridgeTool_ConstrainedSamplingMalformed(t *testing.T) {
	rt := fixtureTool("t", "d", `{"type":"object"}`, nil)
	rt.Definition.ConstrainedSampling = json.RawMessage(`{"type":`)
	if _, err := newBridgeTool(rt); err == nil {
		t.Fatal("expected error for malformed constrained_sampling")
	}
}

// TestBridgeTool_MalformedSchemaReturnsError locks the
// fail-at-bridge-time contract.
func TestBridgeTool_MalformedSchemaReturnsError(t *testing.T) {
	rt := fixtureTool("broken", "x", `{not valid json`, nil)
	if _, err := newBridgeTool(rt); err == nil {
		t.Fatal("expected error for malformed schema, got nil")
	}
}

func TestBridgeToolPrepareArguments(t *testing.T) {
	rt := fixtureTool("prepared", "x", `{}`, nil)
	rt.Definition.PrepareArguments = func(raw json.RawMessage) (json.RawMessage, error) {
		return append(json.RawMessage(nil), raw...), nil
	}
	bt, err := newBridgeTool(rt)
	if err != nil {
		t.Fatal(err)
	}
	input := json.RawMessage(`{"legacy":true}`)
	got, err := bt.PrepareArguments(input)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(input) {
		t.Fatalf("PrepareArguments = %s, want %s", got, input)
	}
}

// TestBridgeTool_Execute_RoundTripResult locks the type-assertion
// contract: Execute must accept tools that return
// `agent.AgentToolResult` (the pig-native shape).
func TestBridgeTool_Execute_RoundTripResult(t *testing.T) {
	want := agent.AgentToolResult{Content: "ok", IsError: false}
	rt := fixtureTool("t", "x", `{}`, func(_ context.Context, _ string, _ json.RawMessage, _ any) (any, error) {
		return want, nil
	})
	bt, _ := newBridgeTool(rt)
	got, err := bt.Execute(context.Background(), "call-1", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.Content != want.Content || got.IsError != want.IsError {
		t.Errorf("Execute returned %+v, want %+v", got, want)
	}
}

// TestBridgeTool_Execute_NilResultIsZeroValue locks that nil returns
// are accepted (legacy idiom: some builtins return (nil, nil) for
// success-with-empty-payload).
func TestBridgeTool_Execute_NilResultIsZeroValue(t *testing.T) {
	rt := fixtureTool("t", "x", `{}`, func(_ context.Context, _ string, _ json.RawMessage, _ any) (any, error) {
		return nil, nil
	})
	bt, _ := newBridgeTool(rt)
	got, err := bt.Execute(context.Background(), "c", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.Content != "" || got.IsError || len(got.Images) != 0 {
		t.Errorf("got %+v, want zero value", got)
	}
}

// TestBridgeTool_Execute_WrongResultTypeIsError locks the boundary
// guard: D1 erases the result type but tools that return the wrong
// concrete shape produce an actionable error rather than a panic.
func TestBridgeTool_Execute_WrongResultTypeIsError(t *testing.T) {
	rt := fixtureTool("t", "x", `{}`, func(_ context.Context, _ string, _ json.RawMessage, _ any) (any, error) {
		return "not a tool result", nil
	})
	bt, _ := newBridgeTool(rt)
	_, err := bt.Execute(context.Background(), "c", nil, nil)
	if err == nil {
		t.Fatal("expected error for wrong result type, got nil")
	}
}

// TestBridgeTool_ExecutionMode locks the parallel/sequential string
// mapping. The empty string and any unrecognized value default to
// sequential, matching upstream and the legacy adapter.
func TestBridgeTool_ExecutionMode(t *testing.T) {
	cases := []struct {
		raw  string
		want agent.ToolExecutionMode
	}{
		{"", agent.ToolModeSequential},
		{"sequential", agent.ToolModeSequential},
		{"parallel", agent.ToolModeParallel},
		{"unknown-future-value", agent.ToolModeSequential},
	}
	for _, c := range cases {
		t.Run(c.raw, func(t *testing.T) {
			rt := extension.RegisteredTool{
				Definition: extension.ToolDefinition{
					Name:          "t",
					Parameters:    json.RawMessage(`{}`),
					ExecutionMode: c.raw,
				},
			}
			bt, _ := newBridgeTool(rt)
			if got := bt.ExecutionMode(); got != c.want {
				t.Errorf("ExecutionMode(%q) = %q, want %q", c.raw, got, c.want)
			}
		})
	}
}

// TestBridgeTool_ExecutePropagatesError locks that errors from the
// underlying ToolExecuteFunc reach the agent loop verbatim.
func TestBridgeTool_ExecutePropagatesError(t *testing.T) {
	want := errors.New("boom")
	rt := fixtureTool("t", "x", `{}`, func(_ context.Context, _ string, _ json.RawMessage, _ any) (any, error) {
		return nil, want
	})
	bt, _ := newBridgeTool(rt)
	_, err := bt.Execute(context.Background(), "c", nil, nil)
	if !errors.Is(err, want) {
		t.Errorf("Execute returned err=%v, want %v", err, want)
	}
}

// TestBridgeNewRunnerTools_DropsMalformed confirms the bridge skips
// tools whose schema fails to parse rather than failing the whole
// session. The diagnostic is returned for callers who want to log it.
func TestBridgeNewRunnerTools_DropsMalformed(t *testing.T) {
	tools := []extension.RegisteredTool{
		fixtureTool("ok", "x", `{}`, nil),
		fixtureTool("bad", "x", `{not valid`, nil),
	}
	bridged, diags := BridgeNewRunnerTools(tools)
	if len(bridged) != 1 || bridged[0].Name() != "ok" {
		t.Errorf("bridged tools = %v, want [ok]", names(bridged))
	}
	if len(diags) != 1 {
		t.Errorf("diags = %d, want 1", len(diags))
	}
}

// TestRuntime_StartSession_MergesNewToolsWithLegacy verifies that a Session
// includes tools registered through coding/extension.
func TestRuntime_StartSession_MergesNewToolsWithLegacy(t *testing.T) {
	svcs := newTestServices(t)
	exts := []extension.Extension{
		fixtureExtension("/fixture/x",
			fixtureTool("hello", "greet", `{"type":"object"}`, nil),
		),
	}
	rt, err := NewRuntime(RuntimeOptions{Services: svcs, NewExtensions: exts})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rt.Close() }()

	sess, err := rt.New(SessionStartOptions{Model: fakeModel()})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sess.Close() }()

	// Inspect the session's tools through its public surface.
	gotNames := map[string]bool{}
	for _, tool := range sess.Tools() {
		gotNames[tool.Name()] = true
	}
	if !gotNames["hello"] {
		t.Errorf("session tools missing extension tool 'hello'; got %v", gotNames)
	}
}

// TestRuntime_StartSession_SkipExtensionToolsAlsoSkipsBridge verifies that
// SkipExtensionTools excludes tools registered through coding/extension.
func TestRuntime_StartSession_SkipExtensionToolsAlsoSkipsBridge(t *testing.T) {
	svcs := newTestServices(t)
	exts := []extension.Extension{
		fixtureExtension("/fixture/x",
			fixtureTool("hello", "greet", `{"type":"object"}`, nil),
		),
	}
	rt, err := NewRuntime(RuntimeOptions{Services: svcs, NewExtensions: exts})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rt.Close() }()

	sess, err := rt.New(SessionStartOptions{
		Model:              fakeModel(),
		SkipExtensionTools: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sess.Close() }()

	for _, tool := range sess.Tools() {
		if tool.Name() == "hello" {
			t.Errorf("session leaked 'hello' through bridge despite SkipExtensionTools=true")
		}
	}
}

var _ agent.AgentTool = (*bridgeTool)(nil)
var _ agent.ArgumentPreparer = (*bridgeTool)(nil)

func names(ts []agent.AgentTool) []string {
	out := make([]string, len(ts))
	for i, t := range ts {
		out[i] = t.Name()
	}
	return out
}
