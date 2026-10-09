package subprocess

import (
	"bytes"
	"fmt"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/orderedjson"
)

// pig additive (D19): autocomplete callback handles stay on their owner connection. ui.addAutocompleteProvider names a factoryId; autocomplete.sync wraps it around current or invokes apply/trigger, while autocomplete.suggest awaits getSuggestions. Captured references use ui.autocomplete.invoke and one-way release notifications. Columns are UTF-16. Parent cancellation owns handler calls; queryId also cancels editor-owned calls without a parent.
type autocompleteDescriptor struct {
	ID                string   `json:"id"`
	LocalID           string   `json:"localId,omitempty"`
	TriggerCharacters []string `json:"triggerCharacters"`
	HasFileTrigger    bool     `json:"hasFileTrigger"`
}

type autocompleteInvocation struct {
	QueryID    string                     `json:"queryId,omitempty"`
	ID         string                     `json:"id"`
	Operation  string                     `json:"operation"`
	Lines      []string                   `json:"lines"`
	CursorLine int                        `json:"cursorLine"`
	CursorCol  int                        `json:"cursorCol"`
	Force      bool                       `json:"force"`
	Item       extension.AutocompleteItem `json:"item"`
	Prefix     string                     `json:"prefix"`
}

// MaxFrameSize bounds a single length-prefixed wire frame. The 4-byte length
// prefix is a uint32, so the hard ceiling is ~4 GB; this cap guards against
// unbounded allocation from a corrupt/oversized length while still allowing the
// large payloads legitimate extensions request: notably getBranch on a long
// session, whose full branch history can run to tens of MB (an earlier 16 MB
// cap silently disabled context-info on big sessions). Upstream pi's Node IPC
// has no comparable hard cap. Must stay in sync with the Go/Rust/Python SDK
// MaxFrameSize constants.
const MaxFrameSize = 128 * 1024 * 1024

// legacyFrameSize is the historical extension IPC frame cap, in force before
// MaxFrameSize was raised to 128 MB. A subprocess binary built against a pig
// SDK from before that change rejects any frame larger than this, so a host
// that has advanced its cap can silently kill such a binary by sending a
// bigger frame (notably a large getBranch call_result). The host uses this
// constant to attribute that disconnect to extension-SDK frame-cap skew and
// tell the user to rebuild. It is a historical fact, not a tunable.
const legacyFrameSize = 16 * 1024 * 1024

// Message types for the wire protocol.
const (
	// Extension → Host
	MsgRegister     = "register"      // Initial handshake: declare capabilities
	MsgResponse     = "response"      // Reply to a host request
	MsgCall         = "call"          // Extension calls a host method (ui.notify, sendMessage, etc.)
	MsgWidgetPush   = "widget_push"   // Push rendered widget lines to host cache
	MsgPong         = "pong"          // Dispatcher heartbeat reply
	MsgRequestState = "request_state" // Request lifecycle/activity update

	// Host → Extension
	MsgReady      = "ready"       // Acknowledge registration, send context
	MsgRequest    = "request"     // Tool call or event dispatch (expects response)
	MsgNotify     = "notify"      // Fire-and-forget notification (no response)
	MsgCancel     = "cancel"      // Cancel an in-flight request by ID
	MsgCallResult = "call_result" // Reply to an extension call
	MsgShutdown   = "shutdown"    // Graceful shutdown signal
	MsgPing       = "ping"        // Dispatcher heartbeat request
)

// Envelope is the top-level wire message. Every message on the socket is an
// Envelope serialized as length-prefixed JSON. The Type field determines which
// payload fields are populated.
// pig additive (D19): Pig's language SDKs use this one current subprocess
// wire because upstream Pi loads only in-process TypeScript extensions.
type Envelope struct {
	Type string `json:"type"`

	// Common fields
	ID string `json:"id,omitempty"` // Correlation ID for request/response pairs

	// Register (ext→host)
	Register *RegisterPayload `json:"register,omitempty"`

	// Ready (host→ext)
	Ready *ReadyPayload `json:"ready,omitempty"`

	// Request (host→ext)
	Request *RequestPayload `json:"request,omitempty"`

	// Response (ext→host)
	Response *ResponsePayload `json:"response,omitempty"`

	// Notify (bidirectional)
	Notify *NotifyPayload `json:"notify,omitempty"`

	// Call (ext→host)
	Call *CallPayload `json:"call,omitempty"`

	// CallResult (host→ext)
	CallResult *CallResultPayload `json:"call_result,omitempty"`

	// WidgetPush (ext→host)
	WidgetPush *WidgetPushPayload `json:"widget_push,omitempty"`

	// Cancel (host→ext)
	Cancel *CancelPayload `json:"cancel,omitempty"`

	// Shutdown (host→ext)
	Shutdown *ShutdownPayload `json:"shutdown,omitempty"`

	// Heartbeat and request lifecycle.
	Ping         *PingPayload         `json:"ping,omitempty"`
	Pong         *PongPayload         `json:"pong,omitempty"`
	RequestState *RequestStatePayload `json:"request_state,omitempty"`
}

// ── Register (ext→host) ──────────────────────────────────────────────────────

