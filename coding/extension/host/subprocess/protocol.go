package subprocess

import (
	"bytes"
	"fmt"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
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
	Widgets          []WidgetDecl          `json:"widgets,omitempty"`
	Flags            []FlagDecl            `json:"flags,omitempty"`
	Providers        []ProviderDecl        `json:"providers,omitempty"`
	MessageRenderers []MessageRendererDecl `json:"message_renderers,omitempty"`
	EntryRenderers   []EntryRendererDecl   `json:"entry_renderers,omitempty"`
	// MarkdownTransformer reports that the extension registered a Markdown
	// transformer. The host runs it with RequestMarkdownTransform.
	MarkdownTransformer bool `json:"markdown_transformer,omitempty"`

	// WantsSessionLog subscribes this extension to session-log replication from
	// the handshake, before the ready state is built, so the log is present on
	// the first read. Runtimes whose session readers can block on the host
	// subscribe on first use instead and leave this false; a runtime that
	// exposes those readers synchronously over asynchronous IPC has no such
	// moment and declares it here.
	WantsSessionLog bool `json:"wants_session_log,omitempty"`

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
	Name                string            `json:"name"`
	Label               string            `json:"label,omitempty"`
	Description         string            `json:"description"`
	Parameters          json.RawMessage   `json:"parameters"`                     // JSON Schema
	ConstrainedSampling json.RawMessage   `json:"constrained_sampling,omitempty"` // false | ConstrainedSamplingConfig (ai.ConstrainedSamplingConfig JSON); false/null/absent all disable
	ExecutionMode       string            `json:"execution_mode,omitempty"`       // "sequential" | "parallel"
	PromptSnippet       string            `json:"prompt_snippet,omitempty"`
	PromptGuidelines    []string          `json:"prompt_guidelines,omitempty"`
	Annotations         map[string]string `json:"annotations,omitempty"`
	Source              string            `json:"source,omitempty"` // pig additive (D23): per-tool source override; default: extension name
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
	Args        string `json:"args,omitempty"` // Argument hint shown in /help
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

// WidgetDecl declares a widget the extension will push updates for.
type WidgetDecl struct {
	Key string `json:"key"` // Widget slot key
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
const CallProviderPublish = "provider.publish"
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
	CallOAuthOnManualCodeInput = "oauth.cb.onManualCodeInput" // Args: none.                    Result: OAuthInputResult.
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
	Models      []map[string]any `json:"models,omitempty"`       // Complete registry snapshot for synchronous Node lookup
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
	FooterData          *FooterDataPayload         `json:"footerData,omitempty"`
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
	// SignalTimeoutMS, on oauth_refresh, asks a runtime whose refresh callback receives an AbortSignal to compose AbortSignal.timeout(SignalTimeoutMS) into it, as Pi's resolveStoredOAuth does (auth/resolve.ts:149-153). Absent, the signal follows only the caller (models.ts:474).
	SignalTimeoutMS *float64 `json:"signal_timeout_ms,omitempty"`
}

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

// BeforeAgentStartResponsePayload carries per-run mutations even when a handler fails. SelectedTools retains the untyped value until the handler chain ends: non-string entries cannot name registered tools, and null rejects prompt admission. Result carries the handler's ordinary message/systemPrompt result.
type BeforeAgentStartResponsePayload struct {
	Sections      ai.OrderedSections `json:"_pigPromptSections"`
	SelectedTools json.RawMessage    `json:"_pigPromptSelectedTools"`
	Result        json.RawMessage    `json:"_pigPromptResult"`
}

// ResponsePayload is the extension's reply to a request. For agent_before_settle and turn_end,
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

// NotifyPayload carries a fire-and-forget notification. The host-only runtime_input_end notification removes stdin as a Node keepalive. runtime_drained reports a still-pending quit handler whose event loop drained. runtime_quit_yield reports a post-disposal boundary that suspended beyond its immediately fulfilled continuations. Command suspension travels as request_state. None completes an extension request.
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
	CallUISetLogin       = "ui.setLogin"
	NotifyUICustomInput  = "ui.custom.input"
	NotifyUICustomRender = "ui.custom.render"
	NotifyUICustomClose  = "ui.custom.close"
	NotifyUICustomOpened = "ui.custom.opened"
	CallUICustomControl  = "ui.custom.control"
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

type RemoteOverlayRenderPayload struct {
	Key   string   `json:"key"`
	Lines []string `json:"lines"`
	// Width is the terminal width the frame was laid out for (stale-frame
	// key), not the overlay render width.
	Width int    `json:"width,omitempty"`
	Seq   uint64 `json:"seq,omitempty"`
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
	Key   string   `json:"key"`             // Widget slot key (matches WidgetDecl.Key)
	Lines []string `json:"lines"`           // Frame rows or string list entries (may contain ANSI)
	Width int      `json:"width,omitempty"` // Width the frame was rendered at (0 = a string list)
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
	Content []ai.ToolResultMessageContent `json:"-"`
	Details json.RawMessage               `json:"details,omitempty"`
	IsError bool                          `json:"is_error,omitempty"`
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
		Content   json.RawMessage `json:"content,omitempty"`
		Details   json.RawMessage `json:"details,omitempty"`
		IsError   bool            `json:"is_error,omitempty"`
		Usage     *ai.Usage       `json:"usage,omitempty"`
		Terminate bool            `json:"terminate,omitempty"`
		Preview   string          `json:"preview,omitempty"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	r.Details = raw.Details
	r.IsError = raw.IsError
	r.Usage = raw.Usage
	r.Terminate = raw.Terminate
	r.Preview = raw.Preview
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

// RenderResult is the structured result from a renderer execution.
type RenderResult struct {
	Lines []string `json:"lines,omitempty"`
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
	Card     string                             `json:"card"`
	Phase    string                             `json:"phase"` // "call" | "result"
	Rerender bool                               `json:"rerender"`
	Args     json.RawMessage                    `json:"args"`
	Result   *RenderToolResult                  `json:"result,omitempty"`
	Options  *extension.ToolRenderResultOptions `json:"options,omitempty"`
	Context  RenderToolContext                  `json:"context"`
	Width    int                                `json:"width"`
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
}
