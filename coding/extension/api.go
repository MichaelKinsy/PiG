package extension

import (
	"context"
)

// ─── Action payload + option types ───────────────────────────────────────

// DeliverAs is the queueing discriminator for [API.SendMessage] /
// [API.SendUserMessage]. Upstream literal-string union.
//
// upstream: types.ts:1161, 1170
type DeliverAs string

const (
	DeliverAsSteer    DeliverAs = "steer"
	DeliverAsFollowUp DeliverAs = "followUp"
	DeliverAsNextTurn DeliverAs = "nextTurn"
)

// SendMessagePayload mirrors the inline `Pick<CustomMessage<T>, "customType"
// | "content" | "display" | "details">` parameter on upstream's sendMessage.
//
// upstream: types.ts:1159–1163
type SendMessagePayload struct {
	CustomType string `json:"customType"`
	// Content preserves upstream CustomMessage content across the dynamic
	// extension boundary.
	Content any `json:"content,omitempty"`
	Display any `json:"display,omitempty"`
	Details any `json:"details,omitempty"`
}

// SendMessageOptions mirrors the inline options object on sendMessage.
//
// upstream: types.ts:1161–1162
type SendMessageOptions struct {
	TriggerTurn bool      `json:"triggerTurn,omitempty"`
	DeliverAs   DeliverAs `json:"deliverAs,omitempty"`
}

// SendUserMessageOptions mirrors the inline options object on
// sendUserMessage. Note upstream restricts DeliverAs to {steer, followUp};
// the type is shared, but [DeliverAsNextTurn] is invalid here and the host
// rejects it at runtime.
//
// upstream: types.ts:1170–1171
type SendUserMessageOptions struct {
	DeliverAs DeliverAs `json:"deliverAs,omitempty"`
}

// ─── The fat API interface ───────────────────────────────────────────────