// RegisterPayload declares an extension's capabilities at startup.
type RegisterPayload struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`

	Tools            []ToolDecl            `json:"tools,omitempty"`
	Commands         []CommandDecl         `json:"commands,omitempty"`
	Shortcuts        []ShortcutDecl        `json:"shortcuts,omitempty"`
	Handlers         []HandlerDecl         `json:"handlers,omitempty"`
	Flags            []FlagDecl            `json:"flags,omitempty"`
	Providers        []ProviderDecl        `json:"providers,omitempty"`
	MessageRenderers []MessageRendererDecl `json:"message_renderers,omitempty"`
	EntryRenderers   []EntryRendererDecl   `json:"entry_renderers,omitempty"`
	// MarkdownTransformer reports that the extension registered a Markdown
	// transformer. The host runs it with RequestMarkdownTransform.
	MarkdownTransformer bool `json:"markdown_transformer,omitempty"`
	// ToolRenderers is the number of tool renderer resolvers the extension
	// registered while loading (pi.registerToolRenderer). The host asks them
	// with RequestResolveToolRenderers; NotifyToolRenderers reports later ones.
	ToolRenderers int `json:"tool_renderers,omitempty"`
	// McpServers are the MCP servers the extension registered while loading. The host queues them like upstream's load-time registerMcpServer.
	McpServers []McpServerDecl `json:"mcp_servers,omitempty"`
	// VirtualModels are the virtual models the extension registered while loading. Routing calls back with RequestVirtualModelRoute.
	VirtualModels []VirtualModelDecl `json:"virtual_models,omitempty"`
	// UnregisterVirtualModels are the virtual models the extension unregistered while loading, in call order. Upstream's unregisterVirtualModel filters the runtime-wide queue of models registered before the runner binds, including another extension's (loader.ts:228-232), so the host applies them to that queue before it applies VirtualModels.
	UnregisterVirtualModels []VirtualModelRef `json:"unregister_virtual_models,omitempty"`

	// WantsSessionLog subscribes this extension to session-log replication from
	// the handshake, before the ready state is built, so the log is present on
	// the first read. Runtimes whose session readers can block on the host
	// subscribe on first use instead and leave this false; a runtime that
	// exposes those readers synchronously over asynchronous IPC has no such
	// moment and declares it here.
	WantsSessionLog bool `json:"wants_session_log,omitempty"`

	// WantsModelRegistry subscribes this extension to model-registry
	// replication: the ready payload's Models and every model_registry_update
	// notify. Runtimes whose ctx.modelRegistry readers can block on the host
	// read getModelRegistryState on use and leave this false, so the host
	// neither builds nor sends them a catalog snapshot; a runtime that answers
	// those readers synchronously over asynchronous IPC declares it here.
	WantsModelRegistry bool `json:"wants_model_registry,omitempty"`

	// flagDefaults holds each flag's decoded default. validateRegisterPayload
	// fills it, and rejects a default that is not valid JSON.
	flagDefaults map[string]any
}

func (p *RegisterPayload) UnmarshalJSON(data []byte) error {
	type registerPayload RegisterPayload
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var decoded registerPayload
	if err := decoder.Decode(&decoded); err != nil {
		return fmt.Errorf("decode register payload: %w", err)
	}
	*p = RegisterPayload(decoded)
	return nil
}

// CallRegisterTool carries one ToolDecl after the initial registration. The host validates and replaces the definition, refreshes the Session registry, and replies with allTools and activeTools before the SDK returns.
const CallRegisterTool = "registerTool"

// ToolDecl declares a tool the extension provides.
type ToolDecl struct {
	Name                string                     `json:"name"`
	Label               string                     `json:"label,omitempty"`
	Description         string                     `json:"description"`
	Parameters          json.RawMessage            `json:"parameters"`                     // JSON Schema
	ConstrainedSampling json.RawMessage            `json:"constrained_sampling,omitempty"` // false | ConstrainedSamplingConfig (ai.ConstrainedSamplingConfig JSON); false/null/absent all disable
	ExecutionMode       string                     `json:"execution_mode,omitempty"`       // "sequential" | "parallel"
	PromptSnippet       string                     `json:"prompt_snippet,omitempty"`
	PromptGuidelines    []string                   `json:"prompt_guidelines,omitempty"`
	Annotations         *extension.ToolAnnotations `json:"annotations,omitempty"` // upstream ToolDefinition.annotations
	// OutputSchema is upstream ToolDefinition.outputSchema.
	OutputSchema json.RawMessage `json:"output_schema,omitempty"`
	// Exposure is upstream ToolDefinition.exposure; empty is "direct".
	Exposure string `json:"exposure,omitempty"`
	// Namespace is upstream ToolDefinition.namespace.
	Namespace *extension.ToolNamespace `json:"namespace,omitempty"`
	// DefaultActive is upstream ToolDefinition.defaultActive; nil is the exposure's default.
	DefaultActive *bool `json:"default_active,omitempty"`
	// PreparesLoadout reports that the tool defines prepareLoadout. The host asks with RequestPrepareLoadout.
	PreparesLoadout bool `json:"prepares_loadout,omitempty"`
	// PreparesArguments reports that the tool defines prepareArguments. The host asks with RequestPrepareArguments before it validates the arguments.
	PreparesArguments bool   `json:"prepares_arguments,omitempty"`
	Source            string `json:"source,omitempty"` // pig additive (D23): per-tool source override; default: extension name
	// RenderShell is upstream ToolDefinition.renderShell: "self" when the
	// tool's renderers draw their own framing, else empty for "default".
	RenderShell string `json:"render_shell,omitempty"`
	// RendersCall and RendersResult report that the tool defines renderCall
	// and renderResult. The host asks for them with RequestRenderTool.
	RendersCall   bool `json:"renders_call,omitempty"`
	RendersResult bool `json:"renders_result,omitempty"`
	// BuiltInRenderers names the built-in tool whose host renderers draw the
	// halves the tool does not render itself: the Node runtime sets it for a
	// definition from Pi's create<Tool>ToolDefinition (D73).
	BuiltInRenderers string `json:"builtin_renderers,omitempty"`
	// ValidationParameters preserves non-enumerable TypeBox kinds separately from provider parameters.
	ValidationParameters json.RawMessage `json:"validation_parameters,omitempty"`
}

// CommandDecl declares a slash command the extension provides.
type CommandDecl struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	// ArgumentCompletions reports that the command defines upstream
	// getArgumentCompletions. The host asks for them with
	// RequestCommandArgumentCompletions.
	ArgumentCompletions bool `json:"argument_completions,omitempty"`
}

// ShortcutDecl declares a keyboard shortcut the extension binds.
type ShortcutDecl struct {
	Key         string `json:"key"` // e.g. "ctrl+shift+i"
	Description string `json:"description"`
}

// HandlerDecl declares which events the extension wants to receive.
type HandlerDecl struct {
	Event     string `json:"event"`      // e.g. "tool_call", "session_start"
	CanBlock  bool   `json:"can_block"`  // Whether the handler can block/mutate
	HandlerID int    `json:"handler_id"` // Positive registration identity.
}

// FlagDecl declares a CLI flag the extension consumes.
type FlagDecl struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Type        string          `json:"type"` // "boolean" | "string"
	Default     json.RawMessage `json:"default,omitempty"`
}

// ProviderDecl registers or overrides a model provider.
type ProviderDecl struct {
	StreamSimple bool                       `json:"stream_simple,omitempty"`
	Name         string                     `json:"name"`
	Config       json.RawMessage            `json:"config"`
	Native       *NativeProviderDeclaration `json:"native,omitempty"`
	// ImageAPIs and ClassifierAPIs name the APIs whose implementations (ProviderConfig images and classifiers) run in the extension.
	ImageAPIs      []string `json:"image_apis,omitempty"`
	ClassifierAPIs []string `json:"classifier_apis,omitempty"`
}

// NativeProviderDeclaration describes callback ownership, never executable code. Key identifies the owner's callback object; Handle identifies its host-owned reference lifetime. Methods lists the callable public members. Provider calls carry caller callbacks as request-scoped handles, and stream creation is acknowledged before ordered events.
type NativeProviderDeclaration struct {
	ID      string                          `json:"id"`
	Key     string                          `json:"key"`
	Handle  string                          `json:"handle,omitempty"`
	Headers *map[string]string              `json:"headers,omitempty"`
	Auth    *ProviderObjectAuthDeclaration  `json:"auth,omitempty"`
	Methods []string                        `json:"methods,omitempty"`
	Name    string                          `json:"name"`
	BaseURL *string                         `json:"baseUrl,omitempty"`
	Models  []extension.ProviderModelConfig `json:"models"`
	OAuth   *ProviderOAuthConfig            `json:"oauth,omitempty"`
}

type ProviderObjectAuthDeclaration struct {
	APIKey *ProviderObjectAuthMethodDeclaration `json:"apiKey,omitempty"`
	OAuth  *ProviderObjectAuthMethodDeclaration `json:"oauth,omitempty"`
}

type ProviderObjectAuthMethodDeclaration struct {
	Name           string  `json:"name"`
	IsSubscription *bool   `json:"isSubscription,omitempty"`
	LoginLabel     *string `json:"loginLabel,omitempty"`
}

type ProviderObjectCall struct {
	Handle     string          `json:"handle"`
	Method     string          `json:"method"`
	Params     json.RawMessage `json:"params"`
	StreamID   string          `json:"streamId,omitempty"`
	CallbackID string          `json:"callbackId,omitempty"`
}

const CallProviderObject = "provider.object"
const MethodProviderSync = "provider_sync"
const MethodProviderObjectCallback = "provider_object_callback"
const MethodProviderCall = "provider_call"
const MethodProviderStream = "provider_stream"

// MethodToolCall runs a registered tool's execute.
const MethodToolCall = "tool_call"
const CallProviderCallback = "provider.callback"

// ModelStreamCall invokes a registry stream or an already-resolved API leaf.
// Callback capabilities remain scoped to StreamID and the originating connection.
type ModelStreamCall struct {
	StreamID         string          `json:"streamId"`
	Simple           bool            `json:"simple,omitempty"`
	APIRequest       bool            `json:"apiRequest,omitempty"`
	Fetch            bool            `json:"fetch,omitempty"`
	OnPayload        bool            `json:"onPayload,omitempty"`
	OnResponse       bool            `json:"onResponse,omitempty"`
	TransformHeaders bool            `json:"transformHeaders,omitempty"`
	Model            map[string]any  `json:"model"`
	Request          json.RawMessage `json:"request"`
}

const MethodModelStreamCallback = "model_stream_callback"

// ModelStreamCallback invokes one advertised callback before its provider boundary proceeds.
type ModelStreamCallback struct {
	StreamID string `json:"streamId"`
	Callback string `json:"callback"`
	Value    any    `json:"value"`
}

// ── OAuth provider bridge (additive, the current subprocess wire) ────────────────────────────
//
// A subprocess extension contributes an OAuth provider by putting a
// ProviderOAuthConfig under the "oauth" key of its ProviderDecl.Config. The host
// builds an ai.OAuthProviderInterface proxy whose methods RPC the oauth_*
// request methods back into the extension; during an in-flight oauth_login the
// extension drives the host login UI with the oauth.cb.* call methods. These are
// new method names on the existing MsgRequest / MsgCall frames: not a new
// message type and not a protocol version bump. An extension built against an
// older SDK never sets the capability flags, so the host never dispatches them.

// ProviderOAuthConfig is the "oauth" sub-object inside a ProviderDecl.Config. It
// carries declarative fields and capability flags only, never functions.
type ProviderOAuthConfig struct {
	Name               string `json:"name"`
	IsSubscription     bool   `json:"isSubscription,omitempty"`
	HasLogin           bool   `json:"has_login"`
	HasRefresh         bool   `json:"has_refresh"`
	HasGetAPIKey       bool   `json:"has_get_api_key"`
	HasModifyModels    bool   `json:"has_modify_models,omitempty"`
	HasCredentialStore bool   `json:"has_credential_store,omitempty"`
}

// OAuth bridge request methods (host→ext), carried in RequestPayload.Method.
const (
	MethodOAuthLogin             = "oauth_login"              // Args: none.                Result: OAuthCredentialsWire.
	MethodOAuthRefresh           = "oauth_refresh"            // Args: OAuthCredentialsWire. Result: OAuthCredentialsWire.
	MethodOAuthGetAPIKey         = "oauth_get_api_key"        // Args: OAuthCredentialsWire. Result: OAuthAPIKeyResult.
	MethodOAuthCredentialStatus  = "oauth_credential_status"  // Args: none.                Result: OAuthCredentialStatusResult.
	MethodOAuthStoreCredentials  = "oauth_store_credentials"  // Args: OAuthCredentialsWire. Result: OAuthStoreResult.
	MethodOAuthDeleteCredentials = "oauth_delete_credentials" // Args: none.                Result: OAuthDeleteResult.
)

// OAuth login-callback methods (ext→host), carried in CallPayload.Method. Issued
// by the extension while its login flow is in flight to drive the host UI.
const (
	CallOAuthOnAuth            = "oauth.cb.onAuth"            // Args: OAuthAuthInfoWire.       No result.
	CallOAuthOnDeviceCode      = "oauth.cb.onDeviceCode"      // Args: OAuthDeviceCodeInfoWire. No result.
	CallOAuthOnProgress        = "oauth.cb.onProgress"        // Args: OAuthProgressWire.       No result.
	CallOAuthOnPrompt          = "oauth.cb.onPrompt"          // Args: OAuthPromptWire.         Result: OAuthInputResult.
	CallOAuthOnSelect          = "oauth.cb.onSelect"          // Args: OAuthSelectPromptWire.   Result: OAuthInputResult.
	CallOAuthOnManualCodeInput = "oauth.cb.onManualCodeInput" // Args: none, or a provider object's manual_code AuthPrompt. Result: OAuthInputResult.
)

// OAuthCredentialsWire is the wire shape of OAuth credentials: Pi's complete token object, including provider-owned keys and the exact JavaScript expires value.
type OAuthCredentialsWire = ai.OAuthCredentials

// OAuthAuthInfoWire, OAuthDeviceCodeInfoWire, OAuthProgressWire, OAuthPromptWire,
// OAuthSelectPromptWire, and OAuthSelectOptionWire carry the login-callback
// payloads. The host converts to/from the untagged ai callback types.
type OAuthAuthInfoWire struct {
	URL          string `json:"url"`
	Instructions string `json:"instructions,omitempty"`
}

type OAuthDeviceCodeInfoWire struct {
	UserCode         string  `json:"userCode"`
	VerificationURI  string  `json:"verificationUri"`
	IntervalSeconds  float64 `json:"intervalSeconds,omitempty"`
	ExpiresInSeconds float64 `json:"expiresInSeconds,omitempty"`
}

type OAuthProgressWire struct {
	Message string `json:"message"`
}

type OAuthPromptWire struct {
	Message     string `json:"message"`
	Placeholder string `json:"placeholder,omitempty"`
	AllowEmpty  bool   `json:"allowEmpty,omitempty"`
}

type OAuthSelectOptionWire struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

type OAuthSelectPromptWire struct {
	Message string                  `json:"message"`
	Options []OAuthSelectOptionWire `json:"options"`
}

// OAuthAPIKeyResult is the reply to MethodOAuthGetAPIKey.
type OAuthAPIKeyResult struct {
	APIKey string `json:"apiKey"`
}

// OAuthInputResult is the reply to a value-returning login callback. Cancel is
// true when the user dismissed the prompt.
type OAuthInputResult struct {
	Value  string `json:"value"`
	Cancel bool   `json:"cancel,omitempty"`
}

// OAuthCredentialStatusResult is the reply to MethodOAuthCredentialStatus.
type OAuthCredentialStatusResult struct {
	Present  bool   `json:"present"`
	AuthType string `json:"authType,omitempty"`
	Source   string `json:"source,omitempty"`
}

// OAuthStoreResult is the reply to MethodOAuthStoreCredentials.
type OAuthStoreResult struct {
	Path string `json:"path"`
}

// OAuthDeleteResult is the reply to MethodOAuthDeleteCredentials.
type OAuthDeleteResult struct {
	Deleted bool `json:"deleted"`
}

// MessageRendererDecl declares a custom message renderer.
type MessageRendererDecl struct {
	CustomType string `json:"custom_type"`
}

// EntryRendererDecl declares a custom session-entry renderer.
type EntryRendererDecl struct {
	CustomType string `json:"custom_type"`
}

// ── Ready (host→ext) ─────────────────────────────────────────────────────────

// ReadyPayload is sent after successful registration.
type ReadyPayload struct {
	SessionName string           `json:"session_name,omitempty"` // Current session name
	Cwd         string           `json:"cwd"`                    // Working directory
	Mode        string           `json:"mode,omitempty"`         // Run mode: tui|rpc|json|print (ctx.mode)
	Width       int              `json:"width"`                  // Terminal width
	Height      int              `json:"height,omitempty"`       // Terminal height (0 if unavailable)
	Model       string           `json:"model,omitempty"`        // Active model name
	Models      []map[string]any `json:"models,omitempty"`       // Complete registry snapshot, sent only to a runtime that declared RegisterPayload.WantsModelRegistry
	Theme       json.RawMessage  `json:"theme,omitempty"`        // Current theme data
	State       *StatePayload    `json:"state,omitempty"`        // Initial extension-host state snapshot
}

// StatePayload is the host's view of state visible to extensions through
// the upstream-faithful synchronous getters (pi.getActiveTools(),
// pi.getThinkingLevel(), ctx.isIdle(), ctx.getContextUsage(), etc.). It is
// sent with [ReadyPayload] at startup and re-sent via "state_update"
// notifies whenever the host knows it has changed.
// State fields carry explicit empty/null values so a snapshot clears prior values; session log pages remain incremental.
type StatePayload struct {
	ActiveTools         []string                   `json:"activeTools"`
	AllTools            []ToolInfo                 `json:"allTools"`
	Commands            []CommandInfo              `json:"commands"`
	ThinkingLevel       string                     `json:"thinkingLevel"`
	Model               map[string]any             `json:"model"`
	ScopedModels        []scopedModelSnapshot      `json:"scopedModels"`
	Session             *SessionStatePayload       `json:"session,omitempty"`
	IsIdle              bool                       `json:"isIdle"`
	ProjectTrusted      bool                       `json:"projectTrusted"`
	HasPendingMessages  bool                       `json:"hasPendingMessages"`
	ContextUsage        *extensionContextUsageDTO  `json:"contextUsage"`
	SystemPrompt        string                     `json:"systemPrompt"`
	SystemPromptOptions json.RawMessage            `json:"systemPromptOptions,omitempty"`
	Flags               map[string]json.RawMessage `json:"flags"`
	HasUI               bool                       `json:"hasUI"`
	// Frontend reports that a D91 frontend session draws the interactive
	// mode (D107): the Node runtime derives views only then, and SDKs send
	// frontend-only view annotations only then.
	Frontend   bool               `json:"frontend,omitempty"`
	FooterData *FooterDataPayload `json:"footerData,omitempty"`
	// Settings is the effective settings object behind pi.getSettings() (global and project merged, with overrides).
	Settings json.RawMessage `json:"settings,omitempty"`
	// McpServers is every server registered by extensions, behind pi.getMcpServers().
	McpServers []extension.RegisteredMcpServer `json:"mcpServers"`
	// Always serialized: an emptied editor must clear the replicated value
	// rather than leave the previous text in place.
	EditorText    string         `json:"editorText"`
	ToolsExpanded bool           `json:"toolsExpanded"`
	AllThemes     []themeMetaDTO `json:"allThemes"`
	// TerminalCapabilities is the host terminal's resolved capabilities
	// (detection plus settings overrides). The Node runtime seeds pi-tui's
	// capability cache with it, so Pi's Markdown renders links as the host
	// terminal supports them.
	TerminalCapabilities *TerminalCapabilitiesPayload `json:"terminalCapabilities,omitempty"`
	// Theme is the host's active theme as extensions see it (ctx.ui.theme):
	// its name, foreground and background escape sequences by token, and
	// whether chalk styles draw. The Node runtime's theme helpers
	// (getSelectListTheme, highlightCode, keyHint, ...) color with it.
	Theme any `json:"theme,omitempty"`
	// Keybindings is Pi's keybinding table as the host resolved it: every
	// tui.* and app.* definition with the user's keybindings.json overrides.
	// The Node runtime installs it as pi-tui's keybindings manager, the one
	// Pi hands editor and custom-component factories and sets globally.
	Keybindings any `json:"keybindings,omitempty"`
}

// TerminalCapabilitiesPayload mirrors pi-tui's TerminalCapabilities.
type TerminalCapabilitiesPayload struct {
	Images     string `json:"images,omitempty"` // "kitty", "iterm2", or empty for none
	TrueColor  bool   `json:"trueColor"`
	Hyperlinks bool   `json:"hyperlinks"`
}

// themeMetaDTO mirrors extension.ThemeMeta on the wire. The Node runtime
// answers ui.getAllThemes and ui.getTheme synchronously, so the theme list
// travels with the state snapshot rather than as a call.
type themeMetaDTO struct {
	Name string `json:"name"`
	Path string `json:"path,omitempty"`
}

// FooterDataPayload carries the data behind upstream's
// ReadonlyFooterDataProvider (footer-data-provider.ts:387), the third argument
// handed to a ui.setFooter factory.
type FooterDataPayload struct {
	GitBranch              string            `json:"gitBranch,omitempty"`
	ExtensionStatuses      map[string]string `json:"extensionStatuses,omitempty"`
	AvailableProviderCount int               `json:"availableProviderCount"`
}

// SessionStatePayload mirrors the synchronous sessionManager getters exposed
// to TS extensions (session id/name/file, current leaf, branch, full entry
// list). Branch/Entries carry raw session-entry objects serialized from the
// host session file.
type SessionStatePayload struct {
	SessionID   string `json:"sessionId,omitempty"`
	SessionName string `json:"sessionName"`
	SessionFile string `json:"sessionFile"`
	LeafID      string `json:"leafId"`
	// EntriesAppended carries only the next bounded page after this extension's
	// cursor. EntryCount is the cursor after applying the page. When
	// EntriesRemaining is true, the host sends another ordered state update.
	EntriesAppended  []json.RawMessage `json:"entriesAppended,omitempty"`
	EntryCount       int               `json:"entryCount"`
	EntriesRemaining bool              `json:"entriesRemaining,omitempty"`
	// Info carries the session manager facts the log does not hold: the
	// header, cwd, session directory, and whether the session persists
	// (upstream getHeader, getCwd, getSessionDir, isPersisted,
	// usesDefaultSessionDir).
	Info json.RawMessage `json:"info,omitempty"`
}

// extensionContextUsageDTO mirrors extension.ContextUsage on the wire. We
// duplicate it here to avoid importing the public extension package from
// the protocol layer.
type extensionContextUsageDTO struct {
	Tokens        *int     `json:"tokens"`
	ContextWindow int      `json:"contextWindow"`
	Percent       *float64 `json:"percent"`
}

// ── Request (host→ext) ───────────────────────────────────────────────────────

// RequestPayload carries a host request that expects a response.
type RequestPayload struct {
	Method     string          `json:"method"` // "tool_call", "event", "command", "shortcut", "render_message", "render_entry", "render_tool"
	Tool       string          `json:"tool,omitempty"`
	Event      string          `json:"event,omitempty"`
	HandlerID  int             `json:"handler_id,omitempty"`
	ToolCallID string          `json:"tool_call_id,omitempty"` // For tool_call: unique ID
	Args       json.RawMessage `json:"args,omitempty"`         // Tool args or event payload
	// SignalTimeoutMS, on oauth_refresh, tells a runtime whose refresh callback receives an AbortSignal that the signal is AbortSignal.timeout(SignalTimeoutMS) alone: the caller's cancellation never reaches it, as in Pi's refreshStoredOAuthCredential (auth/resolve.ts). Absent, the signal follows the request's cancellation.
	SignalTimeoutMS *float64 `json:"signal_timeout_ms,omitempty"`
	// OwnSignal, on tool_call, reports a nested call that runs with the signal its caller passed in options.signal: the tool's signal parameter is that signal, not the run's.
	OwnSignal bool `json:"own_signal,omitempty"`
	// ExecuteID, with OwnSignal, names the executeTool call of the same connection that made this nested call, so its runtime can hand the tool the very signal object its caller passed.
	ExecuteID string `json:"execute_id,omitempty"`
}

// RequestWithSession (host→ext) runs the withSession callback that a newSession, fork or switchSession call registered under WithSessionArgs.Handle. The call's args name that handle in their withSession field. The host sends it after the replacement Session is bound and before the call returns, as Pi's finishSessionReplacement awaits withSession (agent-session-runtime.ts:187-194). Host calls whose parent is this request act on the replacement Session; the response settles the callback, and its error rejects the call.
const RequestWithSession = "with_session"

// RequestSetup (host→ext) runs the setup callback of a newSession call that named it under SetupArgs.Handle in its setup field (types.ts:411, agent-session-runtime.ts:254-257). The host sends it after the replacement Session is bound and before the withSession callback runs. Host calls whose parent is this request act on the replacement Session: sessionRead reads its SessionManager and sessionWrite appends to it (SessionManager.appendMessage, appendCustomEntry, appendCustomMessageEntry, appendSessionInfo, appendModelChange, appendThinkingLevelChange, appendLabelChange). The response settles the callback, and its error rejects the newSession call.
const RequestSetup = "setup"

// SetupArgs carries the setup callback handle.
type SetupArgs struct {
	Handle string `json:"handle"`
}

// WithSessionArgs carries the callback handle and the replacement Session's ready payload. A runtime that answers context getters from local state answers the replacement context from Ready.
type WithSessionArgs struct {
	Handle string        `json:"handle"`
	Ready  *ReadyPayload `json:"ready"`
}

// NotifyInvalidate (host→ext) carries the stale message after the host invalidated the extension's Session. A runtime that answers pi or ctx members locally throws InvalidateArgs.Message from them afterwards, as Pi's runner.invalidate makes them throw (runner.ts:679-690).
const NotifyInvalidate = "invalidate"

// InvalidateArgs is the NotifyInvalidate argument.
type InvalidateArgs struct {
	Message string `json:"message"`
}

// NotifyReloadStarted (host→ext) tells a running generation that Host.Reload has begun, before the host writes the replacement generations' admissions to the process's control channel. In print and JSON mode a Node runtime that answered a prompt's last call or session_shutdown waits in place for the host's next step (Pi's print mode continues without an event-loop turn); this notification is that step for a reload, so the runtime returns to its event loop, which reads the admissions. It carries no arguments and changes no state; the Go, Rust and Python SDKs, which have no such wait, ignore it.
const NotifyReloadStarted = "reload_started"

// NotifyLoopTurned (Node runtime→host, then host→ext) tells the host that a handler yielded to its process's event loop, and the host tells every other Node process. Pi runs every extension on one event loop, so while one extension's handler awaits a timer, I/O or a child process, the callbacks another extension queued run, with a live ctx. In print and JSON mode a Node runtime sends it when a request's turn window closes with the request unanswered: after the handler's synchronous run and microtasks, once the short host calls Pi settles in microtasks have settled. The Conn read loop hands it to the host before it reads any later frame of the connection, such as that handler's response, and the host queues it on every connection of every other Node process, so each one reads it before anything the host sends after that response, such as invalidate. A Node runtime waiting for the host's next step after a prompt's last call or session_shutdown takes it as that step, and delivers the frames that follow it only after the immediates it had queued have run. It carries no arguments and changes no state; the Go, Rust and Python SDKs, which have no such wait, ignore it.
const NotifyLoopTurned = "loop_turned"

// TerminalInputArgs carries an ordered input and the UI values visible before its listener runs. Data and EditorText preserve UTF-16 units using WTF-8 in Go and surrogate escapes in JSON. Runtimes with synchronous local UI getters refresh those values before invoking the listener; host-query SDKs read the same live UI through their existing calls.
type TerminalInputArgs struct {
	Data          string `json:"data"`
	EditorText    string `json:"editorText"`
	ToolsExpanded bool   `json:"toolsExpanded"`
}

// ── Response (ext→host) ──────────────────────────────────────────────────────

// BoundaryEventResultPayload is the current-wire result for turn_end and
// agent_before_settle. Entries is the complete replacement proposal; pointers
// preserve an omitted field from an explicit empty list or false continuation.
type BoundaryEventResultPayload struct {
	Entries  *json.RawMessage `json:"entries,omitempty"`
	Continue *bool            `json:"continue,omitempty"`
}

// BeforeAgentStartResponsePayload carries per-run mutations even when a handler fails. SelectedTools retains the untyped value until the handler chain ends: non-string entries cannot name registered tools, and null rejects prompt admission. Options is the handler's whole options object; the host applies every field except sections and selectedTools from it. Result carries the handler's ordinary message/systemPrompt result.
type BeforeAgentStartResponsePayload struct {
	Sections      ai.OrderedSections `json:"_pigPromptSections"`
	SelectedTools json.RawMessage    `json:"_pigPromptSelectedTools"`
	Options       json.RawMessage    `json:"_pigPromptOptions"`
	Result        json.RawMessage    `json:"_pigPromptResult"`
}

// ToolCallResponsePayload is the result of a tool_call handler. Input is the event's input when the handler left it different from the one it received; an omitted Input means the handler made no edit. Result is the handler's ordinary block result. Any other member is an error: a reply in another shape would otherwise decode as no result and lose a block.
type ToolCallResponsePayload struct {
	Input  json.RawMessage `json:"_pigToolCallInput"`
	Result json.RawMessage `json:"_pigToolCallResult"`
}

func (p *ToolCallResponsePayload) UnmarshalJSON(data []byte) error {
	type toolCallResponsePayload ToolCallResponsePayload
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var decoded toolCallResponsePayload
	if err := decoder.Decode(&decoded); err != nil {
		return err
	}
	*p = ToolCallResponsePayload(decoded)
	return nil
}

// ResponsePayload is the extension's reply to a request. For tool_call, Result carries a [ToolCallResponsePayload]. For agent_before_settle and turn_end,
// Result carries {_pigBoundaryEntries, _pigBoundaryResult}: the mutated input
// draft list and the explicit handler result. Mutations also accompany Error;
// the host applies them before surfacing the error and ignores the explicit result.
type ResponsePayload struct {
	Result json.RawMessage `json:"result,omitempty"` // Tool result, event result, etc.
	Error  *ErrorInfo      `json:"error,omitempty"`  // Non-nil on failure
}

// ErrorInfo carries structured error information.
type ErrorInfo struct {
	Code    string `json:"code,omitempty"`
	Message string `json:"message"`
	// Stack is the thrown error's own stack when the extension runtime has
	// one (a JavaScript `err.stack`). Hosts report it as upstream reports
	// `ExtensionError.stack`.
	Stack string `json:"stack,omitempty"`
}

func (e *ErrorInfo) ToError() error {
	if e == nil {
		return nil
	}
	message := e.Message
	if e.Code != "" {
		message = fmt.Sprintf("[%s] %s", e.Code, e.Message)
	}
	return &remoteError{message: message, stack: e.Stack}
}

// remoteError is an error an extension process returned. It carries the
// extension's own stack for [extension.ErrorStack].
type remoteError struct {
	message string
	stack   string
}

func (e *remoteError) Error() string      { return e.message }
func (e *remoteError) ErrorStack() string { return e.stack }

// ── Notify (bidirectional) ───────────────────────────────────────────────────

// NotifyPayload carries a fire-and-forget notification. The host-only runtime_input_end notification removes stdin as a Node keepalive and releases each command's suspension: a command's request_state suspended is sent only after it, as a re-evaluation of the command's closed window (a command that started or finished a host call since its window closed gets a fresh window). After it, a Node process reports when its event loop drained, without arguments, on each connection it hosts, and the host applies runtime_drained and runtime_quit_yield to the process after every connection it holds for that process reported or closed. runtime_drained reports a still-pending quit handler whose event loop drained: Pi's exit when its loop empties, which takes the host's loop-drain checkpoint. runtime_commands_drained reports, from a process with no pending quit handler, that its event loop drained with an unanswered command it had not reported: the process cannot answer the commands whose started request_state precedes the report on each connection, and the host does not end. A command read after a report is reported again if the loop drains with it unanswered. runtime_quit_yield reports a post-disposal boundary that suspended beyond its immediately fulfilled continuations: Pi's exit at the stdout flush, which applies the flush checkpoint. Command suspension travels as request_state, after runtime_input_end. None completes an extension request.
type NotifyPayload struct {
	Method string          `json:"method"` // e.g. "width_change", "widget_invalidate"
	Args   json.RawMessage `json:"args,omitempty"`
}

// NotifyLoadFailed (ext→host) takes the place of the register handshake
// when an extension's module or factory throws: the Node runtime reports its
// loader's error, as Pi's loader words it, and closes. Args is a
// FactoryLoadError.
const NotifyLoadFailed = "load_failed"

// FactoryLoadError is an extension's own report that it failed to load.
// Message is Pi's loader error ("Failed to load extension: <message>", or the
// missing-factory message); Stack is the thrown error's stack.
type FactoryLoadError struct {
	Message string `json:"error"`
	Stack   string `json:"stack,omitempty"`
}

func (e *FactoryLoadError) Error() string      { return e.Message }
func (e *FactoryLoadError) ErrorStack() string { return e.Stack }

// RequestProviderStream invokes a legacy streamSimple callback. Native Provider objects retain their provider_stream method/params carrier.
const RequestProviderStream = "provider_stream_simple"

// NotifyProviderStreamEvent carries ordered assistant events before the final request response.
const NotifyProviderStreamEvent = "provider_stream_event"

// NotifyToolUpdate (ext→host) carries a tool's partial result while its
// tool_call request runs, as upstream's onUpdate(partialResult) does. The host
// delivers it to the running tool in frame order, before the final response.
const NotifyToolUpdate = "tool_update"

// ToolUpdatePayload is the NotifyToolUpdate argument. Result has the shape of
// a ToolResult.
type ToolUpdatePayload struct {
	RequestID string          `json:"request_id"`
	Result    json.RawMessage `json:"result"`
}

// Focused custom UI uses three current-wire message classes. Input is an
// ordered, non-idempotent event; render is a replaceable snapshot; open and
// close/error are exactly-once barriers that delimit one key generation.
const (
	CallUICustom = "ui.custom"
	// pig additive (D60): typed native login crosses the subprocess boundary.
	CallUISetLogin = "ui.setLogin"
	// pig divergence (D2): an extension's sprite joins /sprite.
	CallUIRegisterSprite = "ui.registerSprite"
	NotifyUICustomInput  = "ui.custom.input"
	NotifyUICustomRender = "ui.custom.render"
	NotifyUICustomClose  = "ui.custom.close"
	NotifyUICustomOpened = "ui.custom.opened"
	CallUICustomControl  = "ui.custom.control"
	// NotifyUICustomMouse (host→ext) hands a fullscreen
	// mouse event to a ui.custom component that takes the mouse, as Pi's
	// renderer calls its handleMouse.
	NotifyUICustomMouse = "ui.custom.mouse"
)

type RemoteOverlayOpenPayload struct {
	Key            string  `json:"key"`
	Title          string  `json:"title,omitempty"`
	WidthFraction  float64 `json:"widthFraction,omitempty"`
	HeightFraction float64 `json:"heightFraction,omitempty"`
	Overlay        bool    `json:"overlay,omitempty"`
	HasHandle      bool    `json:"hasHandle,omitempty"`
	// OverlayOptions is upstream ui.custom()'s serialisable overlayOptions.
	OverlayOptions *extension.OverlayLayout `json:"overlayOptions,omitempty"`
}

type RemoteOverlayControlPayload struct {
	Key    string                      `json:"key"`
	Action string                      `json:"action"`
	Hidden bool                        `json:"hidden,omitempty"`
	Target *RemoteOverlayTargetPayload `json:"target,omitempty"`
}

// RemoteOverlayTargetPayload names an explicit unfocus target: "null", "editor", or "overlay" with the key of another mounted overlay of the same extension.
type RemoteOverlayTargetPayload struct {
	Kind string `json:"kind"`
	Key  string `json:"key,omitempty"`
}

type RemoteOverlayInputPayload struct {
	Key   string                        `json:"key"`
	Data  string                        `json:"data"`
	State *extension.RemoteOverlayState `json:"state,omitempty"`
}

// RemoteOverlayMousePayload is a NotifyUICustomMouse argument: Event is
// local to the component's first rendered cell.
type RemoteOverlayMousePayload struct {
	Key   string                     `json:"key"`
	Event extension.RemoteMouseEvent `json:"event"`
}

type RemoteOverlayRenderPayload struct {
	Key string `json:"key"`
	// Lines are the frame's rows. Absent (nil) with a View, the view is
	// authoritative and the host renders it (D107).
	Lines []string `json:"lines"`
	// Width is the terminal width the frame was laid out for (stale-frame
	// key), not the overlay render width.
	Width int    `json:"width,omitempty"`
	Seq   uint64 `json:"seq,omitempty"`
	// View is the frame's component structure, a [ViewPayload] (D107).
	View json.RawMessage `json:"view,omitempty"`
	// Mouse reports a component that takes the mouse:
	// the host hands it NotifyUICustomMouse events inside its bounds.
	Mouse bool `json:"mouse,omitempty"`
}

// pig additive (D107): the extension component kit. A view describes an
// extension surface as Pi's tui components. With lines absent it is
// authoritative: the host renders it with its tui ports. With lines present
// (Node) it annotates them and survives only when rendering it reproduces
// them byte for byte. See docs/plan/extension-component-kit.md.
const (
	// NotifyUIViewEvent (host→ext) reports a callback of an interactive view
	// node (select-list, settings-list) of a ui.custom frame, in firing
	// order after every ui.custom.input sent before it.
	NotifyUIViewEvent = "ui.view.event"
	// NotifyUIViewEvicted (host→ext) reports image refs whose bytes the
	// host dropped; the next frame that references one sends its data again.
	NotifyUIViewEvicted = "ui.view.evicted"
)

// View node kinds. Each mirrors the upstream pi-tui (or coding-agent
// DynamicBorder) component of the same name; ViewKindLines is an opaque
// range of rows a custom render(width) drew.
const (
	ViewKindContainer     = "container"
	ViewKindBox           = "box"
	ViewKindText          = "text"
	ViewKindTruncatedText = "truncated-text"
	ViewKindMarkdown      = "markdown"
	ViewKindSpacer        = "spacer"
	ViewKindDynamicBorder = "dynamic-border"
	ViewKindSelectList    = "select-list"
	ViewKindSettingsList  = "settings-list"
	ViewKindImage         = "image"
	ViewKindLoader        = "loader"
	ViewKindHStack        = "hstack"
	ViewKindVStack        = "vstack"
	ViewKindLines         = "lines"
	// The conversation kinds mirror the coding-agent components Pi exports
	// to extensions (UserMessageComponent, AssistantMessageComponent,
	// ToolExecutionComponent, BashExecutionComponent and renderDiff in a
	// Text).
	ViewKindUserMessage      = "user-message"
	ViewKindAssistantMessage = "assistant-message"
	ViewKindToolExecution    = "tool-execution"
	ViewKindBashExecution    = "bash-execution"
	ViewKindDiff             = "diff"
)

// Tool definitions of a tool-execution node: the renderers a closure-free
// tool definition names.
const (
	// ViewToolDefinitionBuiltin is the built-in tool's definition when the
	// tool name has one, and a definition without renderers otherwise: the
	// card the main transcript draws for a tool without its own renderers.
	ViewToolDefinitionBuiltin = "builtin"
	// ViewToolDefinitionEmpty is a definition without renderers ({}).
	ViewToolDefinitionEmpty = "empty"
)

// View event types: the upstream callbacks of SelectList (onSelect,
// onCancel, onSelectionChange) and SettingsList (onChange, onCancel).
const (
	ViewEventSelect          = "select"
	ViewEventCancel          = "cancel"
	ViewEventSelectionChange = "selectionChange"
	ViewEventChange          = "change"
)

// ViewPayload is one view: a component tree with the surface's focus,
// theme overrides and the image bytes first sent with this frame.
type ViewPayload struct {
	Root ViewNode `json:"root"`
	// Focus is the id of the select-list or settings-list that receives
	// the keys it binds (ui.custom only).
	Focus string `json:"focus,omitempty"`
	// Theme overrides theme tokens for this surface only: token name to
	// "#rrggbb".
	//portlint:allow emptydrop the D107 view wire is PiG-owned with no Pi counterpart, and an absent and an empty theme mean the same: none
	Theme map[string]string `json:"theme,omitempty"`
	// Width is the cells the sender laid the frame's lines out at; required
	// when the frame also carries lines.
	Width int `json:"width,omitempty"`
	// Images carries image bytes the first time a frame on the connection
	// references them.
	//portlint:allow emptydrop the D107 view wire is PiG-owned with no Pi counterpart, and an absent and an empty image list mean the same: none
	Images []ViewImageData `json:"images,omitempty"`
}

// ViewNode is one component. Fields mirror the upstream constructor
// parameters and state; an omitted field takes the upstream default.
type ViewNode struct {
	Kind string `json:"kind"`
	// ID names the node; required and unique for select-list and
	// settings-list.
	ID string `json:"id,omitempty"`
	//portlint:allow emptydrop the D107 view wire is PiG-owned with no Pi counterpart, and an absent and an empty child list mean the same: none
	Children []ViewNode `json:"children,omitempty"`
	// Stack holds the StackEntry options of a child of hstack or vstack.
	Stack *ViewStackEntry `json:"stack,omitempty"`

	// Text is the text of text, truncated-text and markdown.
	Text     string `json:"text,omitempty"`
	PaddingX *int   `json:"paddingX,omitempty"`
	PaddingY *int   `json:"paddingY,omitempty"`
	// Bg is the background token of box (bgFn) and text (customBgFn).
	Bg string `json:"bg,omitempty"`
	// Color is dynamic-border's foreground token (default "border").
	Color            string         `json:"color,omitempty"`
	DefaultTextStyle *ViewTextStyle `json:"defaultTextStyle,omitempty"`
	RenderLatex      *bool          `json:"renderLatex,omitempty"`
	// Lines is a spacer's line count.
	Lines *int `json:"lines,omitempty"`
	//portlint:allow emptydrop the D107 view wire is PiG-owned with no Pi counterpart, and an absent and an empty item list mean the same: none
	Items         []ViewItem        `json:"items,omitempty"`
	MaxVisible    *int              `json:"maxVisible,omitempty"`
	Layout        *ViewSelectLayout `json:"layout,omitempty"`
	SelectedIndex *int              `json:"selectedIndex,omitempty"`
	Filter        *string           `json:"filter,omitempty"`
	EnableSearch  bool              `json:"enableSearch,omitempty"`
	// Ref names an image node's bytes.
	Ref            string `json:"ref,omitempty"`
	MimeType       string `json:"mimeType,omitempty"`
	MaxWidthCells  *int   `json:"maxWidthCells,omitempty"`
	MaxHeightCells *int   `json:"maxHeightCells,omitempty"`
	// ImageID is upstream ImageOptions.imageId, the Kitty image id; an
	// absent one is allocated by the host as upstream allocates it.
	ImageID       *int   `json:"imageId,omitempty"`
	Filename      string `json:"filename,omitempty"`
	FallbackColor string `json:"fallbackColor,omitempty"`
	// Message is a loader's message (default "Loading..."). On the wire an
	// assistant-message's "message" is an object, [ViewNode.AssistantMessage].
	Message      *string              `json:"message,omitempty"`
	SpinnerColor string               `json:"spinnerColor,omitempty"`
	MessageColor string               `json:"messageColor,omitempty"`
	Indicator    *ViewLoaderIndicator `json:"indicator,omitempty"`
	// Frame is a loader's current frame index (upstream currentFrame);
	// the host's animation continues from it.
	Frame *int `json:"frame,omitempty"`
	// Gap and Align are StackOptions of hstack and vstack.
	Gap   *int   `json:"gap,omitempty"`
	Align string `json:"align,omitempty"`
	// Content is a lines node's rows, drawn verbatim.
	//portlint:allow emptydrop the D107 view wire is PiG-owned with no Pi counterpart, and an absent and an empty row list mean the same: none
	Content []string `json:"content,omitempty"`
	// Image, Progress and List are frontend-only annotations of a lines
	// node: the rows depict that image, show a position in a range, or are
	// a list with one item per row.
	Image    *ViewImageRef `json:"image,omitempty"`
	Progress *ViewProgress `json:"progress,omitempty"`
	List     *ViewList     `json:"list,omitempty"`

	// OutputPad is a user-message's and an assistant-message's outputPad
	// (default 1).
	OutputPad *int `json:"outputPad,omitempty"`
	// AssistantMessage is an assistant-message's message, the wire's
	// "message" object; nil before one arrives.
	AssistantMessage    *ViewAssistantMessage `json:"-"`
	HideThinkingBlock   bool                  `json:"hideThinkingBlock,omitempty"`
	HiddenThinkingLabel *string               `json:"hiddenThinkingLabel,omitempty"`
	IsStreaming         bool                  `json:"isStreaming,omitempty"`

	// ToolName, ToolCallID, Args, ToolDefinition and Cwd are a
	// tool-execution's constructor arguments; Args is any JSON value
	// (default {}) and ToolDefinition a ViewToolDefinition* name (default
	// builtin).
	ToolName        string          `json:"toolName,omitempty"`
	ToolCallID      string          `json:"toolCallId,omitempty"`
	Args            json.RawMessage `json:"args,omitempty"`
	ToolDefinition  string          `json:"toolDefinition,omitempty"`
	Cwd             string          `json:"cwd,omitempty"`
	ShowImages      *bool           `json:"showImages,omitempty"`
	ImageWidthCells *int            `json:"imageWidthCells,omitempty"`
	// ExecutionStarted, ArgsComplete, Result and IsPartial (default true)
	// are a tool-execution's state; Expanded is a tool-execution's and a
	// bash-execution's.
	ExecutionStarted bool            `json:"executionStarted,omitempty"`
	ArgsComplete     bool            `json:"argsComplete,omitempty"`
	Expanded         bool            `json:"expanded,omitempty"`
	Result           *ViewToolResult `json:"result,omitempty"`
	IsPartial        *bool           `json:"isPartial,omitempty"`

	// Command, ExcludeFromContext, Output and Complete are a
	// bash-execution's: Output is the output appended so far and Complete
	// the setComplete arguments, nil while the command runs.
	Command            string            `json:"command,omitempty"`
	ExcludeFromContext bool              `json:"excludeFromContext,omitempty"`
	Output             string            `json:"output,omitempty"`
	Complete           *ViewBashComplete `json:"complete,omitempty"`

	// Diff and FilePath are a diff node's renderDiff arguments.
	Diff     string `json:"diff,omitempty"`
	FilePath string `json:"filePath,omitempty"`
}

// viewNodeWire is a ViewNode with its "message" as raw JSON: a loader's
// string or an assistant-message's object.
type viewNodeWire struct {
	viewNodeFields
	Message json.RawMessage `json:"message,omitempty"`
}

type viewNodeFields ViewNode

// UnmarshalJSON reads "message" as a loader's string or an
// assistant-message's object.
func (n *ViewNode) UnmarshalJSON(data []byte) error {
	var wire viewNodeWire
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	*n = ViewNode(wire.viewNodeFields)
	if len(wire.Message) == 0 || string(wire.Message) == "null" {
		return nil
	}
	if wire.Message[0] == '"' {
		var message string
		if err := json.Unmarshal(wire.Message, &message); err != nil {
			return err
		}
		n.Message = &message
		return nil
	}
	var message ViewAssistantMessage
	if err := json.Unmarshal(wire.Message, &message); err != nil {
		return err
	}
	n.AssistantMessage = &message
	return nil
}

// MarshalJSON writes "message" from Message, or from AssistantMessage after
// the other fields.
func (n ViewNode) MarshalJSON() ([]byte, error) {
	data, err := json.Marshal(viewNodeFields(n))
	if err != nil || n.AssistantMessage == nil {
		return data, err
	}
	message, err := json.Marshal(n.AssistantMessage)
	if err != nil {
		return nil, err
	}
	data = append(data[:len(data)-1], `,"message":`...)
	data = append(data, message...)
	return append(data, '}'), nil
}

// ViewAssistantMessage is the part of upstream AssistantMessage that
// AssistantMessageComponent draws. StopReason "" is "stop".
type ViewAssistantMessage struct {
	Content      []ViewContentBlock `json:"content"`
	StopReason   string             `json:"stopReason,omitempty"`
	ErrorMessage string             `json:"errorMessage,omitempty"`
}

// ViewContentBlock is a content block of an assistant message ("text" with
// Text, "thinking" with Thinking, or "toolCall") or of a tool result ("text"
// with Text, or "image" with Ref and MimeType).
type ViewContentBlock struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	Thinking string `json:"thinking,omitempty"`
	Ref      string `json:"ref,omitempty"`
	MimeType string `json:"mimeType,omitempty"`
}

// ViewToolResult is the result a tool-execution's updateResult receives.
type ViewToolResult struct {
	Content []ViewContentBlock `json:"content"`
	IsError bool               `json:"isError,omitempty"`
	Details json.RawMessage    `json:"details,omitempty"`
}

// ViewBashComplete is a bash-execution's setComplete arguments: the exit
// code (nil: unknown), whether it was cancelled, whether the output was
// truncated, and where the full output is.
type ViewBashComplete struct {
	ExitCode       *int   `json:"exitCode,omitempty"`
	Cancelled      bool   `json:"cancelled,omitempty"`
	Truncated      bool   `json:"truncated,omitempty"`
	FullOutputPath string `json:"fullOutputPath,omitempty"`
}

// ViewStackEntry mirrors upstream StackEntryOptions without visible. A nil
// Basis is "auto".
type ViewStackEntry struct {
	Basis   *int `json:"basis,omitempty"`
	Grow    *int `json:"grow,omitempty"`
	Shrink  *int `json:"shrink,omitempty"`
	MinSize *int `json:"minSize,omitempty"`
	MaxSize *int `json:"maxSize,omitempty"`
}

// ViewTextStyle mirrors upstream Markdown DefaultTextStyle with tokens for
// its color functions.
type ViewTextStyle struct {
	Color         string `json:"color,omitempty"`
	BgColor       string `json:"bgColor,omitempty"`
	Bold          bool   `json:"bold,omitempty"`
	Italic        bool   `json:"italic,omitempty"`
	Strikethrough bool   `json:"strikethrough,omitempty"`
	Underline     bool   `json:"underline,omitempty"`
}

// ViewItem is a SelectItem (value, label, description) or a SettingItem
// (id, label, description, currentValue, values, submenu).
type ViewItem struct {
	Value        string `json:"value,omitempty"`
	ID           string `json:"id,omitempty"`
	Label        string `json:"label,omitempty"`
	Description  string `json:"description,omitempty"`
	CurrentValue string `json:"currentValue,omitempty"`
	//portlint:allow emptydrop the D107 view wire is PiG-owned with no Pi counterpart, and an absent and an empty value list mean the same: none
	Values  []string  `json:"values,omitempty"`
	Submenu *ViewNode `json:"submenu,omitempty"`
}

// ViewSelectLayout mirrors SelectListLayoutOptions without truncatePrimary.
type ViewSelectLayout struct {
	MinPrimaryColumnWidth *int `json:"minPrimaryColumnWidth,omitempty"`
	MaxPrimaryColumnWidth *int `json:"maxPrimaryColumnWidth,omitempty"`
}

// ViewLoaderIndicator mirrors LoaderIndicatorOptions. A given indicator is
// rendered verbatim, as upstream renders it; nil Frames are the default
// spinner and an empty list hides it.
type ViewLoaderIndicator struct {
	Frames     *[]string `json:"frames,omitempty"`
	IntervalMs int       `json:"intervalMs,omitempty"`
}

// ViewImageData is image bytes: Ref is the lowercase hex SHA-256 of the
// decoded Data.
type ViewImageData struct {
	Ref      string `json:"ref"`
	MimeType string `json:"mimeType"`
	Data     string `json:"data"` // base64
}

// ViewImageRef names image bytes already sent on the connection.
type ViewImageRef struct {
	Ref string `json:"ref"`
}

// ViewProgress is a position in a range.
type ViewProgress struct {
	Value float64 `json:"value"`
	Max   float64 `json:"max"`
}

// ViewList says a lines node's rows are a list: Items[i] is row i, so there
// are as many items as rows. SelectedIndex is the highlighted item, or -1.
type ViewList struct {
	Items         []ViewListItem `json:"items"`
	SelectedIndex int            `json:"selectedIndex"`
}

// ViewListItem is one row of a [ViewList]: its primary text, an optional
// secondary text, and optional further cells of a table row (for example a
// track's artist, album and length).
type ViewListItem struct {
	Label  string `json:"label"`
	Detail string `json:"detail,omitempty"`
	//portlint:allow emptydrop the D107 view wire is PiG-owned with no Pi counterpart, and an absent and an empty column list mean the same: none
	Columns []string `json:"columns,omitempty"`
}

// ViewEventPayload is a NotifyUIViewEvent argument. Index is the item's
// index among the shown (filtered) items for select and selectionChange; ID
// and Value are the setting and its new value for change.
type ViewEventPayload struct {
	Key   string    `json:"key"`
	Node  string    `json:"node"`
	Type  string    `json:"type"`
	Index int       `json:"index"`
	Item  *ViewItem `json:"item,omitempty"`
	ID    string    `json:"id,omitempty"`
	Value string    `json:"value,omitempty"`
}

// ViewEvictedPayload is a NotifyUIViewEvicted argument.
type ViewEvictedPayload struct {
	Refs []string `json:"refs"`
}

type RemoteOverlayClosePayload struct {
	Key    string          `json:"key"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  string          `json:"error,omitempty"`
}

