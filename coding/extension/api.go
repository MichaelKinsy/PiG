package extension

import (
	"context"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/orderedjson"
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
	Content any  `json:"content,omitempty"`
	Display bool `json:"display"`
	Details any  `json:"details,omitempty"`
}

// UnmarshalJSON keeps the member order of the `details` object the extension wrote.
func (p *SendMessagePayload) UnmarshalJSON(data []byte) error {
	type plain SendMessagePayload
	return orderedjson.UnmarshalFields(data, (*plain)(p), "details")
}

// SendMessageOptions mirrors the inline options object on sendMessage.
//
// upstream: types.ts:1161–1162
type SendMessageOptions struct {
	TriggerTurn *bool     `json:"triggerTurn,omitempty"`
	DeliverAs   DeliverAs `json:"deliverAs,omitempty"`
}

// SendUserMessageOptions mirrors the inline options object on
// sendUserMessage. Note upstream restricts DeliverAs to {steer, followUp};
// the type is shared, but [DeliverAsNextTurn] is invalid here and the host
// rejects it at runtime.
//
// upstream: types.ts:1170–1171
type SendUserMessageOptions struct {
	DeliverAs             DeliverAs `json:"deliverAs,omitempty"`
	ExpandPromptTemplates *bool     `json:"expandPromptTemplates,omitempty"`
}

// ─── The fat API interface ───────────────────────────────────────────────

