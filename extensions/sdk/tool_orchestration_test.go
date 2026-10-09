package sdk

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// Upstream types.ts:579-607 (outputSchema, exposure, namespace, annotations, defaultActive, prepareLoadout) as the host's ToolDecl receives them. An unset field is not on the wire, so an undeclared tool keeps the host's defaults.
func TestRegisterToolSendsUpstreamExposureFields(t *testing.T) {
	ext := New("orchestration")
	ext.RegisterTool(ToolDefinition{
		Name: "search", Label: "Search", Description: "Search docs", Parameters: Schema{"type": "object"},
		OutputSchema:  Schema{"type": "object", "properties": map[string]any{"n": map[string]any{"type": "number"}}},
		Exposure:      ToolExposureCodemode,
		Namespace:     &ToolNamespace{Name: "mcp__docs", Description: "Docs server", Instructions: "Search before reading."},
		Annotations:   &ToolAnnotations{ReadOnlyHint: Bool(true), OpenWorldHint: Bool(false)},
		DefaultActive: Bool(false),
		PrepareLoadout: func(ToolLoadout) *ToolLoadoutChanges {
			return nil
		},
		Execute: func(Context, map[string]any) (any, error) { return "ok", nil },
	})
	ext.RegisterTool(ToolDefinition{Name: "always", Label: "Always", Description: "Always", Parameters: Schema{"type": "object"}, Exposure: ToolExposureModelOnly, DefaultActive: Bool(true)})
	ext.RegisterTool(ToolDefinition{Name: "plain", Label: "Plain", Description: "Plain", Parameters: Schema{"type": "object"}})
	host, reg, done := surfaceHost(t, ext, nil)
	defer surfaceShutdown(t, host, done)

	var tools []map[string]any
	if err := json.Unmarshal([]byte(mustJSON(t, reg.Tools)), &tools); err != nil {
		t.Fatal(err)
	}
	want := []map[string]any{
		{
			"name": "search", "label": "Search", "description": "Search docs", "parameters": map[string]any{"type": "object"},
			"output_schema": map[string]any{"type": "object", "properties": map[string]any{"n": map[string]any{"type": "number"}}},
			"exposure":      "codemode", "namespace": map[string]any{"name": "mcp__docs", "description": "Docs server", "instructions": "Search before reading."},
			"annotations": map[string]any{"readOnlyHint": true, "openWorldHint": false}, "default_active": false, "prepares_loadout": true,
		},
		// An explicit `defaultActive: true` is a value, not an omission.
		{"name": "always", "label": "Always", "description": "Always", "parameters": map[string]any{"type": "object"}, "exposure": "model-only", "default_active": true},
		{"name": "plain", "label": "Plain", "description": "Plain", "parameters": map[string]any{"type": "object"}},
	}
	if !reflect.DeepEqual(tools, want) {
		t.Fatalf("registered tools\n got  %#v\n want %#v", tools, want)
	}
}

func orchestratorTool(name, description string) AgentTool {
	return AgentTool{Name: name, Label: name, Description: description, Parameters: json.RawMessage(`{"type":"object"}`)}
}