// ── Call (ext→host) ──────────────────────────────────────────────────────────

// CallPayload is an extension calling a host method.
type CallPayload struct {
	Method          string          `json:"method"` // e.g. "ui.notify", "ui.setStatus", "sendMessage"
	Args            json.RawMessage `json:"args,omitempty"`
	ParentRequestID string          `json:"parent_request_id,omitempty"`
}

// ── CallResult (host→ext) ────────────────────────────────────────────────────

// CallResultPayload is the host's reply to an extension call.
type CallResultPayload struct {
	Result json.RawMessage `json:"result,omitempty"`
	Error  *ErrorInfo      `json:"error,omitempty"`
}

// ── WidgetPush (ext→host) ────────────────────────────────────────────────────

// WidgetPushPayload carries widget lines from extension to host. With a width
// the lines are a frame a component rendered at that width; without one they
// are a string list the host lays out as Pi's setWidget(key, string[]) does.
type WidgetPushPayload struct {
	Key   string   `json:"key"`             // Widget slot key
	Lines []string `json:"lines"`           // Frame rows or string list entries (may contain ANSI)
	Width int      `json:"width,omitempty"` // Width the frame was rendered at (0 = a string list)
	// View is the widget's component structure (D107); with Lines absent
	// the host renders it at the widget's width.
	View json.RawMessage `json:"view,omitempty"`
}

