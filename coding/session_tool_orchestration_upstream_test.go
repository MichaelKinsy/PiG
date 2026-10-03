package coding

// Ports .upstream/v0.99.1/packages/coding-agent/test/suite/agent-session-tool-orchestration.test.ts (3 cases).
// Case 2 ("registers codemode and tool_search inactive until they are named", :115-131) is TestSessionToolOrchestrationRegistersCodemodeAndToolSearchInactiveUntilNamed
// over the native builtin:codemode and builtin:tool-search extensions. TestToolsWithDefaultActiveFalseAreActiveOnlyWhenNamed guards the same rule with synthetic tools.

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/builtin"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

func orchestrationTool(name, description string, definition extension.ToolDefinition) extension.RegisteredTool {
	definition.Name, definition.Label, definition.Description = name, name, description
	if definition.Parameters == nil {
		definition.Parameters = json.RawMessage(`{"type":"object","properties":{}}`)
	}
	return extension.RegisteredTool{Definition: definition}
}

func textOutcome(outcome extension.AgentToolCallOutcome) string {
	result, _ := outcome.Result.(agent.AgentToolResult)
	if len(result.Content) == 0 {
		return ""
	}
	text, _ := result.Content[0].(ai.TextContent)
	return text.Text
}

// orchestratorExtension is upstream's `orchestratorExtension` (:14-53): a tool that calls other tools, built only on the extension API.
func orchestratorExtension(toolCalls *[]string, mu *sync.Mutex) extension.Extension {
	echo := orchestrationTool("echo", "Echo text.", extension.ToolDefinition{
		Parameters: json.RawMessage(`{"type":"object","properties":{"text":{"type":"string"}},"required":["text"]}`),
		Execute: func(_ context.Context, _ string, params json.RawMessage, _ extension.AgentToolUpdateCallback) (extension.AgentToolResult, error) {
			var input struct{ Text string }
			_ = json.Unmarshal(params, &input)
			return textResult("echo: " + input.Text), nil
		},
	})
	helper := orchestrationTool("helper", "Only reachable from other tools.", extension.ToolDefinition{
		Exposure: extension.ToolExposureCodemode,
		Execute: func(context.Context, string, json.RawMessage, extension.AgentToolUpdateCallback) (extension.AgentToolResult, error) {
			return textResult("helped"), nil
		},
	})
	runTools := orchestrationTool("run_tools", "Runs tools.", extension.ToolDefinition{
		Exposure: extension.ToolExposureModelOnly,
		PrepareLoadout: func(loadout extension.ToolLoadout) *extension.ToolLoadoutChanges {
			names := make([]string, len(loadout.Callable))
			for i, tool := range loadout.Callable {
				names[i] = tool.Name
			}
			return &extension.ToolLoadoutChanges{
				Descriptions:       map[string]string{"run_tools": "Runs tools: " + strings.Join(names, ", "), "echo": "Echo text (also callable from run_tools)."},
				HiddenDeclarations: []string{"echo"},
			}
		},
		Execute: func(ctx context.Context, _ string, _ json.RawMessage, _ extension.AgentToolUpdateCallback) (extension.AgentToolResult, error) {
			tc := extension.ToolContextFromContext(ctx)
			helperOutcome, err := tc.ExecuteTool("helper", map[string]any{}, nil)
			if err != nil {
				return nil, err
			}
			echoOutcome, err := tc.ExecuteTool("echo", map[string]any{"text": "hi"}, nil)
			if err != nil {
				return nil, err
			}
			selfOutcome, err := tc.ExecuteTool("run_tools", map[string]any{}, nil)
			if err != nil {
				return nil, err
			}
			return textResult(strings.Join([]string{textOutcome(helperOutcome), textOutcome(echoOutcome), textOutcome(selfOutcome)}, " | ")), nil
		},
	})
	return extension.Extension{
		Tools:     map[string]extension.RegisteredTool{"echo": echo, "helper": helper, "run_tools": runTools},
		ToolOrder: []string{"echo", "helper", "run_tools"},
		Handlers: map[string][]extension.HandlerFn{"tool_call": {func(args ...any) (any, error) {
			event := args[0].(extension.CustomToolCallEvent)
			parent := event.ParentToolCallID
			if parent == "" {
				parent = "top"
			}
			mu.Lock()
			*toolCalls = append(*toolCalls, event.ToolName+":"+parent)
			mu.Unlock()
			return nil, nil
		}}},
	}
}

func newOrchestrationSession(t *testing.T, ext extension.Extension) (*Session, virtualFaux) {
	t.Helper()
	services := newTestServices(t)
	faux := ai.NewFauxProvider(ai.FauxConfig{ProviderID: "faux", Model: "faux-1"})
	if err := services.ModelRuntime().RegisterNativeProvider(faux.Provider()); err != nil {
		t.Fatal(err)
	}
	if err := services.Auth().Set("faux", ai.Credential{Type: ai.CredentialAPIKey, Key: "faux-key"}); err != nil {
		t.Fatal(err)
	}
	runner := inproc.NewRunner([]extension.Extension{ext}, t.TempDir())
	session, err := NewSession(services, SessionOptions{Model: services.ModelRuntime().GetModel("faux", "faux-1"), Runner: runner, SkipBuiltinTools: true})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for event := range session.Events() {
			AcknowledgeEvent(event)
		}
	}()
	t.Cleanup(func() { _ = session.Close(); <-done })
	if err := session.BindExtensions(t.Context()); err != nil {
		t.Fatal(err)
	}
	return session, faux
}

