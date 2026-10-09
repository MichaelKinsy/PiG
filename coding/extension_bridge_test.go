// These tests cover the adapter from registered extension tools to the agent
// tool interface.

package coding

// pi: packages/coding-agent/src/core/extensions/wrapper.ts

// pi: packages/coding-agent/src/core/tools/tool-definition-wrapper.ts

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
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
	want := agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "ok"}}, IsError: false}
	rt := fixtureTool("t", "x", `{}`, func(_ context.Context, _ string, _ json.RawMessage, _ extension.AgentToolUpdateCallback) (extension.AgentToolResult, error) {
		return want, nil
	})
	bt, _ := newBridgeTool(rt)
	got, err := bt.Execute(context.Background(), "call-1", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.Text() != want.Text() || got.IsError != want.IsError {
		t.Errorf("Execute returned %+v, want %+v", got, want)
	}
}

// TestBridgeTool_Execute_ZeroResultPassesThrough locks that a tool returning the zero AgentToolResult
// (success with an empty payload) reaches the agent loop unchanged.
func TestBridgeTool_Execute_ZeroResultPassesThrough(t *testing.T) {
	rt := fixtureTool("t", "x", `{}`, func(_ context.Context, _ string, _ json.RawMessage, _ extension.AgentToolUpdateCallback) (extension.AgentToolResult, error) {
		return extension.AgentToolResult{}, nil
	})
	bt, _ := newBridgeTool(rt)
	got, err := bt.Execute(context.Background(), "c", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.Text() != "" || got.IsError || len(got.Images()) != 0 {
		t.Errorf("got %+v, want zero value", got)
	}
}

// .upstream/v0.87.1/packages/agent/src/agent-loop.ts:514 selects sequential execution only for the explicit literal; omitted and unknown values are parallel.
// Pi: packages/coding-agent/src/core/extensions/types.ts:626 (ToolDefinition.executionMode).
func TestBridgeTool_ExecutionMode(t *testing.T) {
	cases := []struct {
		raw  string
		want agent.ToolExecutionMode
	}{
		{"", agent.ToolModeParallel},
		{"sequential", agent.ToolModeSequential},
		{"parallel", agent.ToolModeParallel},
		{"unknown-future-value", agent.ToolModeParallel},
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
	rt := fixtureTool("t", "x", `{}`, func(_ context.Context, _ string, _ json.RawMessage, _ extension.AgentToolUpdateCallback) (extension.AgentToolResult, error) {
		return extension.AgentToolResult{}, want
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

// A tool registered with `constrainedSampling: false` keeps the explicit false in its transcript declaration (transcript.ts:123-129).
func TestBridgeToolKeepsExplicitConstrainedSamplingFalse(t *testing.T) {
	for raw, want := range map[string]bool{`false`: true, ``: false, `null`: false} {
		tool, err := newBridgeTool(extension.RegisteredTool{Definition: extension.ToolDefinition{Name: "t", Parameters: json.RawMessage(`{"type":"object"}`), ConstrainedSampling: json.RawMessage(raw)}})
		if err != nil {
			t.Fatal(err)
		}
		if got := tool.Schema().ConstrainedSamplingDisabled; got != want {
			t.Errorf("constrained_sampling %q: disabled = %t, want %t", raw, got, want)
		}
	}
}

// types.ts ToolDefinition.execute(toolCallId, params, signal, onUpdate: AgentToolUpdateCallback, ctx): AgentToolResult. The agent loop hands the bridge tool its update
// callback (agent-loop.ts:778-786) and the definition's Execute streams partial AgentToolResults through that callback before it returns its AgentToolResult.
func TestBridgeToolForwardsTheAgentsUpdateCallbackAndReturnsTheDefinitionsResult(t *testing.T) {
	partial := agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "partial"}}, Details: map[string]any{"n": 1}}
	final := agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "final"}}, IsError: true}
	rt := fixtureTool("t", "x", `{}`, func(_ context.Context, _ string, _ json.RawMessage, onUpdate extension.AgentToolUpdateCallback) (extension.AgentToolResult, error) {
		onUpdate(partial)
		return final, nil
	})
	bt, _ := newBridgeTool(rt)
	var updates []agent.AgentToolResult
	got, err := bt.Execute(context.Background(), "call-1", nil, func(p agent.AgentToolResult) { updates = append(updates, p) })
	if err != nil {
		t.Fatal(err)
	}
	if len(updates) != 1 || updates[0].Text() != "partial" || updates[0].Details.(map[string]any)["n"] != 1 {
		t.Errorf("updates = %+v, want the one partial result", updates)
	}
	if got.Text() != "final" || !got.IsError {
		t.Errorf("result = %+v, want the definition's final result", got)
	}
}

// wrapper.ts:17-29 wrapRegisteredTool(s) gives every call of a registered tool its runner's createToolContext(toolCallId, signal), in registration order;
// the Session builds its extension tools through it (agent-session.ts:3547), so a tool run through a real Session registry sees the same context.
func TestWrapRegisteredToolsGiveEachCallTheRunnersToolContext(t *testing.T) {
	runner := inproc.NewRunner(nil, t.TempDir())
	var seen []string
	record := func(label string) extension.ToolExecuteFunc {
		return func(ctx context.Context, id string, _ json.RawMessage, _ extension.AgentToolUpdateCallback) (extension.AgentToolResult, error) {
			if extension.ToolContextFromContext(ctx) == nil || extension.FromContext(ctx) == nil {
				t.Errorf("%s call %q ran without the runner's context", label, id)
			}
			seen = append(seen, label+":"+id)
			return agent.AgentToolResult{}, nil
		}
	}
	tools, err := WrapRegisteredTools([]extension.RegisteredTool{
		fixtureTool("first", "d", `{"type":"object"}`, record("first")),
		fixtureTool("second", "d", `{"type":"object"}`, record("second")),
	}, runner)
	if err != nil || len(tools) != 2 || tools[0].Name() != "first" || tools[1].Name() != "second" {
		t.Fatalf("WrapRegisteredTools = %v, %v", tools, err)
	}
	for i, tool := range tools {
		if _, err := tool.Execute(t.Context(), "c"+string(rune('1'+i)), json.RawMessage(`{}`), nil); err != nil {
			t.Fatal(err)
		}
	}
	if want := []string{"first:c1", "second:c2"}; !slices.Equal(seen, want) {
		t.Fatalf("calls = %v, want %v", seen, want)
	}
	if _, err := WrapRegisteredTools([]extension.RegisteredTool{fixtureTool("bad", "d", `{`, nil)}, runner); err == nil {
		t.Fatal("a tool whose schema does not parse was wrapped")
	}
}