// ── Shutdown (host→ext) ──────────────────────────────────────────────────────

// CancelPayload asks the extension SDK to cancel an in-flight request.
type CancelPayload struct {
	RequestID string `json:"request_id,omitempty"`
	Reason    string `json:"reason,omitempty"`
}

// ShutdownPayload signals the extension to exit gracefully.
type ShutdownPayload struct {
	Reason string `json:"reason,omitempty"` // "quit", "reload", "disable"
}

// PingPayload and PongPayload correlate one dispatcher liveness probe.
type PingPayload struct {
	Nonce string `json:"nonce"`
}

type PongPayload struct {
	Nonce string `json:"nonce"`
}

// RequestStatePayload reports activity for one host-owned request. A blocked
// state means the handler has entered an awaited operation; completed means its
// body returned. The Node runtime reports suspended when an extension command's, or a quit session_shutdown handler's, event-loop window closed with the request unresponded. Either admits the next unawaited UI prompt event. Started alone
// reports dispatch and does not establish handler-body invocation order.
type RequestStatePayload struct {
	RequestID string `json:"request_id"`
	State     string `json:"state"`            // started | progress | blocked | completed | suspended
	Reason    string `json:"reason,omitempty"` // user | host_call | external_io
}

// ── Tool Result (within Response) ────────────────────────────────────────────