// Upstream agent-session-tool-orchestration.test.ts:26-40 (`run_tools`' prepareLoadout, verbatim): it lists the callable tools in its own description, rewrites the description of `echo` and hides its declaration. The loadout it sees is the host's payload (types.ts:541-551), including each tool's exposure and namespace.
func TestToolPrepareLoadoutRunsInTheExtension(t *testing.T) {
	ext := New("orchestration")
	seen := make(chan ToolLoadout, 2)
	ext.RegisterTool(ToolDefinition{
		Name: "run_tools", Label: "run_tools", Description: "Runs tools.", Parameters: Schema{"type": "object"}, Exposure: ToolExposureModelOnly,
		PrepareLoadout: func(loadout ToolLoadout) *ToolLoadoutChanges {
			seen <- loadout
			names := make([]string, len(loadout.Callable))
			for i, tool := range loadout.Callable {
				names[i] = tool.Name
			}
			return &ToolLoadoutChanges{
				Descriptions:       map[string]string{"run_tools": "Runs tools: " + strings.Join(names, ", "), "echo": "Echo text (also callable from run_tools)."},
				HiddenDeclarations: []string{"echo"},
			}
		},
		Execute: func(Context, map[string]any) (any, error) { return "ok", nil },
	})
	ext.RegisterTool(ToolDefinition{Name: "silent", Label: "silent", Description: "Silent.", Parameters: Schema{"type": "object"}, PrepareLoadout: func(ToolLoadout) *ToolLoadoutChanges { return nil }})
	host, _, done := surfaceHost(t, ext, nil)
	defer surfaceShutdown(t, host, done)

	payload := mustJSON(t, map[string]any{
		"declared":         []AgentTool{orchestratorTool("echo", "Echo text."), orchestratorTool("run_tools", "Runs tools.")},
		"callable":         []AgentTool{orchestratorTool("echo", "Echo text."), orchestratorTool("helper", "Only reachable from other tools.")},
		"registered":       []AgentTool{orchestratorTool("echo", "Echo text."), orchestratorTool("helper", "h"), orchestratorTool("run_tools", "Runs tools.")},
		"exposures":        map[string]string{"echo": "direct", "helper": "codemode", "run_tools": "model-only"},
		"namespaces":       map[string]any{"helper": map[string]any{"name": "group", "description": "A group"}},
		"promptGuidelines": map[string][]string{"echo": {"Echo with care.", "Quote the text."}},
	})
	_, resp := runSurfaceRequest(t, host, "loadout-1", &requestMsg{Method: "tool_prepare_loadout", Tool: "run_tools", Args: json.RawMessage(payload)}, nil)
	if resp.Error != nil {
		t.Fatalf("prepareLoadout failed: %+v", resp.Error)
	}
	want := `{"descriptions":{"echo":"Echo text (also callable from run_tools).","run_tools":"Runs tools: echo, helper"},"hiddenDeclarations":["echo"]}`
	if string(resp.Result) != want {
		t.Fatalf("changes = %s, want %s", resp.Result, want)
	}
	loadout := recv(t, seen)
	if len(loadout.Declared) != 2 || loadout.Declared[1].Name != "run_tools" || len(loadout.Registered) != 3 || loadout.Declared[0].Description != "Echo text." {
		t.Fatalf("loadout = %+v", loadout)
	}
	if loadout.GetExposure == nil || loadout.GetExposure("helper") != ToolExposureCodemode || loadout.GetExposure("run_tools") != ToolExposureModelOnly || loadout.GetExposure("echo") != ToolExposureDirect {
		t.Fatalf("exposures do not follow the payload")
	}
	if ns := loadout.GetNamespace("helper"); ns == nil || ns.Name != "group" || ns.Description != "A group" || loadout.GetNamespace("echo") != nil {
		t.Fatalf("namespaces do not follow the payload: %+v", ns)
	}
	if got := loadout.GetPromptGuidelines("echo"); !slices.Equal(got, []string{"Echo with care.", "Quote the text."}) || len(loadout.GetPromptGuidelines("helper")) != 0 {
		t.Fatalf("prompt guidelines do not follow the payload: %v", got)
	}

	// types.ts:607: `ToolLoadoutChanges | undefined`. No changes is null on the wire, and a tool asked about an unknown name fails the request.
	_, resp = runSurfaceRequest(t, host, "loadout-2", &requestMsg{Method: "tool_prepare_loadout", Tool: "silent", Args: json.RawMessage(payload)}, nil)
	if resp.Error != nil || (len(resp.Result) != 0 && string(resp.Result) != "null") {
		t.Fatalf("no changes = %s (%+v)", resp.Result, resp.Error)
	}
	_, resp = runSurfaceRequest(t, host, "loadout-3", &requestMsg{Method: "tool_prepare_loadout", Tool: "missing", Args: json.RawMessage(payload)}, nil)
	if resp.Error == nil || !strings.Contains(resp.Error.Message, "missing") {
		t.Fatalf("unknown tool response = %+v", resp)
	}
}