// API is the surface passed to extension factory functions. It mirrors
// upstream's `ExtensionAPI` interface 1:1: every event registrable via
// upstream's `pi.on(event, handler)` overload has a typed `On<Event>`
// method here, every other method on `ExtensionAPI` has a Go counterpart
// (camelCase → PascalCase), and the upstream `events: EventBus` property
// surfaces as the [API.Events] method.
//
// Method order matches upstream declaration order so a side-by-side
// review against types.ts is mechanical. Every method's doc comment cites
// its upstream line in `types.ts`.
//
// Go mapping:
//   - Events is a method because Go interfaces cannot contain fields.
//   - Handlers receive context.Context and return errors.
//
// upstream: types.ts:1066–1296
type API interface {
	// =====================================================================
	// Event Subscription (in upstream declaration order)
	// =====================================================================

	// OnProjectTrust registers a handler for "project_trust".
	// upstream: types.ts:1125 (on("project_trust", ...))
	OnProjectTrust(handler func(ctx context.Context, evt ProjectTrustEvent) (ProjectTrustEventResult, error))

	// OnResourcesDiscover registers a handler for "resources_discover".
	// upstream: types.ts:1071
	OnResourcesDiscover(handler func(ctx context.Context, evt ResourcesDiscoverEvent) (ResourcesDiscoverResult, error))

	// OnSessionStart registers a handler for "session_start".
	// upstream: types.ts:1143
	OnSessionStart(handler func(ctx context.Context, evt SessionStartEvent) error)

	// OnSessionInfoChanged registers a handler for "session_info_changed"
	// (upstream types.ts SessionInfoChangedEvent; adopted upstream in 0.80.3).
	OnSessionInfoChanged(handler func(ctx context.Context, evt SessionInfoChangedEvent) error)

	// OnSessionBeforeSwitch registers a handler for "session_before_switch".
	// upstream: types.ts:1145
	OnSessionBeforeSwitch(handler func(ctx context.Context, evt SessionBeforeSwitchEvent) (SessionBeforeSwitchResult, error))

	// OnSessionBeforeFork registers a handler for "session_before_fork".
	// upstream: types.ts:1077
	OnSessionBeforeFork(handler func(ctx context.Context, evt SessionBeforeForkEvent) (SessionBeforeForkResult, error))

	// OnSessionBeforeCompact registers a handler for "session_before_compact".
	// upstream: types.ts:1078
	OnSessionBeforeCompact(handler func(ctx context.Context, evt SessionBeforeCompactEvent) (SessionBeforeCompactResult, error))

	// OnSessionCompact registers a handler for "session_compact".
	// upstream: types.ts:1082
	OnSessionCompact(handler func(ctx context.Context, evt SessionCompactEvent) error)

	// OnSessionCompactFailed registers a handler for "session_compact_failed",
	// fired after manual or automatic compaction fails or is aborted.
	// upstream: types.ts:1374
	OnSessionCompactFailed(handler func(ctx context.Context, evt SessionCompactFailedEvent) error)

	// OnSessionShutdown registers a handler for "session_shutdown".
	// upstream: types.ts:1083
	OnSessionShutdown(handler func(ctx context.Context, evt SessionShutdownEvent) error)

	// OnSessionBeforeTree registers a handler for "session_before_tree".
	// upstream: types.ts:1084
	OnSessionBeforeTree(handler func(ctx context.Context, evt SessionBeforeTreeEvent) (SessionBeforeTreeResult, error))

	// OnSessionTree registers a handler for "session_tree".
	// upstream: types.ts:1085
	OnSessionTree(handler func(ctx context.Context, evt SessionTreeEvent) error)

	// OnContext registers a handler for "context".
	// upstream: types.ts:1086
	OnContext(handler func(ctx context.Context, evt ContextEvent) (ContextEventResult, error))

	// OnContextWithSystem registers a handler for "context_with_system".
	// upstream: types.ts ExtensionAPI.on("context_with_system")
	OnContextWithSystem(handler func(ctx context.Context, evt ContextWithSystemEvent) (ContextEventResult, error))

	// OnBeforeProviderRequest registers a handler for "before_provider_request".
	// upstream: types.ts:1087
	OnBeforeProviderRequest(handler func(ctx context.Context, evt BeforeProviderRequestEvent) (BeforeProviderRequestEventResult, error))

	// OnAfterProviderResponse registers a handler for "after_provider_response".
	// upstream: types.ts:1091
	OnAfterProviderResponse(handler func(ctx context.Context, evt AfterProviderResponseEvent) error)

	// OnBeforeProviderHeaders registers a handler for "before_provider_headers".
	// Handlers mutate evt.Headers in place before the request is sent.
	// upstream: types.ts:1199
	OnBeforeProviderHeaders(handler func(ctx context.Context, evt BeforeProviderHeadersEvent) error)

	// OnBeforeAgentStart registers a handler for "before_agent_start".
	// upstream: types.ts:1092
	OnBeforeAgentStart(handler func(ctx context.Context, evt BeforeAgentStartEvent) (BeforeAgentStartEventResult, error))

	// OnAgentStart registers a handler for "agent_start".
	// upstream: types.ts:1093
	OnAgentStart(handler func(ctx context.Context, evt AgentStartEvent) error)

	// OnAgentEnd registers a handler for "agent_end".
	// upstream: types.ts:1399
	OnAgentEnd(handler func(ctx context.Context, evt AgentEndEvent) error)

	// OnAgentBeforeSettle registers an awaited final-settlement boundary handler.
	// Handlers may propose durable entries and request one runnable continuation.
	// upstream: types.ts:1401
	OnAgentBeforeSettle(handler func(ctx context.Context, evt *AgentBeforeSettleEvent) (AgentBeforeSettleEventResult, error))

	// OnAgentSettled registers a handler for "agent_settled", fired after an
	// agent run has fully settled (no retry, compaction, or queued continuation).
	// upstream: types.ts:1204
	OnAgentSettled(handler func(ctx context.Context, evt AgentSettledEvent) error)

	// OnUiPromptStart registers a handler for "ui_prompt_start", fired
	// without awaiting handlers when the outermost blocking ctx.ui prompt
	// (select, confirm, input, editor, custom) begins.
	// upstream: types.ts:1404
	OnUiPromptStart(handler func(ctx context.Context, evt UIPromptStartEvent) error)

	// OnUiPromptEnd registers a handler for "ui_prompt_end", fired without
	// awaiting handlers when the outermost blocking ctx.ui prompt settles.
	// upstream: types.ts:1405
	OnUiPromptEnd(handler func(ctx context.Context, evt UIPromptEndEvent) error)

	// OnTurnStart registers a handler for "turn_start".
	// upstream: types.ts:1095
	OnTurnStart(handler func(ctx context.Context, evt TurnStartEvent) error)

	// OnTurnEnd registers a handler for "turn_end".
	// upstream: types.ts:1096
	OnTurnEnd(handler func(ctx context.Context, evt TurnEndEvent) error)

	// OnMessageStart registers a handler for "message_start".
	// upstream: types.ts:1097
	OnMessageStart(handler func(ctx context.Context, evt MessageStartEvent) error)

	// OnMessageUpdate registers a handler for "message_update".
	// upstream: types.ts:1098
	OnMessageUpdate(handler func(ctx context.Context, evt MessageUpdateEvent) error)

	// OnMessageEnd registers a handler for "message_end".
	// upstream: types.ts:1099
	OnMessageEnd(handler func(ctx context.Context, evt MessageEndEvent) (MessageEndEventResult, error))

	// OnToolExecutionStart registers a handler for "tool_execution_start".
	// upstream: types.ts:1100
	OnToolExecutionStart(handler func(ctx context.Context, evt ToolExecutionStartEvent) error)

	// OnToolExecutionUpdate registers a handler for "tool_execution_update".
	// upstream: types.ts:1101
	OnToolExecutionUpdate(handler func(ctx context.Context, evt ToolExecutionUpdateEvent) error)

	// OnToolExecutionEnd registers a handler for "tool_execution_end".
	// upstream: types.ts:1102
	OnToolExecutionEnd(handler func(ctx context.Context, evt ToolExecutionEndEvent) error)

	// OnModelSelect registers a handler for "model_select".
	// upstream: types.ts:1103
	OnModelSelect(handler func(ctx context.Context, evt ModelSelectEvent) error)

	// OnThinkingLevelSelect registers a handler for "thinking_level_select".
	// upstream: types.ts:1122 (added in v0.71.0).
	OnThinkingLevelSelect(handler func(ctx context.Context, evt ThinkingLevelSelectEvent) error)

	// OnToolCall registers a handler for "tool_call". The event is the union
	// [ToolCallEvent]. Type-switch on its concrete variant.
	// upstream: types.ts:1104
	OnToolCall(handler func(ctx context.Context, evt ToolCallEvent) (ToolCallEventResult, error))

	// OnToolResult registers a handler for "tool_result". The event is the
	// union [ToolResultEvent].
	// upstream: types.ts:1105
	OnToolResult(handler func(ctx context.Context, evt ToolResultEvent) (ToolResultEventResult, error))

	// OnUserBash registers a handler for "user_bash".
	// upstream: types.ts:1106
	OnUserBash(handler func(ctx context.Context, evt UserBashEvent) (UserBashEventResult, error))

	// OnInput registers a handler for "input".
	// upstream: types.ts:1107
	OnInput(handler func(ctx context.Context, evt InputEvent) (InputEventResult, error))

	// =====================================================================
	// Tool Registration
	// =====================================================================

	// RegisterTool registers a tool that the LLM can call.
	// upstream: types.ts:1114 (registerTool)
	RegisterTool(tool ToolDefinition)

	// =====================================================================
	// Command, Shortcut, Flag Registration
	// =====================================================================

	// RegisterCommand registers a custom command. Mirrors upstream's
	// `registerCommand(name, options: Omit<RegisteredCommand, "name" | "sourceInfo">)`.
	// upstream: types.ts:1123
	RegisterCommand(name string, options CommandOptions)

	// RegisterShortcut registers a keyboard shortcut.
	// upstream: types.ts:1126
	RegisterShortcut(shortcut KeyID, options ShortcutOptions)

	// RegisterFlag registers a CLI flag.
	// upstream: types.ts:1135
	RegisterFlag(name string, options FlagOptions)

	// GetFlag returns the value of a registered CLI flag. Upstream returns
	// `boolean | string | undefined`; Go returns `any` (nil when unset).
	// upstream: types.ts:1145
	GetFlag(name string) any

	// =====================================================================
	// Message Rendering
	// =====================================================================

	// RegisterMessageRenderer registers a custom renderer for a CustomMessageEntry.
	// upstream: types.ts:1152
	RegisterMessageRenderer(customType string, renderer MessageRenderer)

	// RegisterEntryRenderer registers a custom renderer for a CustomEntry. Custom
	// entries do not participate in LLM context.
	// upstream: types.ts:1266
	RegisterEntryRenderer(customType string, renderer EntryRenderer)

	// RegisterMarkdownTransformer registers a display-only Markdown transform.
	// upstream: types.ts:1287
	RegisterMarkdownTransformer(transformer MarkdownTransformer)

	// =====================================================================
	// Actions
	// =====================================================================

	// SendMessage sends a custom message to the session. Pass nil options for defaults.
	// upstream: types.ts:1159
	SendMessage(message SendMessagePayload, options *SendMessageOptions)

	// SendUserMessage sends a user message to the agent. Always triggers a turn.
	// content is `string` or `[]any` carrying TextContent / ImageContent values
	// (matches upstream's `string | (TextContent | ImageContent)[]`).
	// upstream: types.ts:1168
	SendUserMessage(content any, options *SendUserMessageOptions)

	// AppendEntry appends a custom entry to the session for state persistence
	// (not sent to LLM).
	// upstream: types.ts:1174
	AppendEntry(customType string, data any)

	// =====================================================================
	// Session Metadata
	// =====================================================================

	// SetSessionName sets the session display name (shown in session selector).
	// upstream: types.ts:1181
	SetSessionName(name string)

	// GetSessionName returns the current session name. Upstream returns
	// `string | undefined`; Go returns the empty string when unset.
	// upstream: types.ts:1184
	GetSessionName() string

	// SetLabel sets or clears a label on an entry. Pass empty string to clear.
	// upstream: types.ts:1187
	SetLabel(entryID string, label string)

	// Exec executes a shell command. Pass nil options for defaults. Upstream
	// returns `Promise<ExecResult>`; Go returns `(ExecResult, error)`.
	// Cancellation flows through `options.Signal` (which is a context.Context
	// after the D3 migration; see DIVERGENCES.md).
	// upstream: types.ts:1190
	Exec(command string, args []string, options *ExecOptions) (ExecResult, error)

	// GetActiveTools returns the list of currently active tool names.
	// upstream: types.ts:1193
	GetActiveTools() []string

	// GetAllTools returns all configured tools with parameter schema and source metadata.
	// upstream: types.ts:1196
	GetAllTools() []ToolInfo

	// SetActiveTools sets the active tools by name.
	// upstream: types.ts:1199
	SetActiveTools(toolNames []string)

	// GetCommands returns the available slash commands in the current session.
	// upstream: types.ts:1202
	GetCommands() []SlashCommandInfo

	// =====================================================================
	// Model and Thinking Level
	// =====================================================================

	// SetModel sets the current model. Upstream returns
	// `Promise<boolean>` (false when no API key available); Go returns
	// `(bool, error)`: error is non-nil only on host-side failures.
	// upstream: types.ts:1209
	SetModel(model Model) (bool, error)

	// GetThinkingLevel returns the current thinking level.
	// upstream: types.ts:1212
	GetThinkingLevel() ThinkingLevel

	// SetThinkingLevel sets the thinking level (clamped to model capabilities).
	// upstream: types.ts:1215
	SetThinkingLevel(level ThinkingLevel)

	// =====================================================================
	// Provider Registration
	// =====================================================================

	// RegisterProvider registers or overrides a model provider. See [ProviderConfig]
	// for the field-level semantics (upstream JSDoc preserved).
	// upstream: types.ts:1264
	RegisterProvider(name string, config ProviderConfig)

	// UnregisterProvider unregisters a previously registered provider. Has
	// no effect if the provider is not currently registered.
	// upstream: types.ts:1280
	UnregisterProvider(name string)

	// =====================================================================
	// Event Bus (D1: upstream property → Go method)
	// =====================================================================

	// Events returns the shared event bus for extension communication.
	//
	// pig translation rule (interface property → method): upstream exposes
	// this as the property `events: EventBus`. Go interfaces cannot have
	// fields, so the API surfaces a method that returns the host's single
	// shared instance. See DIVERGENCES.md "TS→Go translation rituals".
	// upstream: types.ts:1283
	Events() EventBus
}