// ToolResult retains the ordered text/image array returned by a subprocess tool. The SDK's text shorthand decodes as one text block, including an explicit empty string.
type ToolResult struct {
	// MemberOrder names the AgentToolResult members of the wire object in the order the tool wrote them, with upstream's names (isError for is_error). Pi hands the tool's own object on, so the events and the JSON stream write its members in that order (agent-loop.ts:778-786, 912-919).
	MemberOrder []string                      `json:"-"`
	Content     []ai.ToolResultMessageContent `json:"-"`
	Details     json.RawMessage               `json:"details,omitempty"`
	IsError     bool                          `json:"is_error,omitempty"`
	// StructuredContent is upstream AgentToolResult.structuredContent: the machine-readable result of a tool that declares an outputSchema.
	StructuredContent json.RawMessage `json:"structured_content,omitempty"`
	// Usage is upstream AgentToolResult.usage: the tool execution's own usage.
	Usage *ai.Usage `json:"usage,omitempty"`
	// Terminate mirrors upstream AgentToolResult.terminate: the agent stops
	// after the current tool batch when every result in it sets terminate.
	Terminate bool `json:"terminate,omitempty"`
	// Preview is an optional short summary shown when the tool result
	// is collapsed in the TUI. Extensions set this to provide a custom
	// collapsed view instead of the default tail-of-output preview.
	Preview string `json:"preview,omitempty"`
}