// Upstream types.ts:424-446 (structuredContent, isError) and 2063 (ToolInfo): a result carries its structured content and error flag, and a tool listing carries each tool's exposure, namespace and annotations.
func TestToolResultAndToolInfoCarryUpstreamFields(t *testing.T) {
	data, err := json.Marshal(ToolResult{Content: "3 hits", StructuredContent: map[string]any{"n": 3, "hits": []string{"a", "b", "c"}}, Details: map[string]any{"q": "x"}, IsError: true})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"content":"3 hits","details":{"q":"x"},"structured_content":{"hits":["a","b","c"],"n":3},"is_error":true}`
	if string(data) != want {
		t.Fatalf("wire = %s\nwant  %s", data, want)
	}

	ext := New("info")
	got := make(chan []ToolInfo, 1)
	ext.Command("list", "", func(ctx Context, _ string) error {
		tools, err := ctx.GetAllTools()
		got <- tools
		return err
	})
	host, _, done := surfaceHost(t, ext, nil)
	defer surfaceShutdown(t, host, done)
	_, resp := runSurfaceCommand(t, host, "list", func(*callMsg) *callResultMsg {
		return &callResultMsg{Result: json.RawMessage(`{"tools":[{"name":"search","description":"d","parameters":{},"exposure":"deferred","namespace":{"name":"docs","description":"Docs"},"annotations":{"readOnlyHint":true,"destructiveHint":false},"sourceInfo":{"path":"/p","source":"s","scope":"user","origin":"top-level"}},{"name":"read","description":"d","parameters":{},"exposure":"direct","sourceInfo":{"path":"builtin:read","source":"builtin","scope":"temporary","origin":"top-level"}}]}`)}
	})
	if resp.Error != nil {
		t.Fatal(resp.Error)
	}
	tools := recv(t, got)
	if len(tools) != 2 || tools[0].Exposure != ToolExposureDeferred || tools[0].Namespace == nil || tools[0].Namespace.Name != "docs" ||
		tools[0].Annotations == nil || tools[0].Annotations.ReadOnlyHint == nil || !*tools[0].Annotations.ReadOnlyHint || tools[0].Annotations.DestructiveHint == nil || *tools[0].Annotations.DestructiveHint ||
		tools[1].Exposure != ToolExposureDirect || tools[1].Namespace != nil || tools[1].Annotations != nil {
		t.Fatalf("tools = %+v", tools)
	}
}

// Upstream types.ts:699 and 884: the two events are ordinary handlers under their upstream names, and the unsubscribe function is idempotent.
func TestOnProviderStreamEventAndMcpServersChangeRegisterUpstreamEventNames(t *testing.T) {
	if EventProviderStreamEvent != "provider_stream_event" || EventMcpServersChange != "mcp_servers_change" {
		t.Fatalf("names = %q %q", EventProviderStreamEvent, EventMcpServersChange)
	}
	ext := New("events")
	stream := make(chan map[string]any, 2)
	change := make(chan map[string]any, 2)
	ext.OnProviderStreamEvent(func(_ Context, data map[string]any) (any, error) { stream <- data; return nil, nil })
	dropped := ext.OnMcpServersChange(func(Context, map[string]any) (any, error) { return nil, nil })
	dropped()
	dropped()
	ext.OnMcpServersChange(func(_ Context, data map[string]any) (any, error) { change <- data; return nil, nil })
	host, reg, done := surfaceHost(t, ext, nil)
	defer surfaceShutdown(t, host, done)
	var names []string
	ids := map[string]int{}
	for _, handler := range reg.Handlers {
		names = append(names, handler.Event)
		ids[handler.Event] = handler.HandlerID
	}
	if !reflect.DeepEqual(names, []string{"provider_stream_event", "mcp_servers_change"}) || len(reg.Handlers) != 2 {
		t.Fatalf("handlers = %+v", reg.Handlers)
	}
	_, resp := runSurfaceRequest(t, host, "e1", &requestMsg{Method: "event", Event: "provider_stream_event", HandlerID: ids["provider_stream_event"], Args: json.RawMessage(`{"type":"provider_stream_event","provider":"openai","api":"openai-responses","model":"gpt","data":{"type":"response.created"}}`)}, nil)
	if resp.Error != nil {
		t.Fatal(resp.Error)
	}
	if data := recv(t, stream); data["provider"] != "openai" || data["data"].(map[string]any)["type"] != "response.created" {
		t.Fatalf("provider_stream_event data = %#v", data)
	}
	_, resp = runSurfaceRequest(t, host, "e2", &requestMsg{Method: "event", Event: "mcp_servers_change", HandlerID: ids["mcp_servers_change"], Args: json.RawMessage(`{"type":"mcp_servers_change","servers":[{"name":"a","config":{"url":"u"},"extensionPath":"/e"}]}`)}, nil)
	if resp.Error != nil {
		t.Fatal(resp.Error)
	}
	if data := recv(t, change); len(data["servers"].([]any)) != 1 {
		t.Fatalf("mcp_servers_change data = %#v", data)
	}
}

// Upstream agent-session.ts:1480-1482 (`definition.exposure ?? "direct"`) and 1518: a name the host does not know is `direct` and has no namespace, and an exposure the payload leaves empty is `direct`.
func TestToolLoadoutTreatsAnUnknownOrUnsetToolAsDirect(t *testing.T) {
	ext := New("orchestration")
	seen := make(chan ToolLoadout, 1)
	ext.RegisterTool(ToolDefinition{
		Name: "loadout", Label: "loadout", Description: "d", Parameters: Schema{"type": "object"},
		PrepareLoadout: func(loadout ToolLoadout) *ToolLoadoutChanges {
			seen <- loadout
			return nil
		},
	})
	host, _, done := surfaceHost(t, ext, nil)
	defer surfaceShutdown(t, host, done)
	payload := `{"declared":[],"callable":[],"registered":[],"exposures":{"unset":"","hidden_tool":"hidden"}}`
	if _, resp := runSurfaceRequest(t, host, "loadout-4", &requestMsg{Method: "tool_prepare_loadout", Tool: "loadout", Args: json.RawMessage(payload)}, nil); resp.Error != nil {
		t.Fatal(resp.Error)
	}
	loadout := recv(t, seen)
	if loadout.GetExposure("nobody") != ToolExposureDirect || loadout.GetExposure("unset") != ToolExposureDirect || loadout.GetExposure("hidden_tool") != ToolExposureHidden || loadout.GetNamespace("nobody") != nil {
		t.Fatal("exposure defaults do not follow upstream")
	}
}
