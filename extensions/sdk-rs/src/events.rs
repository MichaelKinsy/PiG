//! Event names accepted by [`crate::Extension::on_event`], one per upstream
//! `pi.on(event, handler)` overload in Pi's `ExtensionAPI`.

/// Upstream `pi.on("project_trust", ...)`.
pub const EVENT_PROJECT_TRUST: &str = "project_trust";
/// Upstream `pi.on("resources_discover", ...)`.
pub const EVENT_RESOURCES_DISCOVER: &str = "resources_discover";
/// Upstream `pi.on("session_start", ...)`.
pub const EVENT_SESSION_START: &str = "session_start";
/// Upstream `pi.on("session_info_changed", ...)`.
pub const EVENT_SESSION_INFO_CHANGED: &str = "session_info_changed";
/// Upstream `pi.on("session_before_switch", ...)`.
pub const EVENT_SESSION_BEFORE_SWITCH: &str = "session_before_switch";
/// Upstream `pi.on("session_before_fork", ...)`.
pub const EVENT_SESSION_BEFORE_FORK: &str = "session_before_fork";
/// Upstream `pi.on("session_before_compact", ...)`.
pub const EVENT_SESSION_BEFORE_COMPACT: &str = "session_before_compact";
/// Upstream `pi.on("session_compact", ...)`.
pub const EVENT_SESSION_COMPACT: &str = "session_compact";
/// Upstream `pi.on("session_compact_failed", ...)`.
pub const EVENT_SESSION_COMPACT_FAILED: &str = "session_compact_failed";
/// Upstream `pi.on("session_shutdown", ...)`.
pub const EVENT_SESSION_SHUTDOWN: &str = "session_shutdown";
/// Upstream `pi.on("session_before_tree", ...)`.
pub const EVENT_SESSION_BEFORE_TREE: &str = "session_before_tree";
/// Upstream `pi.on("session_tree", ...)`.
pub const EVENT_SESSION_TREE: &str = "session_tree";
/// Upstream `pi.on("context", ...)`.
pub const EVENT_CONTEXT: &str = "context";
/// Upstream `pi.on("context_with_system", ...)`.
pub const EVENT_CONTEXT_WITH_SYSTEM: &str = "context_with_system";
/// Upstream `pi.on("cache_warming_decision", ...)`.
pub const EVENT_CACHE_WARMING_DECISION: &str = "cache_warming_decision";
/// Upstream `pi.on("before_provider_request", ...)`.
pub const EVENT_BEFORE_PROVIDER_REQUEST: &str = "before_provider_request";
/// Upstream `pi.on("before_provider_headers", ...)`.
pub const EVENT_BEFORE_PROVIDER_HEADERS: &str = "before_provider_headers";
/// Upstream `pi.on("after_provider_response", ...)`.
pub const EVENT_AFTER_PROVIDER_RESPONSE: &str = "after_provider_response";
/// Upstream `pi.on("before_agent_start", ...)`.
pub const EVENT_BEFORE_AGENT_START: &str = "before_agent_start";
/// Upstream `pi.on("agent_start", ...)`.
pub const EVENT_AGENT_START: &str = "agent_start";
/// Upstream `pi.on("agent_end", ...)`.
pub const EVENT_AGENT_END: &str = "agent_end";
/// Upstream `pi.on("agent_before_settle", ...)`.
pub const EVENT_AGENT_BEFORE_SETTLE: &str = "agent_before_settle";
/// Upstream `pi.on("agent_settled", ...)`.
pub const EVENT_AGENT_SETTLED: &str = "agent_settled";
/// Upstream `pi.on("ui_prompt_start", ...)`.
pub const EVENT_UI_PROMPT_START: &str = "ui_prompt_start";
/// Upstream `pi.on("ui_prompt_end", ...)`.
pub const EVENT_UI_PROMPT_END: &str = "ui_prompt_end";
/// Upstream `pi.on("turn_start", ...)`.
pub const EVENT_TURN_START: &str = "turn_start";
/// Upstream `pi.on("turn_end", ...)`.
pub const EVENT_TURN_END: &str = "turn_end";
/// Upstream `pi.on("message_start", ...)`.
pub const EVENT_MESSAGE_START: &str = "message_start";
/// Upstream `pi.on("message_update", ...)`.
pub const EVENT_MESSAGE_UPDATE: &str = "message_update";
/// Upstream `pi.on("message_end", ...)`.
pub const EVENT_MESSAGE_END: &str = "message_end";
/// Upstream `pi.on("tool_execution_start", ...)`.
pub const EVENT_TOOL_EXECUTION_START: &str = "tool_execution_start";
/// Upstream `pi.on("tool_execution_update", ...)`.
pub const EVENT_TOOL_EXECUTION_UPDATE: &str = "tool_execution_update";
/// Upstream `pi.on("tool_execution_end", ...)`.
pub const EVENT_TOOL_EXECUTION_END: &str = "tool_execution_end";
/// Upstream `pi.on("model_select", ...)`.
pub const EVENT_MODEL_SELECT: &str = "model_select";
/// Upstream `pi.on("thinking_level_select", ...)`.
pub const EVENT_THINKING_LEVEL_SELECT: &str = "thinking_level_select";
/// Upstream `pi.on("tool_call", ...)`.
pub const EVENT_TOOL_CALL: &str = "tool_call";
/// Upstream `pi.on("tool_result", ...)`.
pub const EVENT_TOOL_RESULT: &str = "tool_result";
/// Upstream `pi.on("user_bash", ...)`.
pub const EVENT_USER_BASH: &str = "user_bash";
/// Upstream `pi.on("input", ...)`.
pub const EVENT_INPUT: &str = "input";
/// Upstream `pi.on("provider_stream_event", ...)` (`types.ts:884`, `1580`): a parsed provider stream event before Pi normalizes it.
pub const EVENT_PROVIDER_STREAM_EVENT: &str = "provider_stream_event";
/// Upstream `pi.on("mcp_servers_change", ...)` (`types.ts:699`, `1562`): an MCP server was registered or unregistered after the extensions are bound. Handling it marks an extension as the one that connects registered servers.
pub const EVENT_MCP_SERVERS_CHANGE: &str = "mcp_servers_change";

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn event_names_match_upstream() {
        let pairs = [
            (EVENT_PROJECT_TRUST, "project_trust"),
            (EVENT_RESOURCES_DISCOVER, "resources_discover"),
            (EVENT_SESSION_START, "session_start"),
            (EVENT_SESSION_INFO_CHANGED, "session_info_changed"),
            (EVENT_SESSION_BEFORE_SWITCH, "session_before_switch"),
            (EVENT_SESSION_BEFORE_FORK, "session_before_fork"),
            (EVENT_SESSION_BEFORE_COMPACT, "session_before_compact"),
            (EVENT_SESSION_COMPACT, "session_compact"),
            (EVENT_SESSION_COMPACT_FAILED, "session_compact_failed"),
            (EVENT_SESSION_SHUTDOWN, "session_shutdown"),
            (EVENT_SESSION_BEFORE_TREE, "session_before_tree"),
            (EVENT_SESSION_TREE, "session_tree"),
            (EVENT_CONTEXT, "context"),
            (EVENT_CONTEXT_WITH_SYSTEM, "context_with_system"),
            (EVENT_CACHE_WARMING_DECISION, "cache_warming_decision"),
            (EVENT_BEFORE_PROVIDER_REQUEST, "before_provider_request"),
            (EVENT_BEFORE_PROVIDER_HEADERS, "before_provider_headers"),
            (EVENT_AFTER_PROVIDER_RESPONSE, "after_provider_response"),
            (EVENT_BEFORE_AGENT_START, "before_agent_start"),
            (EVENT_AGENT_START, "agent_start"),
            (EVENT_AGENT_END, "agent_end"),
            (EVENT_AGENT_BEFORE_SETTLE, "agent_before_settle"),
            (EVENT_AGENT_SETTLED, "agent_settled"),
            (EVENT_UI_PROMPT_START, "ui_prompt_start"),
            (EVENT_UI_PROMPT_END, "ui_prompt_end"),
            (EVENT_TURN_START, "turn_start"),
            (EVENT_TURN_END, "turn_end"),
            (EVENT_MESSAGE_START, "message_start"),
            (EVENT_MESSAGE_UPDATE, "message_update"),
            (EVENT_MESSAGE_END, "message_end"),
            (EVENT_TOOL_EXECUTION_START, "tool_execution_start"),
            (EVENT_TOOL_EXECUTION_UPDATE, "tool_execution_update"),
            (EVENT_TOOL_EXECUTION_END, "tool_execution_end"),
            (EVENT_MODEL_SELECT, "model_select"),
            (EVENT_THINKING_LEVEL_SELECT, "thinking_level_select"),
            (EVENT_TOOL_CALL, "tool_call"),
            (EVENT_TOOL_RESULT, "tool_result"),
            (EVENT_USER_BASH, "user_bash"),
            (EVENT_INPUT, "input"),
            (EVENT_PROVIDER_STREAM_EVENT, "provider_stream_event"),
            (EVENT_MCP_SERVERS_CHANGE, "mcp_servers_change"),
        ];
        assert_eq!(pairs.len(), 41);
        for (constant, name) in pairs {
            assert_eq!(constant, name);
        }
    }
}