// ExtensionHandler is Pi's ExtensionHandler<E> with its default result (types.ts:1559): a handler receives the event and the context and returns nothing but an error. A handler that returns a result has its own function type, because a Go function type has one result arity.
type ExtensionHandlerNoResult[E any] = func(ctx context.Context, evt E) error

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
//   - Every On<Event> method returns the function that removes its handler, as upstream's `on` returns `() => void`;
//     calling it again does nothing.
//   - Events is a method because Go interfaces cannot contain fields.
//   - Handlers receive context.Context and return errors.
//
// upstream: types.ts:1066–1296
type API interface {
	// =====================================================================
	// Event Subscription (in upstream declaration order). Each On method returns the function that removes exactly the
	// handler it registered, as upstream's `on(event, handler): () => void` does (types.ts:1558, loader.ts:272-286); calling it a
	// second time does nothing.
	// =====================================================================

	// OnProjectTrust registers a handler for "project_trust"; it receives the ProjectTrustContext of the decision.
	// upstream: types.ts:1558 (on("project_trust", handler: ProjectTrustHandler))
	OnProjectTrust(handler ProjectTrustHandler) func()

	// OnResourcesDiscover registers a handler for "resources_discover".
	// upstream: types.ts:1071
	OnResourcesDiscover(handler ExtensionHandler[ResourcesDiscoverEvent, ResourcesDiscoverResult]) func()

	// OnSessionStart registers a handler for "session_start".
	// upstream: types.ts:1143
	OnSessionStart(handler ExtensionHandlerNoResult[SessionStartEvent]) func()

	// OnSessionInfoChanged registers a handler for "session_info_changed"
	// (upstream types.ts SessionInfoChangedEvent; adopted upstream in 0.80.3).
	OnSessionInfoChanged(handler ExtensionHandlerNoResult[SessionInfoChangedEvent]) func()

	// OnMcpServersChange registers a handler for "mcp_servers_change", fired when
	// an extension registers or unregisters an MCP server after the extensions
	// are bound. Handling it marks an extension as the one that connects
	// registered servers.
	// upstream: types.ts:1562 (on("mcp_servers_change", ...))
	OnMcpServersChange(handler ExtensionHandlerNoResult[McpServersChangeEvent]) func()

	// OnSessionBeforeSwitch registers a handler for "session_before_switch".
	// upstream: types.ts:1145
	OnSessionBeforeSwitch(handler ExtensionHandler[SessionBeforeSwitchEvent, SessionBeforeSwitchResult]) func()

	// OnSessionBeforeFork registers a handler for "session_before_fork".
	// upstream: types.ts:1077
	OnSessionBeforeFork(handler ExtensionHandler[SessionBeforeForkEvent, SessionBeforeForkResult]) func()

	// OnSessionBeforeCompact registers a handler for "session_before_compact".
	// upstream: types.ts:1078
	OnSessionBeforeCompact(handler ExtensionHandler[SessionBeforeCompactEvent, SessionBeforeCompactResult]) func()

	// OnSessionCompact registers a handler for "session_compact".
	// upstream: types.ts:1082
	OnSessionCompact(handler ExtensionHandlerNoResult[SessionCompactEvent]) func()

	// OnSessionCompactFailed registers a handler for "session_compact_failed",
	// fired after manual or automatic compaction fails or is aborted.
	// upstream: types.ts:1374
	OnSessionCompactFailed(handler ExtensionHandlerNoResult[SessionCompactFailedEvent]) func()

	// OnSessionShutdown registers a handler for "session_shutdown".
	// upstream: types.ts:1083
	OnSessionShutdown(handler ExtensionHandlerNoResult[SessionShutdownEvent]) func()

	// OnSessionBeforeTree registers a handler for "session_before_tree".
	// upstream: types.ts:1084
	OnSessionBeforeTree(handler ExtensionHandler[SessionBeforeTreeEvent, SessionBeforeTreeResult]) func()

	// OnSessionTree registers a handler for "session_tree".
	// upstream: types.ts:1085
	OnSessionTree(handler ExtensionHandlerNoResult[SessionTreeEvent]) func()

	// OnContext registers a handler for "context".
	// upstream: types.ts:1086
	OnContext(handler ExtensionHandler[ContextEvent, ContextEventResult]) func()

	// OnContextWithSystem registers a handler for "context_with_system".
	// upstream: types.ts ExtensionAPI.on("context_with_system")
	OnContextWithSystem(handler ExtensionHandler[ContextWithSystemEvent, ContextEventResult]) func()

	// OnBeforeProviderRequest registers a handler for "before_provider_request".
	// upstream: types.ts:1087
	OnBeforeProviderRequest(handler ExtensionHandler[BeforeProviderRequestEvent, BeforeProviderRequestEventResult]) func()

	// OnAfterProviderResponse registers a handler for "after_provider_response".
	// upstream: types.ts:1091
	OnAfterProviderResponse(handler ExtensionHandlerNoResult[AfterProviderResponseEvent]) func()

	// OnBeforeProviderHeaders registers a handler for "before_provider_headers".
	// Handlers mutate evt.Headers in place before the request is sent.
	// upstream: types.ts:1199
	OnBeforeProviderHeaders(handler ExtensionHandlerNoResult[BeforeProviderHeadersEvent]) func()

	// OnProviderStreamEvent registers a handler for "provider_stream_event",
	// fired for a parsed provider stream event before it is normalized.
	// upstream: types.ts:1580 (on("provider_stream_event", ...))
	OnProviderStreamEvent(handler ExtensionHandlerNoResult[ProviderStreamEvent]) func()

	// OnBeforeAgentStart registers a handler for "before_agent_start".
	// upstream: types.ts:1092
	OnBeforeAgentStart(handler ExtensionHandler[BeforeAgentStartEvent, BeforeAgentStartEventResult]) func()

	// OnAgentStart registers a handler for "agent_start".
	// upstream: types.ts:1093
	OnAgentStart(handler ExtensionHandlerNoResult[AgentStartEvent]) func()

	// OnAgentEnd registers a handler for "agent_end".
	// upstream: types.ts:1399
	OnAgentEnd(handler ExtensionHandlerNoResult[AgentEndEvent]) func()

	// OnAgentBeforeSettle registers an awaited final-settlement boundary handler.
	// Handlers may propose durable entries and request one runnable continuation.
	// upstream: types.ts:1401
	OnAgentBeforeSettle(handler func(ctx context.Context, evt *AgentBeforeSettleEvent) (AgentBeforeSettleEventResult, error)) func()

	// OnAgentSettled registers a handler for "agent_settled", fired after an
	// agent run has fully settled (no retry, compaction, or queued continuation).
	// upstream: types.ts:1204
	OnAgentSettled(handler ExtensionHandlerNoResult[AgentSettledEvent]) func()

	// OnUiPromptStart registers a handler for "ui_prompt_start", fired
	// without awaiting handlers when the outermost blocking ctx.ui prompt
	// (select, confirm, input, editor, custom) begins.
	// upstream: types.ts:1404
	OnUiPromptStart(handler ExtensionHandlerNoResult[UIPromptStartEvent]) func()

	// OnUiPromptEnd registers a handler for "ui_prompt_end", fired without
	// awaiting handlers when the outermost blocking ctx.ui prompt settles.
	// upstream: types.ts:1405
	OnUiPromptEnd(handler ExtensionHandlerNoResult[UIPromptEndEvent]) func()

	// OnCacheWarmingDecision registers a handler that may override the scheduled prompt-cache refresh decision.
	// Handlers are awaited in registration order and the last explicit action wins.
	// upstream: types.ts:1589 (ExtensionHandler<CacheWarmingDecisionEvent, CacheWarmingDecisionEventResult>)
	OnCacheWarmingDecision(handler ExtensionHandler[CacheWarmingDecisionEvent, CacheWarmingDecisionEventResult]) func()

	// OnTurnStart registers a handler for "turn_start".
	// upstream: types.ts:1095
	OnTurnStart(handler ExtensionHandlerNoResult[TurnStartEvent]) func()

	// OnTurnEnd registers an awaited turn-boundary handler. Like OnAgentBeforeSettle, a handler may
	// propose durable entries and request one runnable continuation.
	// upstream: types.ts:1613 (ExtensionHandler<TurnEndEvent, TurnEndEventResult>)
	OnTurnEnd(handler ExtensionHandler[TurnEndEvent, TurnEndEventResult]) func()

	// OnMessageStart registers a handler for "message_start".
	// upstream: types.ts:1097
	OnMessageStart(handler ExtensionHandlerNoResult[MessageStartEvent]) func()

	// OnMessageUpdate registers a handler for "message_update".
	// upstream: types.ts:1098
	OnMessageUpdate(handler ExtensionHandlerNoResult[MessageUpdateEvent]) func()

	// OnMessageEnd registers a handler for "message_end".
	// upstream: types.ts:1099
	OnMessageEnd(handler ExtensionHandler[MessageEndEvent, MessageEndEventResult]) func()

	// OnToolExecutionStart registers a handler for "tool_execution_start".
	// upstream: types.ts:1100
	OnToolExecutionStart(handler ExtensionHandlerNoResult[ToolExecutionStartEvent]) func()

	// OnToolExecutionUpdate registers a handler for "tool_execution_update".
	// upstream: types.ts:1101
	OnToolExecutionUpdate(handler ExtensionHandlerNoResult[ToolExecutionUpdateEvent]) func()

	// OnToolExecutionEnd registers a handler for "tool_execution_end".
	// upstream: types.ts:1102
	OnToolExecutionEnd(handler ExtensionHandlerNoResult[ToolExecutionEndEvent]) func()

	// OnModelSelect registers a handler for "model_select".
	// upstream: types.ts:1103
	OnModelSelect(handler ExtensionHandlerNoResult[ModelSelectEvent]) func()

	// OnThinkingLevelSelect registers a handler for "thinking_level_select".
	// upstream: types.ts:1122 (added in v0.71.0).
	OnThinkingLevelSelect(handler ExtensionHandlerNoResult[ThinkingLevelSelectEvent]) func()

	// OnToolCall registers a handler for "tool_call". The event is the union
	// [ToolCallEvent]. Type-switch on its concrete variant.
	// upstream: types.ts:1104
	OnToolCall(handler ExtensionHandler[ToolCallEvent, ToolCallEventResult]) func()

	// OnToolResult registers a handler for "tool_result". The event is the
	// union [ToolResultEvent].
	// upstream: types.ts:1105
	OnToolResult(handler ExtensionHandler[ToolResultEvent, ToolResultEventResult]) func()

	// OnUserBash registers a handler for "user_bash".
	// upstream: types.ts:1106
	OnUserBash(handler ExtensionHandler[UserBashEvent, UserBashEventResult]) func()

	// OnInput registers a handler for "input".
	// upstream: types.ts:1107
	OnInput(handler ExtensionHandler[InputEvent, InputEventResult]) func()

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

	// RegisterToolRenderer chooses how tool calls are drawn. Resolvers run in
	// extension load order.
	// upstream: types.ts registerToolRenderer
	RegisterToolRenderer(resolver ToolRendererResolver)

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
	// after the D3 migration; see docs/parity/DIVERGENCES.md).
	// upstream: types.ts:1190
	Exec(command string, args []string, options *ExecOptions) (ExecResult, error)

	// GetActiveTools returns the names of the active tools, which are the tools
	// declared to the model.
	// upstream: types.ts:1193
	GetActiveTools() []string

	// GetAllTools returns all configured tools with parameter schema, prompt
	// guidelines, exposure, and source metadata.
	// upstream: types.ts:1196
	GetAllTools() []ToolInfo

	// GetSettings returns a copy of the effective settings (global and project
	// settings merged, with overrides).
	// upstream: types.ts:1708 (getSettings)
	GetSettings() Settings

	// SetActiveTools sets the active tools by name. Unknown and `hidden` tools
	// are ignored. Tools with `codemode` or `deferred` exposure stay callable
	// from codemode scripts whether active or not.
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

	// RegisterNativeProvider is the Provider-object overload of RegisterProvider: it registers a provider whose
	// auth, model listing and streaming are its own functions ([ai.ModelsProvider] is Pi's Provider).
	// upstream: types.ts:1830 registerProvider(provider: Provider), loader.ts:456-464
	RegisterNativeProvider(provider *ai.ModelsProvider)

	// UnregisterProvider unregisters a previously registered provider. Has
	// no effect if the provider is not currently registered.
	// upstream: types.ts:1280
	UnregisterProvider(name string)

	// =====================================================================
	// MCP Servers
	// =====================================================================

	// RegisterMcpServer registers an MCP server for this session, with the same
	// config as an `mcpServers` entry in `mcp.json`. The server connects next to
	// the configured servers: on session_start when registered during extension
	// load, right away when registered later. Registering a name again replaces
	// the extension's earlier registration.
	//
	// The registration is not saved; register again on every load. A server of
	// the same name in `mcp.json` takes precedence. It returns an error for
	// invalid configs and for names another extension registered (upstream
	// throws). When no loaded extension handles MCP servers (for example because
	// another MCP extension replaced the built-in one), the registration is
	// reported as an extension error.
	// upstream: types.ts:1833 (registerMcpServer)
	RegisterMcpServer(name string, config McpServerConfig) error

	// UnregisterMcpServer removes an MCP server this extension registered and
	// closes its connection.
	// upstream: types.ts:1836 (unregisterMcpServer)
	UnregisterMcpServer(name string)

	// GetMcpServers returns every MCP server registered by extensions, for
	// extensions that connect MCP servers.
	// upstream: types.ts:1839 (getMcpServers)
	GetMcpServers() []RegisteredMcpServer

	// RegisterVirtualModel registers a virtual model: a selectable catalog entry
	// that routes each request to a physical model. The selection (`ctx.model`,
	// `model_change` entries) names the virtual model; assistant messages record
	// the physical model and thinking level the router picked.
	//
	// The provider may be any provider id, including one with physical models,
	// and may list several virtual models. Registering the same provider and id
	// again replaces the virtual model. See docs/virtual-models.md.
	// upstream: types.ts:1852 (registerVirtualModel)
	RegisterVirtualModel(model ExtensionVirtualModel)

	// UnregisterVirtualModel removes a virtual model registered with
	// [API.RegisterVirtualModel].
	// upstream: types.ts:1855 (unregisterVirtualModel)
	UnregisterVirtualModel(provider, id string)

	// =====================================================================
	// Event Bus (D1: upstream property → Go method)
	// =====================================================================

	// Events returns the shared event bus for extension communication.
	//
	// pig translation rule (interface property → method): upstream exposes
	// this as the property `events: EventBus`. Go interfaces cannot have
	// fields, so the API surfaces a method that returns the host's single
	// shared instance. See docs/parity/DIVERGENCES.md "TS→Go translation rituals".
	// upstream: types.ts:1283
	Events() EventBus
}