func toolResultOf(t *testing.T, session *Session) *agent.ToolResultMessage {
	t.Helper()
	for _, message := range session.Messages() {
		if message.ToolResult != nil {
			return message.ToolResult
		}
	}
	t.Fatal("no tool result")
	return nil
}

// upstream agent-session-tool-orchestration.test.ts:62-113
func TestSessionToolOrchestrationSupportsToolsThatCallOtherToolsUnderAnyNameThroughTheExtensionAPI(t *testing.T) {
	var toolCalls []string
	var mu sync.Mutex
	session, faux := newOrchestrationSession(t, orchestratorExtension(&toolCalls, &mu))

	assertEqual(t, "active tools", session.ActiveToolNames(), []string{"echo", "run_tools"})
	assertEqual(t, "callable tools", session.CallableToolNames(), []string{"echo", "helper"})
	description := func(name string) string {
		for _, tool := range session.Agent().Tools() {
			if tool.Name() == name {
				return tool.Schema().Description
			}
		}
		return ""
	}
	if got := description("run_tools"); got != "Runs tools: echo, helper" {
		t.Fatalf("run_tools description = %q", got)
	}
	if got := description("echo"); got != "Echo text (also callable from run_tools)." {
		t.Fatalf("echo description = %q", got)
	}

	var requestTools [][]string
	faux.SetResponses([]ai.FauxResponseStep{
		ai.FauxFactoryStep(func(request ai.TranscriptContext, _ ai.StreamOptions, _ *ai.FauxProviderState, _ *ai.Model) (ai.FauxResponse, error) {
			var names []string
			for _, tool := range ai.GetCurrentTools(request.Messages()) {
				names = append(names, tool.Name)
			}
			requestTools = append(requestTools, names)
			return ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxToolCall("run_tools", map[string]any{}, "")}, StopReason: "toolUse"}, nil
		}),
		fauxText("done"),
	})
	if _, err := session.Prompt(t.Context(), "go"); err != nil {
		t.Fatal(err)
	}

	// echo stays active, but its declaration is left out of requests.
	assertEqual(t, "request tools", requestTools, [][]string{{"run_tools"}})
	result := toolResultOf(t, session)
	if len(result.Content) != 1 || result.Content[0] != (ai.TextContent{Text: "helped | echo: hi | Tool run_tools not found"}) {
		t.Fatalf("content = %+v", result.Content)
	}
	parent := result.ToolCallID
	assertEqual(t, "tool_call events", toolCalls, []string{"run_tools:top", "helper:" + parent, "echo:" + parent})
	if result.NestedCalls == nil {
		t.Fatal("the tool result has no nestedCalls")
	}
	type row struct {
		id, name string
		status   ai.NestedToolCallStatus
	}
	var rows []row
	for _, call := range result.NestedCalls.Calls {
		rows = append(rows, row{call.ID, call.Name, call.Status})
	}
	want := []row{{parent + "/1", "helper", ai.NestedToolCallOK}, {parent + "/2", "echo", ai.NestedToolCallOK}, {parent + "/3", "run_tools", ai.NestedToolCallError}}
	assertEqual(t, "nested calls", rows, want)
	// The record is persisted with the session.
	var persisted *agent.ToolResultMessage
	for _, entry := range session.Inner().GetBranch() {
		if message, ok := entry.AsMessage(); ok && message.Message.ToolResult != nil {
			persisted = message.Message.ToolResult
			break
		}
	}
	if persisted == nil || !reflect.DeepEqual(persisted.NestedCalls, result.NestedCalls) {
		t.Fatalf("persisted nestedCalls = %+v, want %+v", persisted, result.NestedCalls)
	}
}

// upstream agent-session-tool-orchestration.test.ts:133-144
func TestSessionToolOrchestrationLeavesResultsWithoutNestedCallsUnchanged(t *testing.T) {
	var toolCalls []string
	var mu sync.Mutex
	session, faux := newOrchestrationSession(t, orchestratorExtension(&toolCalls, &mu))
	faux.SetResponses([]ai.FauxResponseStep{
		ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxToolCall("echo", map[string]any{"text": "x"}, "")}, StopReason: "toolUse"}),
		fauxText("done"),
	})
	if _, err := session.Prompt(t.Context(), "go"); err != nil {
		t.Fatal(err)
	}
	result := toolResultOf(t, session)
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if result.NestedCalls != nil || strings.Contains(string(raw), "nestedCalls") {
		t.Fatalf("a result without nested calls carries nestedCalls: %s", raw)
	}
}

