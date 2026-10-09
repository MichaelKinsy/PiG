package extension

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// types.ts:1377 ExtensionEvent: every member of Pi's union has a Go event type that implements the closed ExtensionEvent union, and the
// tool_call, tool_result and session members are the sealed ToolCallEvent, ToolResultEvent and SessionEvent unions of those variants.
func TestExtensionEventIsPisUnion(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("..", "..", ".upstream", "current", "packages", "coding-agent", "src", "core", "extensions", "types.ts"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
	block := regexp.MustCompile(`(?s)export type ExtensionEvent =(.*?);`).FindStringSubmatch(text)
	if block == nil {
		t.Fatal("ExtensionEvent union not found in types.ts")
	}
	var piMembers []string
	for line := range strings.SplitSeq(block[1], "\n") {
		if name := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "|")); name != "" {
			piMembers = append(piMembers, name)
		}
	}
	session := regexp.MustCompile(`(?s)export type SessionEvent =(.*?);`).FindStringSubmatch(text)
	if session == nil {
		t.Fatal("SessionEvent union not found in types.ts")
	}
	var sessionMembers []string
	for line := range strings.SplitSeq(session[1], "\n") {
		if name := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "|")); name != "" {
			sessionMembers = append(sessionMembers, name)
		}
	}

	goTypes := map[string]reflect.Type{}
	for _, event := range []ExtensionEvent{
		ProjectTrustEvent{}, ResourcesDiscoverEvent{}, McpServersChangeEvent{}, ContextEvent{}, ContextWithSystemEvent{}, CacheWarmingDecisionEvent{},
		BeforeProviderRequestEvent{}, BeforeProviderHeadersEvent{}, AfterProviderResponseEvent{}, ProviderStreamEvent{}, BeforeAgentStartEvent{},
		AgentStartEvent{}, AgentEndEvent{}, AgentBeforeSettleEvent{}, AgentSettledEvent{}, UIPromptStartEvent{}, UIPromptEndEvent{}, TurnStartEvent{},
		TurnEndEvent{}, MessageStartEvent{}, MessageUpdateEvent{}, MessageEndEvent{}, ToolExecutionStartEvent{}, ToolExecutionUpdateEvent{},
		ToolExecutionEndEvent{}, ModelSelectEvent{}, ThinkingLevelSelectEvent{}, UserBashEvent{}, InputEvent{},
		SessionStartEvent{}, SessionInfoChangedEvent{}, SessionBeforeSwitchEvent{}, SessionBeforeForkEvent{}, SessionBeforeCompactEvent{}, SessionCompactEvent{},
		SessionCompactFailedEvent{}, SessionShutdownEvent{}, SessionBeforeTreeEvent{}, SessionTreeEvent{},
		CustomToolCallEvent{}, BashToolResultEvent{}, PowerShellToolResultEvent{}, ReadToolResultEvent{}, EditToolResultEvent{}, WriteToolResultEvent{},
		GrepToolResultEvent{}, FindToolResultEvent{}, LsToolResultEvent{}, CustomToolResultEvent{},
	} {
		goTypes[reflect.TypeOf(event).Name()] = reflect.TypeOf(event)
	}
	union := reflect.TypeFor[ExtensionEvent]()
	for _, name := range piMembers {
		switch name {
		case "SessionEvent":
			for _, member := range sessionMembers {
				if typ, ok := goTypes[member]; !ok || !typ.Implements(union) || !typ.Implements(reflect.TypeFor[SessionEvent]()) {
					t.Errorf("SessionEvent member %s has no Go type implementing ExtensionEvent and SessionEvent", member)
				}
			}
		case "ToolCallEvent":
			if !reflect.TypeFor[CustomToolCallEvent]().Implements(reflect.TypeFor[ToolCallEvent]()) || !reflect.TypeFor[CustomToolCallEvent]().Implements(union) {
				t.Error("ToolCallEvent is not carried by an ExtensionEvent")
			}
		case "ToolResultEvent":
			for _, variant := range []string{"BashToolResultEvent", "PowerShellToolResultEvent", "ReadToolResultEvent", "EditToolResultEvent", "WriteToolResultEvent", "GrepToolResultEvent", "FindToolResultEvent", "LsToolResultEvent", "CustomToolResultEvent"} {
				if typ := goTypes[variant]; typ == nil || !typ.Implements(union) || !typ.Implements(reflect.TypeFor[ToolResultEvent]()) {
					t.Errorf("%s does not implement ToolResultEvent and ExtensionEvent", variant)
				}
			}
		default:
			if typ, ok := goTypes[name]; !ok || !typ.Implements(union) {
				t.Errorf("ExtensionEvent member %s has no Go type implementing the union", name)
			}
		}
	}
	// A Go event that is not a session_* event does not implement SessionEvent.
	for name, typ := range goTypes {
		if typ.Implements(reflect.TypeFor[SessionEvent]()) != slices.Contains(sessionMembers, name) {
			t.Errorf("%s: implements SessionEvent = %v, but Pi's SessionEvent lists it = %v", name, !slices.Contains(sessionMembers, name), slices.Contains(sessionMembers, name))
		}
	}
	// No Go type claims to be a member Pi's union lacks.
	allowed := []string{"SessionInfoChangedEvent", "SessionCompactFailedEvent", "CustomToolCallEvent"}
	for name := range goTypes {
		if !slices.Contains(piMembers, name) && !slices.Contains(sessionMembers, name) && !strings.HasSuffix(name, "ToolResultEvent") && !slices.Contains(allowed, name) {
			t.Errorf("Go event %s is not a member of Pi's ExtensionEvent union", name)
		}
	}
}
