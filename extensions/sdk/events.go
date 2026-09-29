// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package sdk

// Event names accepted by [Extension.OnEvent]. Each is the name of one
// upstream pi.on overload (ExtensionAPI.on in Pi's extensions/types.ts), and
// the handler's data map is that event's payload.
const (
	// Startup and resources.
	EventProjectTrust       = "project_trust"
	EventResourcesDiscover  = "resources_discover"
	EventSessionStart       = "session_start"
	EventSessionInfoChanged = "session_info_changed"

	// Session lifecycle.
	EventSessionBeforeSwitch  = "session_before_switch"
	EventSessionBeforeFork    = "session_before_fork"
	EventSessionBeforeCompact = "session_before_compact"
	EventSessionCompact       = "session_compact"
	EventSessionCompactFailed = "session_compact_failed"
	EventSessionShutdown      = "session_shutdown"
	EventSessionBeforeTree    = "session_before_tree"
	EventSessionTree          = "session_tree"

	// Context and provider requests.
	EventContext               = "context"
	EventContextWithSystem     = "context_with_system"
	EventCacheWarmingDecision  = "cache_warming_decision"
	EventBeforeProviderRequest = "before_provider_request"
	EventBeforeProviderHeaders = "before_provider_headers"
	EventAfterProviderResponse = "after_provider_response"

	// Agent loop and interactive prompts.
	EventBeforeAgentStart  = "before_agent_start"
	EventAgentStart        = "agent_start"
	EventAgentEnd          = "agent_end"
	EventAgentBeforeSettle = "agent_before_settle"
	EventAgentSettled      = "agent_settled"
	EventUIPromptStart     = "ui_prompt_start"
	EventUIPromptEnd       = "ui_prompt_end"
	EventTurnStart         = "turn_start"
	EventTurnEnd           = "turn_end"

	// Messages and tool execution.
	EventMessageStart        = "message_start"
	EventMessageUpdate       = "message_update"
	EventMessageEnd          = "message_end"
	EventToolExecutionStart  = "tool_execution_start"
	EventToolExecutionUpdate = "tool_execution_update"
	EventToolExecutionEnd    = "tool_execution_end"

	// Selection, tool interception and input.
	EventModelSelect         = "model_select"
	EventThinkingLevelSelect = "thinking_level_select"
	EventToolCall            = "tool_call"
	EventToolResult          = "tool_result"
	EventUserBash            = "user_bash"
	EventInput               = "input"
)