// Go guard for the rule of agent-session-tool-orchestration.test.ts:115-131 (case 2, pending on family 8's codemode and tool-search
// extensions) and agent-session.ts:3507-3519 (_isDeclarable, _isActivatedOnRegistration): a tool with defaultActive false is not
// active on registration; naming it in the allowed tools activates it. `direct` and `model-only` tools are declarable; `codemode`,
// `deferred` and `hidden` tools are never activated.
func TestToolsWithDefaultActiveFalseAreActiveOnlyWhenNamed(t *testing.T) {
	define := func(name string, exposure extension.ToolExposure, defaultActive *bool) extension.RegisteredTool {
		return orchestrationTool(name, name, extension.ToolDefinition{Exposure: exposure, DefaultActive: defaultActive, Execute: func(context.Context, string, json.RawMessage, extension.AgentToolUpdateCallback) (extension.AgentToolResult, error) {
			return textResult(name), nil
		}})
	}
	inactive := false
	ext := extension.Extension{
		Tools: map[string]extension.RegisteredTool{
			"plain": define("plain", "", nil), "lazy": define("lazy", extension.ToolExposureDirect, &inactive), "modelonly": define("modelonly", extension.ToolExposureModelOnly, nil),
			"script": define("script", extension.ToolExposureCodemode, nil), "search": define("search", extension.ToolExposureDeferred, nil), "gone": define("gone", extension.ToolExposureHidden, nil),
		},
		ToolOrder: []string{"plain", "lazy", "modelonly", "script", "search", "gone"},
	}
	session, _ := newOrchestrationSession(t, ext)
	assertEqual(t, "active by default", session.ActiveToolNames(), []string{"plain", "modelonly"})
	assertEqual(t, "callable", session.CallableToolNames(), []string{"plain", "script", "search"})

	session.SetActiveToolsByName([]string{"lazy", "script", "gone", "unknown"})
	// Unknown and hidden names are ignored; a codemode tool named explicitly is active, and stays callable.
	assertEqual(t, "active after naming", session.ActiveToolNames(), []string{"lazy", "script"})
	assertEqual(t, "callable after naming", session.CallableToolNames(), []string{"lazy", "script", "search"})
}

// upstream agent-session-tool-orchestration.test.ts:115-131. `initialActiveToolNames` of upstream's harness is the SDK's initial selection
// (sdk.ts:264-269: options.tools, else the defaultTools setting); `allowedToolNames` is --tools.
func TestSessionToolOrchestrationRegistersCodemodeAndToolSearchInactiveUntilNamed(t *testing.T) {
	open := func(t *testing.T, settings icodingagent.Settings, allowed map[string]struct{}) *Session {
		t.Helper()
		services := newTestServicesWithSettings(t, settings)
		faux := ai.NewFauxProvider(ai.FauxConfig{ProviderID: "faux", Model: "faux-1"})
		if err := services.ModelRuntime().RegisterNativeProvider(faux.Provider()); err != nil {
			t.Fatal(err)
		}
		if err := services.Auth().Set("faux", ai.Credential{Type: ai.CredentialAPIKey, Key: "faux-key"}); err != nil {
			t.Fatal(err)
		}
		var extensions []extension.Extension
		for _, path := range []string{"builtin:codemode", "builtin:tool-search"} {
			entry, err := builtin.Resolve(path, builtin.Options{})
			if err != nil {
				t.Fatal(err)
			}
			ext, err := entry.Factory()
			if err != nil {
				t.Fatal(err)
			}
			extensions = append(extensions, ext)
		}
		session, err := NewSession(services, SessionOptions{Model: services.ModelRuntime().GetModel("faux", "faux-1"), Runner: inproc.NewRunner(extensions, t.TempDir()), AllowedTools: allowed})
		if err != nil {
			t.Fatal(err)
		}
		done := make(chan struct{})
		go func() {
			defer close(done)
			for event := range session.Events() {
				AcknowledgeEvent(event)
			}
		}()
		t.Cleanup(func() { _ = session.Close(); <-done })
		if err := session.BindExtensions(t.Context()); err != nil {
			t.Fatal(err)
		}
		return session
	}

	plain := open(t, icodingagent.Settings{}, nil)
	var names []string
	for _, tool := range plain.GetAllTools() {
		names = append(names, tool.Name)
	}
	for _, want := range []string{"codemode", "tool_search"} {
		if !slices.Contains(names, want) {
			t.Fatalf("getAllTools = %v, want %s", names, want)
		}
	}
	assertEqual(t, "plain active", plain.ActiveToolNames(), []string{"read", "bash", "edit", "write"})

	// --tools and the defaultTools setting name them explicitly.
	allowed := open(t, icodingagent.Settings{}, map[string]struct{}{"read": {}, "codemode": {}})
	assertEqual(t, "allowed active", allowed.ActiveToolNames(), []string{"read", "codemode"})
	initial := open(t, icodingagent.Settings{DefaultTools: []string{"tool_search"}}, nil)
	assertEqual(t, "initial active", initial.ActiveToolNames(), []string{"tool_search"})
}
