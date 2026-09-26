package subprocess

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

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

// ToolDecl declares a tool the extension provides.
type ToolDecl struct {
	Name                string            `json:"name"`
	Label               string            `json:"label,omitempty"`
	Description         string            `json:"description"`
	Parameters          json.RawMessage   `json:"parameters"`                     // JSON Schema
	ConstrainedSampling json.RawMessage   `json:"constrained_sampling,omitempty"` // false | ConstrainedSamplingConfig (ai.ConstrainedSamplingConfig JSON); false/null/absent all disable
	ExecutionMode       string            `json:"execution_mode,omitempty"`       // "sequential" | "parallel"
	PromptGuidelines    []string          `json:"prompt_guidelines,omitempty"`
	Annotations         map[string]string `json:"annotations,omitempty"`
	Source              string            `json:"source,omitempty"` // pig additive (D23): per-tool source override; default: extension name
}

// CommandDecl declares a slash command the extension provides.
type CommandDecl struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Args        string `json:"args,omitempty"` // Argument hint shown in /help
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
	Name   string          `json:"name"`
	Config json.RawMessage `json:"config"`
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

// OAuthCredentialsWire is the wire shape of OAuth credentials. Matches
// ai.OAuthCredentials so the host can marshal that type directly; the SDK
// mirrors these tags.
type OAuthCredentialsWire struct {
	Refresh   string `json:"refresh"`
	Access    string `json:"access"`
	Expires   int64  `json:"expires"`
	ProjectID string `json:"projectId,omitempty"`
}

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
type StatePayload struct {
	ActiveTools         []string                   `json:"activeTools,omitempty"`
	AllTools            []ToolInfo                 `json:"allTools,omitempty"`
	Commands            []CommandInfo              `json:"commands,omitempty"`
	ThinkingLevel       string                     `json:"thinkingLevel,omitempty"`
	Model               map[string]any             `json:"model,omitempty"`
	Session             *SessionStatePayload       `json:"session,omitempty"`
	IsIdle              bool                       `json:"isIdle"`
	ProjectTrusted      bool                       `json:"projectTrusted"`
	HasPendingMessages  bool                       `json:"hasPendingMessages"`
	ContextUsage        *extensionContextUsageDTO  `json:"contextUsage,omitempty"`
	SystemPrompt        string                     `json:"systemPrompt,omitempty"`
	SystemPromptOptions json.RawMessage            `json:"systemPromptOptions,omitempty"`
	Flags               map[string]json.RawMessage `json:"flags,omitempty"`
	HasUI               bool                       `json:"hasUI"`
	FooterData          *FooterDataPayload         `json:"footerData,omitempty"`
	// Always serialized: an emptied editor must clear the replicated value
	// rather than leave the previous text in place.
	EditorText    string         `json:"editorText"`
	ToolsExpanded bool           `json:"toolsExpanded"`
	AllThemes     []themeMetaDTO `json:"allThemes,omitempty"`
	// TerminalCapabilities is the host terminal's resolved capabilities
	// (detection plus settings overrides). The Node runtime seeds pi-tui's
	// capability cache with it, so Pi's Markdown renders links as the host
	// terminal supports them.
	TerminalCapabilities *TerminalCapabilitiesPayload `json:"terminalCapabilities,omitempty"`
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
	SessionName string `json:"sessionName,omitempty"`
	SessionFile string `json:"sessionFile,omitempty"`
	LeafID      string `json:"leafId,omitempty"`
	// EntriesAppended carries only the next bounded page after this extension's
	// cursor. EntryCount is the cursor after applying the page. When
	// EntriesRemaining is true, the host sends another ordered state update.
	EntriesAppended  []json.RawMessage `json:"entriesAppended,omitempty"`
	EntryCount       int               `json:"entryCount"`
	EntriesRemaining bool              `json:"entriesRemaining,omitempty"`
}

// extensionContextUsageDTO mirrors extension.ContextUsage on the wire. We
// duplicate it here to avoid importing the public extension package from
// the protocol layer.
type extensionContextUsageDTO struct {
	Tokens        int     `json:"tokens"`
	ContextWindow int     `json:"contextWindow"`
	Percent       float64 `json:"percent"`
}

// ── Request (host→ext) ───────────────────────────────────────────────────────

// RequestPayload carries a host request that expects a response.
type RequestPayload struct {
	Method     string          `json:"method"` // "tool_call", "event", "command", "shortcut", "render_message", "render_entry"
	Tool       string          `json:"tool,omitempty"`
	Event      string          `json:"event,omitempty"`
	HandlerID  int             `json:"handler_id,omitempty"`
	ToolCallID string          `json:"tool_call_id,omitempty"` // For tool_call: unique ID
	Args       json.RawMessage `json:"args,omitempty"`         // Tool args or event payload
}