func (r *ToolResult) UnmarshalJSON(data []byte) error {
	var raw struct {
		Content           json.RawMessage `json:"content,omitempty"`
		Details           json.RawMessage `json:"details,omitempty"`
		StructuredContent json.RawMessage `json:"structured_content,omitempty"`
		IsError           bool            `json:"is_error,omitempty"`
		Usage             *ai.Usage       `json:"usage,omitempty"`
		Terminate         bool            `json:"terminate,omitempty"`
		Preview           string          `json:"preview,omitempty"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	r.Details = raw.Details
	r.StructuredContent = raw.StructuredContent
	r.IsError = raw.IsError
	r.Usage = raw.Usage
	r.Terminate = raw.Terminate
	r.Preview = raw.Preview
	r.MemberOrder = toolResultMemberOrder(data)
	r.Content = nil

	if len(raw.Content) == 0 {
		return nil
	}
	switch raw.Content[0] {
	case '"':
		var text string
		if err := json.Unmarshal(raw.Content, &text); err != nil {
			return err
		}
		r.Content = []ai.ToolResultMessageContent{ai.TextContent{Text: text}}
		return nil
	case '[':
		var blocks []json.RawMessage
		if err := json.Unmarshal(raw.Content, &blocks); err != nil {
			return fmt.Errorf("decode tool result content blocks: %w", err)
		}
		r.Content = make([]ai.ToolResultMessageContent, 0, len(blocks))
		for _, rawBlock := range blocks {
			block, err := ai.UnmarshalContentBlock(rawBlock)
			if err != nil {
				return fmt.Errorf("decode tool result content block: %w", err)
			}
			value, ok := block.(ai.ToolResultMessageContent)
			if !ok {
				return fmt.Errorf("tool result content block %T is not text or image", block)
			}
			r.Content = append(r.Content, value)
		}
		return nil
	}
	return fmt.Errorf("tool result content must be a string or a block array, got %s", raw.Content)
}

// toolResultWireMembers maps the wire object's keys to the AgentToolResult members they carry. preview and any other key are not members of the object Pi writes.
var toolResultWireMembers = map[string]string{
	"content": "content", "details": "details", "is_error": "isError",
	"structured_content": "structuredContent", "usage": "usage", "terminate": "terminate",
}

func toolResultMemberOrder(data []byte) []string {
	object, err := orderedjson.Parse(data)
	if err != nil {
		return nil
	}
	var names []string
	for _, key := range object.Keys() {
		if name, ok := toolResultWireMembers[key]; ok {
			names = append(names, name)
		}
	}
	return names
}

// RenderResult is the structured result from a renderer execution.
type RenderResult struct {
	Lines []string `json:"lines,omitempty"`
	// View is the rendered component's structure (D107); with Lines absent
	// the host renders it at the requested width.
	View json.RawMessage `json:"view,omitempty"`
}

// RequestRenderTool (host→ext) runs a tool's renderCall or renderResult for
// one tool card and renders the component it returns, as upstream
// ToolExecutionComponent.updateDisplay and render do. Args is a
// RenderToolPayload; the response is a RenderResult, and an error response
// means the renderer threw, so the card draws upstream's fallback.
const RequestRenderTool = "render_tool"

// RequestCommandArgumentCompletions (host→ext) runs a command's
// getArgumentCompletions for the editor's autocomplete. Tool names the
// command and Args is the argument prefix as a JSON string; the response is
// the items as a JSON array, or null for none.
const RequestCommandArgumentCompletions = "command_argument_completions"

// RequestResolveToolRenderers (host→ext) runs the extension's tool renderer
// resolvers, in registration order, for one tool. Args is a
// ResolveToolRenderersPayload; the result is a ResolvedToolRenderers.
const RequestResolveToolRenderers = "resolve_tool_renderers"

// NotifyToolRenderers (ext→host) reports the extension's resolver count after
// a registration that followed loading. Args is a ToolRenderersPayload.
const NotifyToolRenderers = "tool_renderers"

// ToolRenderersPayload is the NotifyToolRenderers argument.
type ToolRenderersPayload struct {
	Count int `json:"count"`
}

// ToolRenderersDecl describes renderers by what they draw: upstream
// ToolRenderers' renderShell and whether renderCall and renderResult exist.
type ToolRenderersDecl struct {
	RenderShell   string `json:"render_shell,omitempty"`
	RendersCall   bool   `json:"renders_call,omitempty"`
	RendersResult bool   `json:"renders_result,omitempty"`
}

// ResolveToolRenderersPayload is the RequestResolveToolRenderers argument.
// Next describes what next() returns: the renderers the remaining resolvers,
// then the registered tool, use, or null for none. The host evaluates it
// before asking; the extension's next() returns a marker for it.
type ResolveToolRenderersPayload struct {
	Tool string             `json:"tool"`
	Next *ToolRenderersDecl `json:"next"`
}

// ResolvedToolRenderers is the RequestResolveToolRenderers result. Use is
// "next" when the resolvers returned next()'s renderers, "none" when they
// returned none, and "own" for renderers of the extension, which the host
// draws with RequestRenderTool naming Renderers.
type ResolvedToolRenderers struct {
	Use string `json:"use"`
	ToolRenderersDecl
	Renderers string `json:"renderers,omitempty"`
}

// NotifyToolRenderInvalidate (ext→host) is a renderer's context.invalidate():
// the host runs the card's renderers again and repaints. Args is a
// ToolRenderCardPayload.
const NotifyToolRenderInvalidate = "tool_render_invalidate"

// NotifyToolRenderRelease (host→ext) reports that a tool card no longer
// exists, so the extension drops the card's renderer state and components.
// Args is a ToolRenderCardPayload.
const NotifyToolRenderRelease = "tool_render_release"

// ToolRenderCardPayload names one tool card.
type ToolRenderCardPayload struct {
	Card string `json:"card"`
}

// RenderToolPayload is the RequestRenderTool argument. Card identifies the
// tool card; upstream keeps one renderer state per card, shared by both
// renderers, and one last component per renderer. Rerender runs the renderer
// again; without it the extension renders the card's last component at Width,
// as a resize does upstream, and runs the renderer only when it has none.
type RenderToolPayload struct {
	Card  string `json:"card"`
	Phase string `json:"phase"` // "call" | "result"
	// Renderers names resolved renderers (ResolvedToolRenderers.Renderers) to
	// draw instead of the registered tool's.
	Renderers string                             `json:"renderers,omitempty"`
	Rerender  bool                               `json:"rerender"`
	Args      json.RawMessage                    `json:"args"`
	Result    *RenderToolResult                  `json:"result,omitempty"`
	Options   *extension.ToolRenderResultOptions `json:"options,omitempty"`
	Context   RenderToolContext                  `json:"context"`
	Width     int                                `json:"width"`
}

// RenderToolResult is the result renderResult receives: upstream's
// {content, details}.
type RenderToolResult struct {
	Content []RenderToolContent `json:"content"`
	Details json.RawMessage     `json:"details,omitempty"`
}

// RenderToolContent is one text or image block of a RenderToolResult.
type RenderToolContent struct {
	Type          string `json:"type"`
	Text          string `json:"text,omitempty"`
	TextSignature string `json:"textSignature,omitempty"`
	Data          string `json:"data,omitempty"`
	MimeType      string `json:"mimeType,omitempty"`
}

// MarshalJSON retains required empty text and image fields on the renderer wire.
func (block RenderToolContent) MarshalJSON() ([]byte, error) {
	switch block.Type {
	case "text":
		return json.Marshal(ai.TextContent{Text: block.Text, TextSignature: block.TextSignature})
	case "image":
		return json.Marshal(ai.ImageContent{Data: block.Data, MimeType: block.MimeType})
	default:
		return nil, fmt.Errorf("invalid render tool content type %q", block.Type)
	}
}

// RenderToolContext carries the serializable fields of upstream
// ToolRenderContext. The extension supplies args, state, lastComponent and
// invalidate from the card.
type RenderToolContext struct {
	ToolCallID       string `json:"toolCallId"`
	Cwd              string `json:"cwd"`
	ExecutionStarted bool   `json:"executionStarted"`
	ArgsComplete     bool   `json:"argsComplete"`
	IsPartial        bool   `json:"isPartial"`
	Expanded         bool   `json:"expanded"`
	ShowImages       bool   `json:"showImages"`
	IsError          bool   `json:"isError"`
	OutputPad        int    `json:"outputPad"`
	// DurationMs is the recorded execution time of a final result in milliseconds. Absent while the result is partial and for results stored without one. upstream: ToolRenderContext.durationMs
	DurationMs *int64 `json:"durationMs,omitempty"`
}

// pig additive (D19): a user_bash handler's `{ operations }` result carries functions that cannot cross the socket. The extension keeps the BashOperations object and the reply names it: `{"operations": {"handle": "<id>"}}`. The host wraps the handle in an extension.BashOperations whose Exec sends RequestUserBashExec to the owner connection, as Pi's runner holds the extension's own object (runner.ts isUserBashEventResult, types.ts UserBashEventResult).

// RequestUserBashExec (host→ext) runs `operations.exec(command, cwd, { onData, signal, timeout, env })` of the object RequestPayload.Tool names. The extension streams each onData chunk as a NotifyToolUpdate whose result is a UserBashExecData, in order and before the response, and answers with a UserBashExecResult. The request's cancellation is the exec's signal. An error response is the exec's rejection.
const RequestUserBashExec = "user_bash_exec"

// UserBashExecArgs is the RequestUserBashExec argument. Timeout is seconds; Env, when present even if empty, replaces the inherited environment.
type UserBashExecArgs struct {
	Command string             `json:"command"`
	Cwd     string             `json:"cwd"`
	Timeout *float64           `json:"timeout,omitempty"`
	Env     *map[string]string `json:"env,omitempty"`
}

// UserBashExecData is one onData chunk: the raw bytes, base64 encoded.
type UserBashExecData struct {
	Data string `json:"data"`
}

// UserBashExecResult is the RequestUserBashExec answer: Pi's `{ exitCode: number | null }`. A null exit code is a failed command.
type UserBashExecResult struct {
	ExitCode *int `json:"exitCode"`
}

// NotifyBashOperationsRelease (host→ext) reports that the host dropped the BashOperations object a user_bash reply named, so the extension drops its table entry. Args is a BashOperationsRelease.
const NotifyBashOperationsRelease = "bash_operations_release"

// BashOperationsRelease is the NotifyBashOperationsRelease argument.
type BashOperationsRelease struct {
	Handle string `json:"handle"`
}
