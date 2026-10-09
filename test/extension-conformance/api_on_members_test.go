package extensionconformance

import (
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// TestConformance_ExtensionAPIOnMembersCoverEverySubscribedEvent pins the Go reference API to the conformance event list (Pi types.ts:1558-1642,
// ExtensionAPI.on): every event the SDK subscription conformance drives has one API.On<Event> member, named after the event, and no
// member is bound to an event the SDKs are not driven with.
func TestConformance_ExtensionAPIOnMembersCoverEverySubscribedEvent(t *testing.T) {
	apiOnMembers := map[string]any{
		"resources_discover":      extension.API.OnResourcesDiscover,
		"session_start":           extension.API.OnSessionStart,
		"session_info_changed":    extension.API.OnSessionInfoChanged,
		"session_before_switch":   extension.API.OnSessionBeforeSwitch,
		"session_before_fork":     extension.API.OnSessionBeforeFork,
		"session_before_compact":  extension.API.OnSessionBeforeCompact,
		"project_trust":           extension.API.OnProjectTrust,
		"session_compact":         extension.API.OnSessionCompact,
		"session_compact_failed":  extension.API.OnSessionCompactFailed,
		"session_shutdown":        extension.API.OnSessionShutdown,
		"session_before_tree":     extension.API.OnSessionBeforeTree,
		"session_tree":            extension.API.OnSessionTree,
		"context":                 extension.API.OnContext,
		"context_with_system":     extension.API.OnContextWithSystem,
		"before_provider_request": extension.API.OnBeforeProviderRequest,
		"mcp_servers_change":      extension.API.OnMcpServersChange,
		"provider_stream_event":   extension.API.OnProviderStreamEvent,
		"after_provider_response": extension.API.OnAfterProviderResponse,
		"before_provider_headers": extension.API.OnBeforeProviderHeaders,
		"before_agent_start":      extension.API.OnBeforeAgentStart,
		"agent_start":             extension.API.OnAgentStart,
		"agent_end":               extension.API.OnAgentEnd,
		"agent_before_settle":     extension.API.OnAgentBeforeSettle,
		"agent_settled":           extension.API.OnAgentSettled,
		"ui_prompt_start":         extension.API.OnUiPromptStart,
		"ui_prompt_end":           extension.API.OnUiPromptEnd,
		"cache_warming_decision":  extension.API.OnCacheWarmingDecision,
		"turn_start":              extension.API.OnTurnStart,
		"turn_end":                extension.API.OnTurnEnd,
		"message_start":           extension.API.OnMessageStart,
		"message_update":          extension.API.OnMessageUpdate,
		"message_end":             extension.API.OnMessageEnd,
		"tool_execution_start":    extension.API.OnToolExecutionStart,
		"tool_execution_update":   extension.API.OnToolExecutionUpdate,
		"tool_execution_end":      extension.API.OnToolExecutionEnd,
		"model_select":            extension.API.OnModelSelect,
		"thinking_level_select":   extension.API.OnThinkingLevelSelect,
		"tool_call":               extension.API.OnToolCall,
		"tool_result":             extension.API.OnToolResult,
		"user_bash":               extension.API.OnUserBash,
		"input":                   extension.API.OnInput,
	}

	if len(apiOnMembers) != len(subscribedEventNames) {
		t.Fatalf("%d API.On members for %d subscribed events", len(apiOnMembers), len(subscribedEventNames))
	}
	for _, event := range subscribedEventNames {
		member, ok := apiOnMembers[event]
		if !ok {
			t.Errorf("%s: no extension.API.On member", event)
			continue
		}
		if reflect.TypeOf(member).Kind() != reflect.Func {
			t.Errorf("%s: member is not a method expression", event)
			continue
		}
		var want strings.Builder
		want.WriteString("On")
		for word := range strings.SplitSeq(event, "_") {
			want.WriteString(strings.ToUpper(word[:1]) + word[1:])
		}
		name := runtime.FuncForPC(reflect.ValueOf(member).Pointer()).Name()
		if !strings.HasSuffix(name, "API."+want.String()) && !strings.HasSuffix(name, "."+want.String()) {
			t.Errorf("%s: bound to %s, want API.%s", event, name, want.String())
		}
	}
}