// ── Response (ext→host) ──────────────────────────────────────────────────────

// BoundaryEventResultPayload is the current-wire result for turn_end and
// agent_before_settle. Entries is the complete replacement proposal; pointers
// preserve an omitted field from an explicit empty list or false continuation.
type BoundaryEventResultPayload struct {
	Entries  *json.RawMessage `json:"entries,omitempty"`
	Continue *bool            `json:"continue,omitempty"`
}

// ResponsePayload is the extension's reply to a request. For agent_before_settle,
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
}

func (e *ErrorInfo) ToError() error {
	if e == nil {
		return nil
	}
	if e.Code != "" {
		return fmt.Errorf("[%s] %s", e.Code, e.Message)
	}
	return fmt.Errorf("%s", e.Message)
}

// ── Notify (bidirectional) ───────────────────────────────────────────────────

// NotifyPayload carries a fire-and-forget notification.
type NotifyPayload struct {
	Method string          `json:"method"` // e.g. "width_change", "widget_invalidate"
	Args   json.RawMessage `json:"args,omitempty"`
}

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
)

type RemoteOverlayOpenPayload struct {
	Key            string  `json:"key"`
	Title          string  `json:"title,omitempty"`
	WidthFraction  float64 `json:"widthFraction,omitempty"`
	HeightFraction float64 `json:"heightFraction,omitempty"`
	Overlay        bool    `json:"overlay,omitempty"`
	// OverlayOptions is upstream ui.custom()'s serialisable overlayOptions.
	OverlayOptions *extension.OverlayLayout `json:"overlayOptions,omitempty"`
}

type RemoteOverlayInputPayload struct {
	Key  string `json:"key"`
	Data string `json:"data"`
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

// WidgetPushPayload carries rendered widget lines from extension to host.
type WidgetPushPayload struct {
	Key   string   `json:"key"`             // Widget slot key (matches WidgetDecl.Key)
	Lines []string `json:"lines"`           // Pre-rendered lines (may contain ANSI)
	Width int      `json:"width,omitempty"` // Width the lines were rendered at (0 = unknown)
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
// body returned. Either admits the next unawaited UI prompt event. Started alone
// reports dispatch and does not establish handler-body invocation order.
type RequestStatePayload struct {
	RequestID string `json:"request_id"`
	State     string `json:"state"`            // started | progress | blocked | completed
	Reason    string `json:"reason,omitempty"` // user | host_call | external_io
}

// ── Tool Result (within Response) ────────────────────────────────────────────

// ToolResult is the structured result from a tool execution, carried inside
// ResponsePayload.Result. Content is a string or upstream's block array of
// {type:"text",text} and {type:"image",data,mimeType}. Text blocks join with
// newlines, unchanged; image blocks become Images in order.
type ToolResult struct {
	Content string            `json:"-"`
	Images  []ai.ImageContent `json:"-"`
	Details json.RawMessage   `json:"details,omitempty"`
	IsError bool              `json:"is_error,omitempty"`
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
		Terminate bool            `json:"terminate,omitempty"`
		Preview   string          `json:"preview,omitempty"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	r.Details = raw.Details
	r.IsError = raw.IsError
	r.Terminate = raw.Terminate
	r.Preview = raw.Preview

	if len(raw.Content) == 0 {
		return nil
	}
	switch raw.Content[0] {
	case '"':
		return json.Unmarshal(raw.Content, &r.Content)
	case '[':
		var blocks []struct {
			Type     string `json:"type"`
			Text     string `json:"text"`
			Data     string `json:"data"`
			MimeType string `json:"mimeType"`
		}
		if err := json.Unmarshal(raw.Content, &blocks); err != nil {
			return fmt.Errorf("decode tool result content blocks: %w", err)
		}
		var sb strings.Builder
		wroteText := false
		for _, b := range blocks {
			switch b.Type {
			case "text":
				if wroteText {
					sb.WriteString("\n")
				}
				sb.WriteString(b.Text)
				wroteText = true
			case "image":
				r.Images = append(r.Images, ai.ImageContent{Data: b.Data, MimeType: b.MimeType})
			default:
				return fmt.Errorf("tool result content block type %q is not text or image", b.Type)
			}
		}
		r.Content = sb.String()
		return nil
	}
	return fmt.Errorf("tool result content must be a string or a block array, got %s", raw.Content)
}

// RenderResult is the structured result from a renderer execution.
type RenderResult struct {
	Lines []string `json:"lines,omitempty"`
}
