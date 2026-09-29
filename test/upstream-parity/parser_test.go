package parity

import (
	"slices"
	"strings"
	"testing"
)

const fixtureMini = `
// Junk preamble.

export interface UnrelatedThing {
	x: number;
}

export interface ToolCallEvent {
	type: "tool_call";
	toolName: string;
	input: Record<string, unknown>;
	readonly toolCallId: string;
}

export interface ToolCallEventResult {
	block: boolean;
	reason?: string;
}

// EventBase is excluded by the parser (suffix EventBase).
export interface ToolCallEventBase {
	type: string;
}

export interface ExtensionAPI {
	on(event: "tool_call", handler: ExtensionHandler<ToolCallEvent, ToolCallEventResult>): void;
	on(event: "session_start", handler: ExtensionHandler<SessionStartEvent>): void;

	registerTool<TParams extends TSchema = TSchema>(tool: ToolDefinition<TParams>): void;
	registerCommand(name: string, options: Omit<RegisteredCommand, "name">): void;

	sendMessage<T = unknown>(
		message: Pick<CustomMessage<T>, "customType" | "content">,
		options?: { triggerTurn?: boolean }
	): void;

	exec(command: string, args: string[], options?: ExecOptions): Promise<ExecResult>;

	events: EventBus;
}
`

func TestParser_ExtractsEventsFromExtensionAPI(t *testing.T) {
	s, err := LoadUpstreamFrom(fixtureMini)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	wantEvents := []string{"tool_call", "session_start"}
	if !equalStrings(s.API.Events, wantEvents) {
		t.Errorf("Events = %v, want %v", s.API.Events, wantEvents)
	}
}

func TestParser_ExtractsMethodsFromExtensionAPI(t *testing.T) {
	s, err := LoadUpstreamFrom(fixtureMini)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	want := []string{"registerTool", "registerCommand", "sendMessage", "exec", "events"}
	if !equalStrings(s.API.Methods, want) {
		t.Errorf("Methods = %v, want %v", s.API.Methods, want)
	}
}

func TestParser_ExtractsEventTypeAndResult(t *testing.T) {
	s, err := LoadUpstreamFrom(fixtureMini)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(s.EventTypes) != 1 || s.EventTypes[0].Name != "ToolCallEvent" {
		t.Fatalf("EventTypes = %+v", s.EventTypes)
	}
	if len(s.EventResults) != 1 || s.EventResults[0].Name != "ToolCallEventResult" {
		t.Fatalf("EventResults = %+v", s.EventResults)
	}
	wantFields := map[string]string{
		"type":       `"tool_call"`,
		"toolName":   "string",
		"input":      "Record<string, unknown>",
		"toolCallId": "string",
	}
	if !equalMaps(s.EventTypes[0].Fields, wantFields) {
		t.Errorf("ToolCallEvent.Fields = %v, want %v", s.EventTypes[0].Fields, wantFields)
	}
	wantOrder := []string{"type", "toolName", "input", "toolCallId"}
	if !equalStrings(s.EventTypes[0].FieldOrder, wantOrder) {
		t.Errorf("ToolCallEvent.FieldOrder = %v, want %v", s.EventTypes[0].FieldOrder, wantOrder)
	}
}

func TestParser_SkipsEventBaseInterfaces(t *testing.T) {
	s, err := LoadUpstreamFrom(fixtureMini)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	for _, e := range s.EventTypes {
		if strings.HasSuffix(e.Name, "EventBase") {
			t.Errorf("EventBase interface leaked into EventTypes: %s", e.Name)
		}
	}
}

func TestSnakeEventToOnMethod(t *testing.T) {
	cases := map[string]string{
		"tool_call":              "OnToolCall",
		"session_before_compact": "OnSessionBeforeCompact",
		"input":                  "OnInput",
		"":                       "On",
	}
	for in, want := range cases {
		if got := SnakeEventToOnMethod(in); got != want {
			t.Errorf("SnakeEventToOnMethod(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCamelToPascal(t *testing.T) {
	cases := map[string]string{
		"registerTool": "RegisterTool",
		"exec":         "Exec",
		"events":       "Events",
		"":             "",
	}
	for in, want := range cases {
		if got := CamelToPascal(in); got != want {
			t.Errorf("CamelToPascal(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCamelToGoField(t *testing.T) {
	cases := map[string]string{
		// terminal initialisms
		"toolCallId":       "ToolCallID",
		"newLeafId":        "NewLeafID",
		"oldLeafId":        "OldLeafID",
		"entryId":          "EntryID",
		"targetId":         "TargetID",
		"commonAncestorId": "CommonAncestorID",
		"baseUrl":          "BaseURL",
		// leading initialisms
		"apiKey":    "APIKey",
		"jsonValue": "JSONValue",
		// pure initialisms
		"id":   "ID",
		"url":  "URL",
		"api":  "API",
		"json": "JSON",
		// no initialism
		"customInstructions": "CustomInstructions",
		"toolName":           "ToolName",
		"reason":             "Reason",
		// edge cases
		"":  "",
		"a": "A",
		// word that contains "id" mid-word but not at boundary stays as-is
		"video":      "Video",      // "vide" + "o"? No: it's all lower so segment is "Video". Not in initialisms.
		"identifier": "Identifier", // first segment "Identifier": not in initialisms.
	}
	for in, want := range cases {
		if got := CamelToGoField(in); got != want {
			t.Errorf("CamelToGoField(%q) = %q, want %q", in, got, want)
		}
	}
}

// ─── Live upstream snapshot ───────────────────────────────────────────────────

func TestParser_LiveUpstream_Smoke(t *testing.T) {
	s, err := LoadUpstream()
	if err != nil {
		t.Skipf("upstream mirror not available: %v", err)
	}
	// Sanity bounds: these are conservative and will only fail if the parser
	// catastrophically miscounts. Exact numbers are checked by the parity tests.
	if len(s.API.Events) < 25 {
		t.Errorf("expected ≥25 events, got %d", len(s.API.Events))
	}
	if len(s.API.Methods) < 20 {
		t.Errorf("expected ≥20 methods, got %d", len(s.API.Methods))
	}
	if len(s.EventTypes) < 30 {
		t.Errorf("expected ≥30 *Event types, got %d", len(s.EventTypes))
	}
	if len(s.EventResults) < 5 {
		t.Errorf("expected ≥5 *EventResult types, got %d", len(s.EventResults))
	}
	// Spot checks: a few well-known events must be present.
	mustHaveEvent := []string{"tool_call", "tool_result", "session_start", "agent_end", "before_provider_request"}
	for _, e := range mustHaveEvent {
		if !contains(s.API.Events, e) {
			t.Errorf("expected event %q present in API.Events; got %v", e, s.API.Events)
		}
	}
	// Spot checks: a few well-known methods.
	mustHaveMethod := []string{"registerTool", "registerCommand", "sendMessage", "exec", "setSessionName", "registerProvider"}
	for _, m := range mustHaveMethod {
		if !contains(s.API.Methods, m) {
			t.Errorf("expected method %q present in API.Methods; got %v", m, s.API.Methods)
		}
	}
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func equalMaps(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func contains(s []string, v string) bool {
	return slices.Contains(s, v)
}
