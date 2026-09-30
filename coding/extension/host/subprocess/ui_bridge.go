package subprocess

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"weak"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// UIBridge translates extension→host call messages into real UI/action method
// invocations. It holds a reference to the host's [extension.UIContext] and
// [HostCallbacks] and manages PushProxy instances for widget components.
//
// The bridge replaces the need for a narrow adapter interface: it directly
// calls the UIContext methods, which handle the "TUI not ready" case via the
// noop implementation (NoopUIContext returns sane zero values for every
// method).
//
// pig-specific: no upstream equivalent.
type UIBridge struct {
	modelCatalogEncoder func() (json.RawMessage, error)
	mu                  sync.RWMutex
	headerMu            sync.Mutex
	footerMu            sync.Mutex            // footer admission, pending state and application are one step, as for headerMu; lock headerMu first
	widgets             map[string]*PushProxy // key → proxy

	// customOverlays tracks the active ui.custom overlays keyed by
	// "<extName>:<key>" so render/close notifications from the TS
	// shim runtime can be routed back to the originating overlay.
	customOverlays   map[string]extension.RemoteOverlayHandle
	interactiveFocus chan struct{}

	// extConns holds the per-extension socket connection so the
	// bridge can forward overlay input notifications back to the
	// extension subprocess.
	extConns               map[string]*Conn
	autocompleteReferences map[string]autocompleteReference
	autocompleteOrigins    map[weak.Pointer[extension.AutocompleteProvider]]autocompleteOrigin
	autocompleteQueries    map[autocompleteQueryKey]*autocompleteQuery

	// editor is the extension editor component installed last
	// (ctx.ui.setEditorComponent), and editorsByConn each connection's
	// latest one, keyed by customOverlayOwnerPrefix.
	editor        *editorProxy
	editorsByConn map[string]*editorProxy

	// modelStreams cancels an in-flight modelStream call, keyed by the
	// owning connection and its stream ID, so an extension's AbortSignal
	// (upstream options.signal) ends the provider request.
	modelStreamMu sync.Mutex
	modelStreams  map[modelStreamKey]context.CancelFunc

	// uiCtx is the per-mode UI surface. Initially [extension.NoopUIContext],
	// upgraded to the real TUI after startup via [SetUIContext].
	// terminalInputSubs holds the retire func for each extension's raw
	// terminal-input subscription, keyed by extension name.
	terminalInputSubs map[string]func()
	// terminalCapabilities reports the host terminal's resolved capabilities.
	terminalCapabilities func() TerminalCapabilitiesPayload
	// theme reports the host's active theme palette for the state snapshot.
	theme func() any
	// keybindings reports the host's resolved keybinding table.
	keybindings func() any

	uiCtx            extension.UIContext
	uiReady          bool
	promptScope      UIPromptScope
	pendingStatuses  map[string]string
	pendingFooterSet bool
	pendingFooter    []string
	pendingFooterW   int
	pendingHeaderSet bool
	pendingHeader    []string
	pendingHeaderW   int
	pendingLogin     *extension.LoginDefinition

	// actions holds agent-loop callbacks (sendMessage, setModel, etc.).
	// Set via [SetActions] after the agent session is created.
	actions *HostCallbacks
	// resolveFlag enforces registration scope and supplies host-owned defaults after CLI overrides.
	resolveFlag func(extName, name string, override any) any

	// registeredProviderConfigs holds the provider configs extensions
	// registered, merged per upstream registerProvider, and
	// registeredProviderOrder their registration order. Upstream keeps them in
	// ModelRuntime.extensionProviders, shared by every extension.
	registeredProviderConfigs map[string]map[string]json.RawMessage
	registeredProviderOrder   []string
	registeredNativeProviders map[string]NativeProviderDeclaration

	// WatchSessionLog enrolls one extension in session-log replication and
	// returns the entries it has not seen. Set by the Host, which owns the
	// per-extension cursor and subscription flag.
	WatchSessionLog func(extName string, cursor int, complete bool) ([]json.RawMessage, int, bool, string)

	// OnStateChanged is called after a host-side change to the synchronous
	// getter surface that a subprocess extension cannot observe on its own,
	// such as an extension status set through ui.setStatus. Set by the Host
	// to its own BroadcastStateUpdate, which pushes a fresh state_update to
	// every connected extension. Mirrors upstream's setExtensionStatus,
	// which calls this.ui.requestRender() synchronously in the single
	// in-process runtime (interactive-mode.ts:2203); pig's footer factory
	// runs in a separate subprocess and only re-renders on a pushed
	// state_update, so the host must proactively push one.
	OnStateChanged func()

	// invalidateTUI triggers a TUI render cycle.
	invalidateTUI func()
	currentWidth  int

	// notifyFunc renders notifications via the TUI chat area.
	// Set by InteractiveMode after TUI creation.
	notifyFunc func(message, level string)

	// widgetSyncFunc is called whenever an extension sets or clears a widget.
	// The callback receives all current widget proxies so the host can sync
	// them into the TUI layout.
	widgetSyncFunc    func(widgets map[string]*PushProxy)
	widgetRequestFunc func(extName, key string, lines []string, opts extension.ExtensionWidgetOptions)
}

// sessionLogPageBytes sizes one session-log chunk on the wire. Every reader
// pages until the log is complete, so it never limits what an extension sees.
// pig additive (D19): the subprocess wire chunks the log that upstream's
// in-process session manager exposes directly.
const sessionLogPageBytes = 4 * 1024 * 1024

// HostCallbacks provides the agent-loop callbacks that extensions can invoke.
// These map to upstream ExtensionActions (types.ts:1447-1462).
//
// All fields are optional (nil = no-op). They are set after the agent session
// is created, which happens after extensions load. Promise-shaped callbacks
// receive the call context and invoke [extension.CallInitiated] after their
// synchronous prefix has applied; they may then block until completion.
type HostCallbacks struct {
	// SendMessage injects a custom message into the conversation.
	// upstream: types.ts:1448: SendMessageHandler
	SendMessage func(msg extension.CustomMessageRef, opts SendMessageOptions) error

	// SendUserMessage injects a user message into the conversation.
	// upstream: types.ts:1449: SendUserMessageHandler
	SendUserMessage func(content any, opts SendUserMessageOptions) error

	// AppendEntry appends a custom session entry.
	// upstream: types.ts:1450: AppendEntryHandler
	// direct is non-nil for ctx.sessionManager.appendCustomEntry, which
	// writes the log only and has already returned the entry's id; nil is
	// pi.appendEntry, which also emits entry_appended.
	AppendEntry func(customType string, data any, direct *DirectEntryAppend) error

	// Exec runs a local command and returns stdout/stderr/exit status.
	// ExecContext is preferred by production hosts: the call context carries
	// the ordered-initiation mark that ExecCommand reports after process start.
	// Exec remains for direct adapters that cannot report an earlier initiation;
	// those adapters release the call lane when they return.
	// upstream: types.ts:1190
	Exec        func(command string, args []string, opts *extension.ExecOptions) (extension.ExecResult, error)
	ExecContext func(context.Context, string, []string, *extension.ExecOptions) (extension.ExecResult, error)

	// GetSessionName returns the current session name.
	// upstream: types.ts:1452
	GetSessionName func() string

	// GetSessionID returns the current session id.
	// upstream: session-manager.ts session.id getter
	GetSessionID func() string

	// GetSessionFile returns the current session file path.
	// upstream: session-manager.ts getSessionFile()
	GetSessionFile func() string

	// GetLeafID returns the current branch leaf entry id.
	// upstream: session-manager.ts getLeafId()
	GetLeafID func() string

	// GetFlag returns the value of a registered CLI flag for an extension.
	// upstream: types.ts:1145
	GetFlag func(extName, name string) any

	// SetSessionName sets the session name.
	// upstream: types.ts:1451
	SetSessionName func(name string) error

	// SetLabel sets a label on a session entry; an empty label clears it.
	// Its error fails the extension's call, as upstream appendLabelChange
	// throws.
	// upstream: types.ts:1453
	SetLabel func(entryID, label string) error

	// GetActiveTools returns the currently active tool names.
	// upstream: types.ts:1454
	GetActiveTools func() []string

	// GetAllTools returns metadata about all available tools.
	// upstream: types.ts:1455
	GetAllTools func() []ToolInfo

	// SetActiveTools sets the active tool list.
	// upstream: types.ts:1456
	SetActiveTools func(names []string)

	// RefreshTools reloads tool definitions.
	// upstream: types.ts:1457
	RefreshTools func() error

	// GetCommands returns available slash commands.
	// upstream: types.ts:1458
	GetCommands func() []CommandInfo

	// SetModel changes the active model.
	// upstream: types.ts:1459
	SetModel func(context.Context, string) (bool, error)

	// GetThinkingLevel returns the current thinking level.
	// upstream: types.ts:1460
	GetThinkingLevel func() string

	// SetThinkingLevel sets the thinking level.
	// upstream: types.ts:1461
	SetThinkingLevel func(level string)

	// GetContextUsage returns context window utilization data.
	// upstream: runner.ts:614-617
	GetContextUsage func() *extension.ContextUsage

	// GetSystemPrompt returns the current system prompt text.
	// upstream: runner.ts:620 (getSystemPrompt getter on ExtensionContext)
	GetSystemPrompt func() string

	// GetSystemPromptOptions returns the base inputs pi currently uses to
	// build the system prompt. Backs ctx.getSystemPromptOptions().
	// upstream: runner.ts:653 (createCommandContext getSystemPromptOptions)
	GetSystemPromptOptions func() extension.BuildSystemPromptOptions

	// GetModelInfo returns structured metadata for the active model.
	// upstream: runner.ts:609 (model getter on ExtensionContext)
	GetModelInfo func() map[string]any

	// GetScopedModels returns the Session's resolved model scope in selection order.
	GetScopedModels func() []extension.ScopedModel

	// GetModel resolves metadata by provider and model ID through the Session-owned runtime registry.
	GetModel  func(providerID, modelID string) map[string]any
	GetModels func() []map[string]any

	// GetBranch returns the conversation history as raw session entries. The
	// entries are already JSON on disk, so they are passed through rather than
	// decoded into maps and re-encoded on every push.
	// upstream: sessionManager.getBranch(): returns SessionEntry[]
	GetBranch func() []json.RawMessage

	// GetEntries returns all session entries in append order.
	// upstream: sessionManager.getEntries()
	GetEntries func() []json.RawMessage

	// GetEntriesPage returns a bounded page at or after cursor, the cursor after
	// that page, whether more entries remain, and the current branch leaf.
	GetEntriesPage func(cursor, maxBytes int) ([]json.RawMessage, int, bool, string)

	// GetModelAuth resolves request auth for a model lookup.
	// upstream: modelRegistry.getApiKeyAndHeaders(model)
	GetModelAuth func(context.Context, string, string) map[string]any

	// Complete performs a one-shot LLM completion.
	// upstream: @mariozechner/pi-ai complete()
	Complete func(context.Context, map[string]any, map[string]any, map[string]any) (map[string]any, error)

	// StreamModel starts a model operation through the Session-owned runtime.
	StreamModel func(context.Context, map[string]any, map[string]any) (*ai.AssistantMessageEventStream, error)

	// GetModelRegistryState returns the state behind upstream ModelRegistry's
	// synchronous reads: the catalog, the available models, each provider's
	// display name, auth status and OAuth use, and the registry error.
	// upstream: model-registry.ts getAll, getAvailable, hasConfiguredAuth,
	// getProviderAuthStatus, getProvider, getProviderDisplayName,
	// isUsingOAuth, getError.
	GetModelRegistryState func() map[string]any

	// GetProviderAuth resolves request auth for a provider, or nil when it has
	// none. upstream: model-registry.ts getProviderAuth(provider)
	GetProviderAuth func(ctx context.Context, provider string) (map[string]any, error)

	// RefreshModelRegistry reloads models.json and refreshes the provider
	// catalogs. allowNetwork nil means the runtime default; providers nil
	// means every provider. upstream: model-registry.ts refresh(options)
	RefreshModelRegistry func(ctx context.Context, allowNetwork *bool, providers []string, force *bool) (map[string]any, error)

	// SessionRead answers a ReadonlySessionManager read computed from the
	// host's session: the header facts (method "info"), and for the Go, Rust
	// and Python SDKs the reads their session mirror cannot answer.
	// upstream: session-manager.ts ReadonlySessionManager
	SessionRead func(method string, args json.RawMessage) (any, error)

	// Agent control.

	// IsIdle returns whether the agent is currently idle (not streaming).
	// upstream: types.ts:307
	IsIdle func() bool

	// IsProjectTrusted returns whether the current project is trusted.
	// upstream: types.ts:332
	IsProjectTrusted func() bool

	// Footer data behind upstream's ReadonlyFooterDataProvider.
	GetGitBranch              func() string
	GetExtensionStatuses      func() map[string]string
	GetAvailableProviderCount func() int

	// Abort cancels the current agent operation.
	// upstream: types.ts:311
	Abort func()

	// HasPendingMessages returns whether there are queued messages waiting.
	// upstream: types.ts:313
	HasPendingMessages func() bool

	// Shutdown triggers a graceful shutdown and exit.
	// upstream: types.ts:315
	Shutdown func()

	// Compact triggers compaction without awaiting completion.
	// upstream: types.ts:319
	Compact func(context.Context, *extension.CompactOptions)

	// Command-only session control.

	// WaitForIdle blocks until the agent finishes streaming.
	// upstream: types.ts:331 (ExtensionCommandContext)
	WaitForIdle func(context.Context) error

	// NewSession starts a new session.
	// upstream: types.ts:333
	NewSession func(context.Context, *extension.NewSessionOptions) (extension.CancelledResult, error)

	// Fork creates a new branch from an entry.
	// upstream: types.ts:340
	Fork func(context.Context, string, *extension.ForkOptions) (extension.CancelledResult, error)

	// NavigateTree moves to a different point in the session tree.
	// upstream: types.ts:346
	NavigateTree func(context.Context, string, *extension.NavigateTreeOptions) (extension.CancelledResult, error)

	// SwitchSession switches to a different session file.
	// upstream: types.ts:352
	SwitchSession func(context.Context, string, *extension.SwitchSessionOptions) (extension.CancelledResult, error)

	// Reload reloads extensions, skills, prompts, themes.
	// upstream: types.ts:358
	Reload func(context.Context) error
}

// SendMessageOptions mirrors upstream options for sendMessage.
// Both fields are optional upstream and their defaults depend on session
// state, so an omitted field stays unset: TriggerTurn is nil and DeliverAs is
// empty.
type SendMessageOptions struct {
	TriggerTurn *bool  `json:"triggerTurn,omitempty"`
	DeliverAs   string `json:"deliverAs,omitempty"` // "steer" | "followUp" | "nextTurn"
}

// SendUserMessageOptions mirrors upstream options for sendUserMessage.
type SendUserMessageOptions struct {
	DeliverAs string `json:"deliverAs,omitempty"` // "steer" | "followUp"
}

// ToolInfo is one entry of getAllTools on the wire. Mirrors upstream ToolInfo
// (types.ts): the tool definition's name, description, parameter schema and
// prompt guidelines, with the sourceInfo of whatever registered it.
type ToolInfo struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
	// PromptGuidelines is omitted when the definition has none, as upstream
	// copies an undefined definition.promptGuidelines.
	PromptGuidelines []string `json:"promptGuidelines,omitempty"`
	// SourceInfo is upstream's SourceInfo object: path, source, scope, origin
	// and an optional baseDir.
	SourceInfo extension.SourceInfo `json:"sourceInfo"`
	// Source is PiG's per-tool source attribution (D23): "builtin", the
	// registering extension's name, or the tool's declared source. It is
	// not part of upstream's ToolInfo, so the replicated state Node
	// extensions read omits it; the getAllTools host call keeps sending it
	// as "source" for SDKs released before sourceInfo existed.
	Source string `json:"-"`
}

// CommandInfo is one entry of getCommands on the wire. Mirrors upstream
// SlashCommandInfo (slash-commands.ts): an extension command, prompt template
// or skill, with its source kind and sourceInfo.
type CommandInfo struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	// Source is "extension", "prompt" or "skill".
	Source     string               `json:"source"`
	SourceInfo extension.SourceInfo `json:"sourceInfo"`
}

// Snapshot returns the current host-side state visible to the extension via
// upstream-faithful synchronous getters. Missing usage stays absent; unknown token and percentage counts stay null.
//
// The result is suitable for the protocol's [StatePayload]: callers send it
// at handshake time (embedded in [ReadyPayload]) and on host-side state
// changes via "state_update" notifies.
// Snapshot builds the state pushed to one extension. cursor is how many
// session entries that extension already holds. wantSessionLog reports whether
// it has ever read the log; when false no entries are sent, because an
// extension that never inspects the session would otherwise hold a full copy
// of it in resident memory for nothing.
func (b *UIBridge) Snapshot(flagNames []string, cursor int, wantSessionLog bool) *StatePayload {
	b.mu.RLock()
	actions := b.actions
	uiCtx := b.uiCtx
	terminalCapabilities := b.terminalCapabilities
	theme := b.theme
	keybindings := b.keybindings
	b.mu.RUnlock()

	// Upstream runner.ts:578-580 reports UI availability from the bound context.
	state := &StatePayload{IsIdle: true, HasUI: uiCtx != nil && uiCtx != extension.NoopUIContext, ProjectTrusted: true, ScopedModels: []scopedModelSnapshot{}}
	// The Node runtime answers getEditorText, getToolsExpanded, and
	// getAllThemes synchronously, matching the in-process API, so it cannot
	// make a host call for them. The host pushes state immediately before
	// dispatching a tool, command, or event handler, so these are current when
	// an extension reads them. They come from the UI context rather than the
	// host actions, so they are filled before the unbound-actions return.
	if terminalCapabilities != nil {
		caps := terminalCapabilities()
		state.TerminalCapabilities = &caps
	}
	if theme != nil {
		state.Theme = theme()
	}
	if keybindings != nil {
		state.Keybindings = keybindings()
	}
	if ui := uiCtx; ui != nil {
		state.EditorText = ui.GetEditorText()
		state.ToolsExpanded = ui.GetToolsExpanded()
		for _, m := range ui.GetAllThemes() {
			state.AllThemes = append(state.AllThemes, themeMetaDTO{Name: m.Name, Path: m.Path})
		}
	}
	if len(flagNames) > 0 {
		state.Flags = make(map[string]json.RawMessage, len(flagNames))
		for _, name := range flagNames {
			val := b.flagValue(actions, "", name)
			if val == nil {
				state.Flags[name] = json.RawMessage("null")
				continue
			}
			if raw, err := json.Marshal(val); err == nil {
				state.Flags[name] = raw
			}
		}
	}
	if actions == nil {
		return state
	}
	if actions.GetActiveTools != nil {
		state.ActiveTools = actions.GetActiveTools()
	}
	if actions.GetAllTools != nil {
		state.AllTools = actions.GetAllTools()
	}
	if actions.GetCommands != nil {
		state.Commands = actions.GetCommands()
	}
	if actions.GetThinkingLevel != nil {
		state.ThinkingLevel = actions.GetThinkingLevel()
	}
	if actions.GetModelInfo != nil {
		state.Model = actions.GetModelInfo()
	}
	if actions.GetScopedModels != nil {
		state.ScopedModels = snapshotScopedModels(actions.GetScopedModels())
	}
	if actions.GetSessionID != nil || actions.GetSessionName != nil || actions.GetSessionFile != nil || actions.GetLeafID != nil || actions.GetEntriesPage != nil || actions.SessionRead != nil {
		state.Session = &SessionStatePayload{}
		if actions.SessionRead != nil {
			if info, err := actions.SessionRead("info", nil); err == nil {
				state.Session.Info, _ = json.Marshal(info)
			}
		}
		if actions.GetSessionID != nil {
			state.Session.SessionID = actions.GetSessionID()
		}
		if actions.GetSessionName != nil {
			state.Session.SessionName = actions.GetSessionName()
		}
		if actions.GetSessionFile != nil {
			state.Session.SessionFile = actions.GetSessionFile()
		}
		if actions.GetLeafID != nil {
			state.Session.LeafID = actions.GetLeafID()
		}
		if actions.GetEntriesPage != nil && wantSessionLog {
			state.Session.EntriesAppended, state.Session.EntryCount, state.Session.EntriesRemaining, _ = actions.GetEntriesPage(cursor, sessionLogPageBytes)
		}
	}
	if actions.IsIdle != nil {
		state.IsIdle = actions.IsIdle()
	}
	if actions.IsProjectTrusted != nil {
		state.ProjectTrusted = actions.IsProjectTrusted()
	}
	if actions.GetGitBranch != nil || actions.GetExtensionStatuses != nil || actions.GetAvailableProviderCount != nil {
		state.FooterData = &FooterDataPayload{}
		if actions.GetGitBranch != nil {
			state.FooterData.GitBranch = actions.GetGitBranch()
		}
		if actions.GetExtensionStatuses != nil {
			state.FooterData.ExtensionStatuses = actions.GetExtensionStatuses()
		}
		if actions.GetAvailableProviderCount != nil {
			state.FooterData.AvailableProviderCount = actions.GetAvailableProviderCount()
		}
	}
	if actions.HasPendingMessages != nil {
		state.HasPendingMessages = actions.HasPendingMessages()
	}
	if actions.GetContextUsage != nil {
		if cu := actions.GetContextUsage(); cu != nil {
			state.ContextUsage = &extensionContextUsageDTO{Tokens: cu.Tokens, ContextWindow: cu.ContextWindow, Percent: cu.Percent}
		}
	}
	if actions.GetSystemPrompt != nil {
		state.SystemPrompt = actions.GetSystemPrompt()
	}
	// ExtensionCommandContext.getSystemPromptOptions (types.ts:355) is
	// synchronous upstream, so the Node runtime answers it from state.
	if actions.GetSystemPromptOptions != nil {
		if encoded, err := json.Marshal(actions.GetSystemPromptOptions()); err == nil {
			state.SystemPromptOptions = encoded
		}
	}
	return state
}

// SetThemeFunc registers the source of the host's active theme palette,
// which every state snapshot carries.
func (b *UIBridge) SetThemeFunc(fn func() any) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.theme = fn
}

// SetKeybindingsFunc registers the source of the host's resolved keybinding
// table, which every state snapshot carries.
func (b *UIBridge) SetKeybindingsFunc(fn func() any) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.keybindings = fn
}

// SetTerminalCapabilitiesFunc registers the source of the host terminal's
// resolved capabilities, sent to extensions with every state snapshot.
func (b *UIBridge) SetTerminalCapabilitiesFunc(fn func() TerminalCapabilitiesPayload) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.terminalCapabilities = fn
}

func NewUIBridge(invalidateTUI func()) *UIBridge {
	bridge := &UIBridge{
		widgets:          make(map[string]*PushProxy),
		customOverlays:   make(map[string]extension.RemoteOverlayHandle),
		interactiveFocus: make(chan struct{}, 1),
		extConns:         make(map[string]*Conn),
		editorsByConn:    make(map[string]*editorProxy),
		modelStreams:     make(map[modelStreamKey]context.CancelFunc),
		uiCtx:            extension.NoopUIContext,
		pendingStatuses:  make(map[string]string),
		invalidateTUI:    invalidateTUI,
	}
	bridge.interactiveFocus <- struct{}{}
	return bridge
}

// SetInvalidate replaces the TUI invalidation callback. Thread-safe.
// Called by InteractiveMode after TUI creation to wire live rendering.
//
// pig-specific: no upstream equivalent.
func (b *UIBridge) SetInvalidate(fn func()) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if fn == nil {
		fn = func() {}
	}
	b.invalidateTUI = fn
}

// Invalidate requests a TUI render using the current host callback.
func (b *UIBridge) Invalidate() {
	b.mu.RLock()
	fn := b.invalidateTUI
	b.mu.RUnlock()
	if fn != nil {
		fn()
	}
}

func (b *UIBridge) SetWidth(width int) {
	if width <= 0 {
		return
	}
	b.mu.Lock()
	b.currentWidth = width
	b.mu.Unlock()
}

// SetUIContext upgrades the UI context from noop to the real TUI. Thread-safe.
// Called by InteractiveMode after TUI creation.
func (b *UIBridge) SetUIContext(ctx extension.UIContext) {
	// The replay paints each slot's current value, so it holds the slot mutexes a later call takes; otherwise the replay can paint over a call applied after it read the value.
	b.headerMu.Lock()
	defer b.headerMu.Unlock()
	b.footerMu.Lock()
	defer b.footerMu.Unlock()
	b.mu.Lock()
	if ctx == nil {
		ctx = extension.NoopUIContext
	}
	b.uiCtx = ctx
	ready := ctx != extension.NoopUIContext
	b.uiReady = ready
	statuses := maps.Clone(b.pendingStatuses)
	footerSet, footer := b.pendingFooterSet, framedLines(b.pendingFooter, b.pendingFooterW)
	headerSet, header := b.pendingHeaderSet, framedLines(b.pendingHeader, b.pendingHeaderW)
	pendingLogin := cloneLoginDefinition(b.pendingLogin)
	conns := make([]*Conn, 0, len(b.extConns))
	for _, conn := range b.extConns {
		conns = append(conns, conn)
	}
	b.mu.Unlock()
	if ready {
		for key, text := range statuses {
			ctx.SetStatus(key, text)
		}
		if editor := b.activeEditor(); editor != nil {
			ctx.SetEditorComponent(editor)
		}
		if footerSet {
			ctx.SetFooter(footer)
		}
		if pendingLogin != nil {
			if err := ctx.SetLogin(*pendingLogin); err == nil {
				b.mu.Lock()
				b.pendingLogin = nil
				b.pendingHeaderSet = false
				b.pendingHeader = nil
				b.mu.Unlock()
			}
		} else if headerSet {
			ctx.SetHeader(header)
		}
	}

	theme := ctx.Theme()
	if theme == nil {
		return
	}
	args, err := json.Marshal(theme)
	if err != nil {
		return
	}
	for _, conn := range conns {
		_ = conn.Send(&Envelope{
			Type: MsgNotify,
			Notify: &NotifyPayload{
				Method: "theme_change",
				Args:   args,
			},
		})
	}
}

// SetNotifyFunc sets the function used to render notifications in the chat area.
// Thread-safe. Called by InteractiveMode after TUI creation.
func (b *UIBridge) SetNotifyFunc(fn func(message, level string)) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.notifyFunc = fn
}

// SetWidgetSyncFunc sets a callback invoked whenever an extension widget
// is set or cleared. The callback receives all current widget proxies.
// The host uses this to sync PushProxy components into the TUI layout.
// PushProxy implements Render(int)[]string + Invalidate() which matches
// tui.Component via structural typing.
func (b *UIBridge) SetWidgetSyncFunc(fn func(widgets map[string]*PushProxy)) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.widgetSyncFunc = fn
}

// SetWidgetRequestFunc installs a mode-specific serialized-widget observer.
func (b *UIBridge) SetWidgetRequestFunc(fn func(extName, key string, lines []string, opts extension.ExtensionWidgetOptions)) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.widgetRequestFunc = fn
}

// SetActions sets the agent-loop callbacks. Thread-safe.
// Called after the agent session is fully initialized.
func (b *UIBridge) SetActions(actions *HostCallbacks) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.actions = actions
	b.modelCatalogEncoder = nil
}

// BindCommandActions binds the non-nil command-context actions to the host
// actions subprocess extensions call, as Runner.BindCommandActions does for
// in-process extensions. A nil action keeps the one already bound.
func (b *UIBridge) BindCommandActions(actions extension.CommandActions) {
	// Upstream's waitForIdle has no effect to apply before it returns its
	// Promise, so the call is initiated before it waits for the session.
	if actions.WaitForIdleContext != nil {
		b.SetHostAction("waitForIdle", func(ctx context.Context) error {
			extension.CallInitiated(ctx)
			return actions.WaitForIdleContext(ctx)
		})
	} else if actions.WaitForIdle != nil {
		b.SetHostAction("waitForIdle", func(ctx context.Context) error {
			extension.CallInitiated(ctx)
			return actions.WaitForIdle()
		})
	}
	if actions.NewSessionContext != nil {
		b.SetHostAction("newSession", actions.NewSessionContext)
	} else if actions.NewSession != nil {
		b.SetHostAction("newSession", func(ctx context.Context, opts *extension.NewSessionOptions) (extension.CancelledResult, error) {
			result, err := actions.NewSession(opts)
			extension.CallInitiated(ctx)
			return result, err
		})
	}
	if actions.ForkContext != nil {
		b.SetHostAction("fork", actions.ForkContext)
	} else if actions.Fork != nil {
		b.SetHostAction("fork", func(ctx context.Context, entryID string, opts *extension.ForkOptions) (extension.CancelledResult, error) {
			result, err := actions.Fork(entryID, opts)
			extension.CallInitiated(ctx)
			return result, err
		})
	}
	if actions.NavigateTreeContext != nil {
		b.SetHostAction("navigateTree", actions.NavigateTreeContext)
	} else if actions.NavigateTree != nil {
		b.SetHostAction("navigateTree", func(ctx context.Context, targetID string, opts *extension.NavigateTreeOptions) (extension.CancelledResult, error) {
			result, err := actions.NavigateTree(targetID, opts)
			extension.CallInitiated(ctx)
			return result, err
		})
	}
	if actions.SwitchSessionContext != nil {
		b.SetHostAction("switchSession", actions.SwitchSessionContext)
	} else if actions.SwitchSession != nil {
		b.SetHostAction("switchSession", func(ctx context.Context, path string, opts *extension.SwitchSessionOptions) (extension.CancelledResult, error) {
			result, err := actions.SwitchSession(path, opts)
			extension.CallInitiated(ctx)
			return result, err
		})
	}
	if actions.ReloadContext != nil {
		b.SetHostAction("reload", actions.ReloadContext)
	} else if actions.Reload != nil {
		b.SetHostAction("reload", func(ctx context.Context) error {
			err := actions.Reload()
			extension.CallInitiated(ctx)
			return err
		})
	}
}

// SetHostAction sets a single host callback by name. Thread-safe.
// This allows the interactive mode to wire callbacks one-by-one without
// importing the HostCallbacks type (avoiding circular deps).
//
// Accepted function signatures per key:
//   - "getFlag":          func(string, string) any
//   - "getActiveTools":   func() []string
//   - "getAllTools":      func() []ToolInfo
//   - "getCommands":      func() []CommandInfo
//   - "getThinkingLevel": func() string
//   - "setThinkingLevel": func(string)
//   - "getContextUsage":  func() *extension.ContextUsage
//   - "getSystemPrompt":  func() string
//   - "getSessionName":   func() string
//   - "getSessionID":     func() string
//   - "getSessionFile":   func() string
//   - "getLeafID":       func() string
//   - "getEntries":      func() []map[string]any
//   - "exec":            func(context.Context, string, []string, *extension.ExecOptions) (extension.ExecResult, error)
//     OR func(string, []string, *extension.ExecOptions) (extension.ExecResult, error)
//   - "sendMessage":     func(extension.CustomMessageRef, SendMessageOptions) error
//   - "sendUserMessage": func(any, SendUserMessageOptions) error
//   - "appendEntry":     func(string, any, *DirectEntryAppend) error
//   - "setLabel":        func(string, string) error
//   - "setActiveTools":  func([]string)
//   - "refreshTools":    func() error
//   - "setModel":        func(context.Context, string) (bool, error)
//   - "isIdle":          func() bool
//   - "abort":           func()
//   - "hasPendingMessages": func() bool
//   - "shutdown":        func()
//   - "compact":         func(context.Context, *extension.CompactOptions)
//   - "waitForIdle":     func(context.Context) error
//   - "reload":          func(context.Context) error
//   - "getModelAuth":    func(context.Context, string, string) map[string]any
//   - "complete":        func(context.Context, map[string]any, map[string]any, map[string]any) (map[string]any, error)
//   - "setSessionName":   func(string)
func (b *UIBridge) SetHostAction(key string, fn any) {
	b.mu.Lock()
	if b.actions == nil {
		b.actions = &HostCallbacks{}
	}
	switch key {
	case "getFlag":
		b.actions.GetFlag = fn.(func(string, string) any)
	case "getActiveTools":
		b.actions.GetActiveTools = fn.(func() []string)
	case "getAllTools":
		b.actions.GetAllTools = fn.(func() []ToolInfo)
	case "getCommands":
		b.actions.GetCommands = fn.(func() []CommandInfo)
	case "getThinkingLevel":
		b.actions.GetThinkingLevel = fn.(func() string)
	case "setThinkingLevel":
		b.actions.SetThinkingLevel = fn.(func(string))
	case "getContextUsage":
		b.actions.GetContextUsage = fn.(func() *extension.ContextUsage)
	case "getSystemPrompt":
		b.actions.GetSystemPrompt = fn.(func() string)
	case "getSystemPromptOptions":
		b.actions.GetSystemPromptOptions = fn.(func() extension.BuildSystemPromptOptions)
	case "exec":
		switch f := fn.(type) {
		case func(context.Context, string, []string, *extension.ExecOptions) (extension.ExecResult, error):
			b.actions.ExecContext = f
		case func(string, []string, *extension.ExecOptions) (extension.ExecResult, error):
			b.actions.Exec = f
		default:
			b.mu.Unlock()
			panic(fmt.Sprintf("SetHostAction: invalid exec action %T", fn))
		}
	case "sendMessage":
		b.actions.SendMessage = fn.(func(extension.CustomMessageRef, SendMessageOptions) error)
	case "sendUserMessage":
		b.actions.SendUserMessage = fn.(func(any, SendUserMessageOptions) error)
	case "appendEntry":
		b.actions.AppendEntry = fn.(func(string, any, *DirectEntryAppend) error)
	case "setLabel":
		b.actions.SetLabel = fn.(func(string, string) error)
	case "setActiveTools":
		b.actions.SetActiveTools = fn.(func([]string))
	case "refreshTools":
		b.actions.RefreshTools = fn.(func() error)
	case "setModel":
		b.actions.SetModel = fn.(func(context.Context, string) (bool, error))
	case "isIdle":
		b.actions.IsIdle = fn.(func() bool)
	case "isProjectTrusted":
		b.actions.IsProjectTrusted = fn.(func() bool)
	case "getGitBranch":
		b.actions.GetGitBranch = fn.(func() string)
	case "getExtensionStatuses":
		b.actions.GetExtensionStatuses = fn.(func() map[string]string)
	case "getAvailableProviderCount":
		b.actions.GetAvailableProviderCount = fn.(func() int)
	case "abort":
		b.actions.Abort = fn.(func())
	case "hasPendingMessages":
		b.actions.HasPendingMessages = fn.(func() bool)
	case "shutdown":
		b.actions.Shutdown = fn.(func())
	case "compact":
		b.actions.Compact = fn.(func(context.Context, *extension.CompactOptions))
	case "waitForIdle":
		b.actions.WaitForIdle = fn.(func(context.Context) error)
	case "newSession":
		b.actions.NewSession = fn.(func(context.Context, *extension.NewSessionOptions) (extension.CancelledResult, error))
	case "fork":
		b.actions.Fork = fn.(func(context.Context, string, *extension.ForkOptions) (extension.CancelledResult, error))
	case "navigateTree":
		b.actions.NavigateTree = fn.(func(context.Context, string, *extension.NavigateTreeOptions) (extension.CancelledResult, error))
	case "switchSession":
		b.actions.SwitchSession = fn.(func(context.Context, string, *extension.SwitchSessionOptions) (extension.CancelledResult, error))
	case "reload":
		b.actions.Reload = fn.(func(context.Context) error)
	case "getScopedModels":
		b.actions.GetScopedModels = fn.(func() []extension.ScopedModel)
	case "getModelInfo":
		b.actions.GetModelInfo = fn.(func() map[string]any)
	case "getModel":
		b.actions.GetModel = fn.(func(string, string) map[string]any)
	case "getModels":
		b.actions.GetModels = fn.(func() []map[string]any)
		b.modelCatalogEncoder = nil
	case "getBranch":
		b.actions.GetBranch = fn.(func() []json.RawMessage)
	case "getEntries":
		b.actions.GetEntries = fn.(func() []json.RawMessage)
	case "getEntriesPage":
		b.actions.GetEntriesPage = fn.(func(int, int) ([]json.RawMessage, int, bool, string))
	case "getSessionID":
		b.actions.GetSessionID = fn.(func() string)
	case "getSessionFile":
		b.actions.GetSessionFile = fn.(func() string)
	case "getLeafID":
		b.actions.GetLeafID = fn.(func() string)
	case "getModelAuth":
		b.actions.GetModelAuth = fn.(func(context.Context, string, string) map[string]any)
	case "complete":
		b.actions.Complete = fn.(func(context.Context, map[string]any, map[string]any, map[string]any) (map[string]any, error))
	case "streamModel":
		b.actions.StreamModel = fn.(func(context.Context, map[string]any, map[string]any) (*ai.AssistantMessageEventStream, error))
	case "getSessionName":
		b.actions.GetSessionName = fn.(func() string)
	case "getModelRegistryState":
		b.actions.GetModelRegistryState = fn.(func() map[string]any)
	case "getProviderAuth":
		b.actions.GetProviderAuth = fn.(func(context.Context, string) (map[string]any, error))
	case "refreshModelRegistry":
		b.actions.RefreshModelRegistry = fn.(func(context.Context, *bool, []string, *bool) (map[string]any, error))
	case "sessionRead":
		b.actions.SessionRead = fn.(func(string, json.RawMessage) (any, error))
	case "setSessionName":
		b.actions.SetSessionName = fn.(func(string) error)
	default:
		b.mu.Unlock()
		// Every key is a literal in pig's own startup wiring, so an unknown one
		// is a typo that would otherwise leave the action unbound and the
		// capability silently missing from every extension.
		panic(fmt.Sprintf("SetHostAction: unknown action %q", key))
	}
	b.mu.Unlock()
	if key == "getModels" || key == "getModelRegistryState" {
		b.PublishModelCatalog()
	}
}

// HandleCall processes an extension→host call message and returns the result.
// This is the main dispatch point for ALL extension API methods:
//
//   - UI methods: ui.notify, ui.setStatus, ui.select, ui.confirm, ui.input,
//     ui.setWidget, ui.setWorkingIndicator, ui.setWorkingMessage,
//     ui.setHiddenThinkingLabel, ui.setFooter, ui.setHeader, ui.setTitle,
//     ui.custom, ui.pasteToEditor, ui.setEditorText, ui.getEditorText,
//     ui.editor, ui.setEditorComponent, ui.addAutocompleteProvider,
//     ui.onTerminalInput, ui.theme, ui.getAllThemes, ui.getTheme,
//     ui.setTheme, ui.getToolsExpanded, ui.setToolsExpanded
//   - Action methods: sendMessage, sendUserMessage, appendEntry,
//     getSessionName, setSessionName, setLabel, getActiveTools,
//     getAllTools, setActiveTools, refreshTools, getCommands,
//     setModel, getThinkingLevel, setThinkingLevel
func (b *UIBridge) HandleCall(extName string, call *CallPayload) (*CallResultPayload, error) {
	return b.handleCall(context.Background(), extName, nil, call)
}

func (b *UIBridge) HandleCallFrom(extName string, owner *Conn, call *CallPayload) (*CallResultPayload, error) {
	return b.handleCall(context.Background(), extName, owner, call)
}

// reportsDialogInitiation reports whether ui marks a dialog call's initiation
// itself once the dialog is installed.
func reportsDialogInitiation(ui extension.UIContext, method string) bool {
	if method == CallUICustom || !takesInteractiveFocus(method) {
		return false
	}
	reporter, ok := ui.(extension.DialogInitiationReporter)
	return ok && reporter.ReportsDialogInitiation()
}

// gatesOtherLanes reports whether a call's initiation orders the calls of
// other lanes. ui.custom initiates on the TUI main loop, which may be waiting
// for a terminal-input handler's reply, so gating on it could stall until the
// liveness bound.
func gatesOtherLanes(method string) bool {
	return method != CallUICustom && takesInteractiveFocus(method)
}

func takesInteractiveFocus(method string) bool {
	switch method {
	case "ui.select", "ui.confirm", "ui.input", "ui.editor", CallUICustom:
		return true
	default:
		return false
	}
}

func (b *UIBridge) handleCall(ctx context.Context, extName string, owner *Conn, call *CallPayload) (*CallResultPayload, error) {
	if call == nil {
		return nil, errors.New("nil extension host call")
	}
	// A blocking dialog opens its ui_prompt scope now and closes it on return.
	defer b.beginUIPrompt(call)()
	// Snapshot UI context and actions under lock.
	b.mu.RLock()
	ui := b.uiCtx
	actions := b.actions
	b.mu.RUnlock()
	// Upstream runner.ts:noOpUIContext ignores mutations without retaining
	// factories, subscriptions, or state for a later UI binding.
	if ui == extension.NoopUIContext {
		switch call.Method {
		case "ui.notify", "ui.setStatus", "ui.setWorkingIndicator", "ui.setWorkingMessage",
			"ui.setWorkingVisible", "ui.setHiddenThinkingLabel", "ui.setWidget", "ui.setFooter",
			"ui.setHeader", "ui.setTitle", "ui.setEditorComponent", "ui.pasteToEditor",
			"ui.setEditorText", "ui.setToolsExpanded", "ui.addAutocompleteProvider",
			"ui.onTerminalInput", "ui.offTerminalInput":
			return &CallResultPayload{}, nil
		case CallUICustom:
			extension.CallInitiated(ctx)
			return &CallResultPayload{}, nil
		}
	}
	// A UI implementation that cannot report dialog installation has no
	// earlier initiation boundary. Production interactive UI contexts report
	// from inside Select/Confirm/Input/Editor after queuing the dialog. Custom
	// overlays report from their bind callback below.
	if call.Method != CallUICustom && takesInteractiveFocus(call.Method) && !reportsDialogInitiation(ui, call.Method) {
		extension.CallInitiated(ctx)
	}
	if call.Method != CallUICustom && takesInteractiveFocus(call.Method) {
		select {
		case <-b.interactiveFocus:
			defer func() { b.interactiveFocus <- struct{}{} }()
		default:
			// Another dialog holds focus: this one starts when it closes.
			extension.CallInitiated(ctx)
			select {
			case <-b.interactiveFocus:
				defer func() { b.interactiveFocus <- struct{}{} }()
			case <-ctx.Done():
				return &CallResultPayload{Error: &ErrorInfo{Code: "cancelled", Message: ctx.Err().Error()}}, nil
			}
		}
	}

	switch call.Method {
	// ── Category 1: Notifications & Status ───────────────────────────────
	case "ui.notify":
		return b.handleNotify(ui, call.Args)
	case "ui.setStatus":
		return b.handleSetStatus(call.Args)
	case "ui.setWorkingIndicator":
		return b.handleSetWorkingIndicator(ui, call.Args)
	case "ui.setWorkingMessage":
		return b.handleSetWorkingMessage(ui, call.Args)
	case "ui.setWorkingVisible":
		return b.handleSetWorkingVisible(ui, call.Args)
	case "ui.setHiddenThinkingLabel":
		return b.handleSetHiddenThinkingLabel(ui, call.Args)
	case "ui.setWidget":
		return b.handleSetWidget(extName, call.Args)

	// ── Category 2: User Interaction (blocking) ──────────────────────────
	case "ui.select":
		return b.handleSelect(ctx, ui, call.Args)
	case "ui.confirm":
		return b.handleConfirm(ctx, ui, call.Args)
	case "ui.input":
		return b.handleInput(ctx, ui, call.Args)
	case CallUICustomControl:
		return b.handleCustomControl(ctx, extName, owner, call.Args)
	case CallUICustom:
		return b.handleCustom(ctx, extName, owner, ui, call.Args)
	case "ui.editor":
		return b.handleEditor(ctx, ui, call.Args)

	// ── Category 3: Component Factories ──────────────────────────────────
	case "ui.setFooter":
		return b.handleSetFooter(call.Args)
	case "ui.setHeader":
		return b.handleSetHeader(call.Args)
	case CallUISetLogin:
		return b.handleSetLogin(call.Args)
	case "ui.setTitle":
		return b.handleSetTitle(ui, call.Args)
	case "ui.setEditorComponent":
		return b.handleSetEditorComponent(ui, call.Args)

	// ── Category 4: Editor Access ────────────────────────────────────────
	case "ui.pasteToEditor":
		return b.handlePasteToEditor(ui, call.Args)
	case "ui.setEditorText":
		return b.handleSetEditorText(ui, call.Args)
	case "ui.getEditorText":
		return b.handleGetEditorText(ui)
	case "ui.getEditorComponent":
		return b.handleGetEditorComponent(ui)

	// ── Category 5: Theme ────────────────────────────────────────────────
	case "ui.theme":
		return b.handleTheme(ui)
	case "ui.getAllThemes":
		return b.handleGetAllThemes(ui)
	case "ui.getTheme":
		return b.handleGetTheme(ui, call.Args)
	case "ui.setTheme":
		return b.handleSetTheme(ui, call.Args)

	// ── Category 6: Tool expansion ───────────────────────────────────────
	case "ui.getToolsExpanded":
		return b.handleGetToolsExpanded(ui)
	case "ui.setToolsExpanded":
		return b.handleSetToolsExpanded(ui, call.Args)

	// ── Category 7: Advanced UI (streaming/callback) ─────────────────────
	case "ui.addAutocompleteProvider":
		return b.handleAddAutocompleteProvider(ctx, extName, owner, ui, call.Args)
	case "ui.autocomplete.invoke":
		return b.handleAutocompleteInvoke(ctx, owner, call.Args)
	case "ui.autocomplete.cancel":
		return b.handleAutocompleteCancel(owner, call.Args)
	case "ui.autocomplete.current":
		return b.handleAutocompleteCurrent(ctx, extName, owner, ui)
	case "ui.onTerminalInput":
		return b.handleOnTerminalInput(extName, call.Args)
	case "ui.offTerminalInput":
		return b.handleOffTerminalInput(extName, call.Args)

	// ── Category 8: Agent Actions (ExtensionActions) ─────────────────────
	case "sendMessage":
		return b.handleSendMessage(actions, call.Args)
	case "sendUserMessage":
		return b.handleSendUserMessage(actions, call.Args)
	case "appendEntry":
		return b.handleAppendEntry(actions, call.Args)
	case "exec":
		return b.handleExec(ctx, actions, call.Args)
	case "getFlag":
		return b.handleGetFlag(extName, actions, call.Args)
	case "getSessionName":
		return b.handleGetSessionName(actions)
	case "getSessionID":
		return b.handleGetSessionID(actions)
	case "getSessionFile":
		return b.handleGetSessionFile(actions)
	case "getLeafID":
		return b.handleGetLeafID(actions)
	case "watchSessionLog":
		return b.handleWatchSessionLog(extName, call.Args)
	case "setSessionName":
		return b.handleSetSessionName(actions, call.Args)
	case "setLabel":
		return b.handleSetLabel(actions, call.Args)
	case "getActiveTools":
		return b.handleGetActiveTools(actions)
	case "getAllTools":
		return b.handleGetAllTools(actions)
	case "setActiveTools":
		return b.handleSetActiveTools(actions, call.Args)
	case "refreshTools":
		return b.handleRefreshTools(actions)
	case "getCommands":
		return b.handleGetCommands(actions)
	case "setModel":
		return b.handleSetModel(ctx, actions, call.Args)
	case "getThinkingLevel":
		return b.handleGetThinkingLevel(actions)
	case "setThinkingLevel":
		return b.handleSetThinkingLevel(actions, call.Args)
	case "getContextUsage":
		return b.handleGetContextUsage(actions)
	case "getSystemPrompt":
		return b.handleGetSystemPrompt(actions)
	case "getSystemPromptOptions":
		return b.handleGetSystemPromptOptions(actions)
	case "getScopedModels":
		var models []extension.ScopedModel
		if actions != nil && actions.GetScopedModels != nil {
			models = actions.GetScopedModels()
		}
		data, err := json.Marshal(snapshotScopedModels(models))
		return &CallResultPayload{Result: data}, err
	case "getModelInfo":
		return b.handleGetModelInfo(actions)
	case "getModel":
		return b.handleGetModel(actions, call.Args)
	case "getBranch":
		return b.handleGetBranch(actions)
	case "getEntries":
		return b.handleGetEntries(actions)
	case "getModelAuth":
		return b.handleGetModelAuth(ctx, actions, call.Args)
	case "getModelRegistryState":
		return b.handleGetModelRegistryState()
	case "getProviderAuth":
		return b.handleGetProviderAuth(ctx, actions, call.Args)
	case "refreshModelRegistry":
		return b.handleRefreshModelRegistry(ctx, actions, call.Args)
	case "sessionRead":
		return b.handleSessionRead(actions, call.Args)
	case "complete":
		return b.handleComplete(ctx, actions, call.Args)
	case "modelStream":
		return b.handleModelStream(ctx, owner, actions, call.Args)
	case "cancelModelStream":
		return b.handleCancelModelStream(owner, call.Args)

	// Agent control.
	case "isIdle":
		return b.handleIsIdle(actions)
	case "isProjectTrusted":
		return b.handleIsProjectTrusted(actions)
	case "abort":
		return b.handleAbort(actions)
	case "hasPendingMessages":
		return b.handleHasPendingMessages(actions)
	case "shutdown":
		return b.handleShutdown(actions)
	case "compact":
		return b.handleCompact(ctx, actions, call.Args)

	// Session control.
	case "waitForIdle":
		return b.handleWaitForIdle(ctx, actions)
	case "newSession":
		return b.handleNewSession(ctx, actions, call.Args)
	case "fork":
		return b.handleFork(ctx, actions, call.Args)
	case "navigateTree":
		return b.handleNavigateTree(ctx, actions, call.Args)
	case "switchSession":
		return b.handleSwitchSession(ctx, actions, call.Args)
	case "reload":
		return b.handleReload(ctx, actions)

	default:
		return &CallResultPayload{
			Error: &ErrorInfo{
				Code:    "unknown_method",
				Message: fmt.Sprintf("unknown call method: %s", call.Method),
			},
		}, nil
	}
}

// HandleWidgetPush processes a widget_push message. A push with a width is a
// frame a component rendered at that width. A push without one is a string
// list widget (the Go, Rust and Python SDKs' setWidget(key, string[])), which
// the host lays out at its own width as Pi's setExtensionWidget does.
func (b *UIBridge) HandleWidgetPush(extName string, push *WidgetPushPayload) {
	if push.Width == 0 {
		b.updateWidget(extName, push.Key, func(proxy *PushProxy) { proxy.UpdateContent(push.Lines) })
		return
	}
	b.updateWidget(extName, push.Key, func(proxy *PushProxy) { proxy.UpdateLinesAt(push.Lines, push.Width) })
}

// updateWidget applies update to the widget's proxy, creating and mounting the
// proxy when the widget is new.
func (b *UIBridge) updateWidget(extName, widgetKey string, update func(*PushProxy)) {
	key := extName + ":" + widgetKey

	b.mu.Lock()
	if b.uiCtx == extension.NoopUIContext {
		b.mu.Unlock()
		return
	}
	proxy, ok := b.widgets[key]
	if !ok {
		// b.Invalidate reads the callback at call time, so a widget pushed
		// before the TUI wires SetInvalidate still repaints on later pushes.
		proxy = NewPushProxy(b.Invalidate, nil)
		b.widgets[key] = proxy
	}
	b.mu.Unlock()

	update(proxy)
	// Only sync widget container when a NEW proxy was created (widget added).
	// Existing proxy updates request a render through PushProxy.UpdateLines.
	if !ok {
		b.notifyWidgetSync()
	}
}

// notifyWidgetSync calls the widgetSyncFunc callback with the current set
// of widget proxies. Thread-safe: reads widgetSyncFunc under lock, then
// calls AllWidgets (which also takes the lock).
func (b *UIBridge) notifyWidgetSync() {
	b.mu.RLock()
	fn := b.widgetSyncFunc
	b.mu.RUnlock()
	if fn != nil {
		fn(b.AllWidgets())
	}
}

// GetWidget returns the PushProxy for a given extension:key pair, or nil.
func (b *UIBridge) GetWidget(extName, key string) *PushProxy {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.widgets[extName+":"+key]
}

// AllWidgets returns all widget proxies (for rendering).
func (b *UIBridge) AllWidgets() map[string]*PushProxy {
	b.mu.RLock()
	defer b.mu.RUnlock()
	out := make(map[string]*PushProxy, len(b.widgets))
	maps.Copy(out, b.widgets)
	return out
}

// ClearExtension removes all widgets for a given extension (on crash/shutdown).
func (b *UIBridge) ClearExtension(extName string) {
	b.clearExtension(extName, nil, false)
}

func (b *UIBridge) ClearExtensionConn(extName string, owner *Conn) {
	b.clearExtension(extName, owner, true)
}

func (b *UIBridge) clearExtension(extName string, owner *Conn, ownerOnly bool) {
	prefix := extName + ":"
	var widgets []*PushProxy
	var overlays []extension.RemoteOverlayHandle
	b.mu.Lock()
	currentOwner := b.extConns[extName] == owner
	for key, query := range b.autocompleteQueries {
		if key.owner == owner || (!ownerOnly && key.owner == b.extConns[extName]) {
			query.cancel()
			delete(b.autocompleteQueries, key)
		}
	}
	if !ownerOnly || currentOwner {
		for k, proxy := range b.widgets {
			if strings.HasPrefix(k, prefix) {
				widgets = append(widgets, proxy)
				delete(b.widgets, k)
			}
		}
	}
	for k, handle := range b.customOverlays {
		if strings.HasPrefix(k, customOverlayOwnerPrefix(extName, owner)) || (!ownerOnly && strings.HasPrefix(k, prefix)) {
			overlays = append(overlays, handle)
			delete(b.customOverlays, k)
		}
	}
	for id, ref := range b.autocompleteReferences {
		if (ownerOnly && ref.owner == owner) || (!ownerOnly && ref.name == extName) {
			delete(b.autocompleteReferences, id)
		}
	}
	for provider, origin := range b.autocompleteOrigins {
		if origin.owner == owner || (!ownerOnly && origin.owner == b.extConns[extName]) {
			delete(b.autocompleteOrigins, provider)
		}
	}
	if !ownerOnly || currentOwner {
		delete(b.extConns, extName)
	}
	b.mu.Unlock()
	for _, proxy := range widgets {
		proxy.Clear()
	}
	if len(widgets) > 0 {
		b.notifyWidgetSync()
	}
	for _, handle := range overlays {
		handle.Close(nil)
	}
	b.clearEditorConn(extName, owner, ownerOnly)
}

// SetModelCatalog installs equivalent value and JSON projections atomically. Publication uses the encoder without changing the value getter or its notification boundary. Replacing the getter through SetHostAction or SetActions clears the encoder.
// pig additive (D19): subprocess snapshot encoding reuses the same current model data without changing the extension API.
func (b *UIBridge) SetModelCatalog(getModels func() []map[string]any, encodeModels func() (json.RawMessage, error)) {
	b.mu.Lock()
	if b.actions == nil {
		b.actions = &HostCallbacks{}
	}
	b.actions.GetModels = getModels
	b.modelCatalogEncoder = encodeModels
	b.mu.Unlock()
	b.PublishModelCatalog()
}

func (b *UIBridge) ModelCatalog() []map[string]any {
	b.mu.RLock()
	actions := b.actions
	b.mu.RUnlock()
	if actions == nil || actions.GetModels == nil {
		return nil
	}
	return actions.GetModels()
}

// PublishModelCatalog sends the current registry snapshot to every connected
// extension. With no connection it builds nothing: a later connection receives
// its own snapshot from RegisterExtConn.
func (b *UIBridge) PublishModelCatalog() {
	b.mu.RLock()
	connections := make([]*Conn, 0, len(b.extConns))
	for _, connection := range b.extConns {
		connections = append(connections, connection)
	}
	b.mu.RUnlock()
	if len(connections) == 0 {
		return
	}
	message := b.modelCatalogUpdate()
	if message == nil {
		return
	}
	for _, connection := range connections {
		_ = connection.sendEncoded(message)
	}
}

func (b *UIBridge) modelCatalogUpdate() encodedEnvelope {
	state, err := b.modelRegistryState(true)
	if err != nil {
		return nil
	}
	args, err := json.Marshal(state)
	if err != nil {
		return nil
	}
	// args is already complete, escaped JSON. Re-marshaling it as RawMessage would parse and copy the catalog again for every connection.
	const prefix = `{"type":"notify","notify":{"method":"model_registry_update","args":`
	data := make(encodedEnvelope, 0, len(prefix)+len(args)+2)
	data = append(data, prefix...)
	data = append(data, args...)
	return append(data, '}', '}')
}

// ModelRegistryState is the registry snapshot an extension's synchronous
// ctx.modelRegistry reads answer from: the host's registry state (the catalog
// under "models", availability, provider auth and the registry error) and the
// provider configs extensions registered, in registration order. Upstream
// ModelRegistry reads the same state in process.
func (b *UIBridge) ModelRegistryState() map[string]any {
	state, _ := b.modelRegistryState(false)
	return state
}

func (b *UIBridge) modelRegistryState(encodedModels bool) (map[string]any, error) {
	b.mu.RLock()
	actions := b.actions
	encodeModels := b.modelCatalogEncoder
	registered := make([]map[string]any, 0, len(b.registeredProviderOrder))
	for _, name := range b.registeredProviderOrder {
		if native, ok := b.registeredNativeProviders[name]; ok {
			registered = append(registered, map[string]any{"name": name, "native": native})
		} else {
			registered = append(registered, map[string]any{"name": name, "config": b.registeredProviderConfigs[name]})
		}
	}
	b.mu.RUnlock()
	state := map[string]any{}
	if actions != nil && actions.GetModelRegistryState != nil {
		maps.Copy(state, actions.GetModelRegistryState())
	}
	if _, ok := state["models"]; !ok {
		if encodedModels && encodeModels != nil {
			models, err := encodeModels()
			if err != nil {
				return nil, err
			}
			state["models"] = models
		} else {
			state["models"] = b.ModelCatalog()
		}
	}
	state["registered"] = registered
	return state, nil
}

// RecordNativeProviderRegistration publishes native ownership metadata.
func (b *UIBridge) RecordNativeProviderRegistration(name string, native NativeProviderDeclaration) {
	b.recordNativeProviderRegistration(name, native)
	b.PublishModelCatalog()
}

func (b *UIBridge) recordNativeProviderRegistration(name string, native NativeProviderDeclaration) {
	b.mu.Lock()
	if b.registeredNativeProviders == nil {
		b.registeredNativeProviders = map[string]NativeProviderDeclaration{}
	}
	delete(b.registeredProviderConfigs, name)
	b.registeredNativeProviders[name] = native
	if !slices.Contains(b.registeredProviderOrder, name) {
		b.registeredProviderOrder = append(b.registeredProviderOrder, name)
	}
	b.mu.Unlock()
}

// RecordProviderRegistration records a provider config an extension
// registered. A re-registration merges its defined top-level values over the
// previous config and keeps the provider's place, as upstream
// ModelRuntime.registerProvider does with its extensionProviders map.
func (b *UIBridge) RecordProviderRegistration(name string, config json.RawMessage) {
	var incoming map[string]json.RawMessage
	if err := json.Unmarshal(config, &incoming); err != nil || incoming == nil {
		incoming = map[string]json.RawMessage{}
	}
	b.mu.Lock()
	if b.registeredProviderConfigs == nil {
		b.registeredProviderConfigs = make(map[string]map[string]json.RawMessage)
	}
	previous, exists := b.registeredProviderConfigs[name]
	delete(b.registeredNativeProviders, name)
	exists = exists || slices.Contains(b.registeredProviderOrder, name)
	merged := make(map[string]json.RawMessage, len(previous)+len(incoming))
	maps.Copy(merged, previous)
	maps.Copy(merged, incoming)
	b.registeredProviderConfigs[name] = merged
	if !exists {
		b.registeredProviderOrder = append(b.registeredProviderOrder, name)
	}
	b.mu.Unlock()
	b.PublishModelCatalog()
}

// ForgetProviderRegistration drops a registered provider config. Upstream
// ModelRuntime.unregisterProvider.
func (b *UIBridge) ForgetProviderRegistration(name string) {
	b.mu.Lock()
	_, exists := b.registeredProviderConfigs[name]
	exists = exists || slices.Contains(b.registeredProviderOrder, name)
	delete(b.registeredNativeProviders, name)
	if exists {
		delete(b.registeredProviderConfigs, name)
		b.registeredProviderOrder = slices.DeleteFunc(b.registeredProviderOrder, func(existing string) bool { return existing == name })
	}
	b.mu.Unlock()
	if exists {
		b.PublishModelCatalog()
	}
}

// RegisterExtConn associates an extension's socket connection with the
// bridge so input notifications generated by overlay surfaces can be
// forwarded back to the extension subprocess.
//
// pig-specific: no upstream equivalent.
func (b *UIBridge) RegisterExtConn(extName string, conn *Conn) {
	b.mu.Lock()
	b.extConns[extName] = conn
	b.mu.Unlock()
	if message := b.modelCatalogUpdate(); message != nil {
		_ = conn.sendEncoded(message)
	}
}

// HandleNotify routes a Node→Go fire-and-forget notification. Currently
// the only routed notifications are remote-overlay render/close events,
// but the surface is the natural extension point for future
// notification-only host APIs.
//
// pig-specific: no upstream equivalent.
func (b *UIBridge) HandleNotify(extName string, n *NotifyPayload) {
	b.HandleNotifyFrom(extName, nil, n)
}

func (b *UIBridge) HandleNotifyFrom(extName string, owner *Conn, n *NotifyPayload) {
	if n == nil {
		return
	}
	if b.handleEditorNotify(extName, owner, n) {
		return
	}
	switch n.Method {
	case NotifyUICustomRender:
		var p RemoteOverlayRenderPayload
		if err := json.Unmarshal(n.Args, &p); err != nil {
			return
		}
		b.mu.RLock()
		width := b.currentWidth
		handle, ok := b.customOverlays[customOverlayOwnerPrefix(extName, owner)+p.Key]
		b.mu.RUnlock()
		if !ok {
			return
		}
		handle.(*overlayProxy).UpdateFrame(p.Lines, p.Width, p.Seq, width)
	case NotifyUICustomClose:
		var p RemoteOverlayClosePayload
		if err := json.Unmarshal(n.Args, &p); err != nil {
			return
		}
		b.mu.RLock()
		handle, ok := b.customOverlays[customOverlayOwnerPrefix(extName, owner)+p.Key]
		b.mu.RUnlock()
		if !ok {
			return
		}
		h := handle.(*overlayProxy)
		if p.Error != "" {
			h.Close(remoteOverlayError{message: p.Error})
			return
		}
		var v any
		if len(p.Result) > 0 {
			_ = json.Unmarshal(p.Result, &v)
		}
		h.Close(v)
	}
}

type remoteOverlayError struct {
	message string
}

// reserveCustomOverlay installs the frame target before the call handler starts.
// The connection delivers call and notify frames in order, but host calls run on
// separate goroutines. Reserving on the serial incoming loop prevents an initial
// frame from racing ahead of the ui.custom handler without allowing late frames
// to recreate a completed overlay.
func (b *UIBridge) reserveCustomOverlay(extName string, owner *Conn, args json.RawMessage) {
	b.mu.RLock()
	hasUI := b.uiCtx != extension.NoopUIContext
	b.mu.RUnlock()
	if !hasUI {
		return
	}
	var payload RemoteOverlayOpenPayload
	if json.Unmarshal(args, &payload) != nil || payload.Key == "" {
		return
	}
	b.customOverlayProxyFor(extName, owner, payload.Key)
}

// customOverlayProxy creates a buffering proxy for direct bridge callers.
// Production reserves the proxy on the serial host input loop before it
// dispatches the blocking call.
func (b *UIBridge) customOverlayProxy(extName, key string) *overlayProxy {
	return b.customOverlayProxyFor(extName, nil, key)
}

func (b *UIBridge) customOverlayProxyFor(extName string, owner *Conn, key string) *overlayProxy {
	fullKey := customOverlayOwnerPrefix(extName, owner) + key
	b.mu.Lock()
	defer b.mu.Unlock()
	if existing, ok := b.customOverlays[fullKey].(*overlayProxy); ok {
		return existing
	}
	proxy := newOverlayProxy()
	b.customOverlays[fullKey] = proxy
	return proxy
}

func customOverlayOwnerPrefix(extName string, owner *Conn) string {
	return fmt.Sprintf("%s:%p:", extName, owner)
}

func (b *UIBridge) unregisterCustomOverlay(extName string, owner *Conn, key string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.customOverlays, customOverlayOwnerPrefix(extName, owner)+key)
}

func (b *UIBridge) sendCustomInput(ctx context.Context, extName string, conn *Conn, key, data string) {
	b.mu.RLock()
	handle, ok := b.customOverlays[customOverlayOwnerPrefix(extName, conn)+key]
	b.mu.RUnlock()
	if !ok {
		return
	}
	proxy := handle.(*overlayProxy)
	if conn == nil {
		proxy.Close(remoteOverlayError{message: "extension connection is unavailable"})
		return
	}
	payload := RemoteOverlayInputPayload{Key: key, Data: data}
	if state, err := proxy.Control(ctx, "", false, nil); err == nil {
		payload.State = &state
	}
	args, _ := json.Marshal(payload)
	if err := conn.Send(&Envelope{
		Type: MsgNotify,
		Notify: &NotifyPayload{
			Method: NotifyUICustomInput,
			Args:   args,
		},
	}); err != nil {
		proxy.Close(remoteOverlayError{message: "deliver focused input: " + err.Error()})
	}
}

// ═══════════════════════════════════════════════════════════════════════════════
// Category 1: Notifications & Status
// ═══════════════════════════════════════════════════════════════════════════════

func (b *UIBridge) handleNotify(ui extension.UIContext, args json.RawMessage) (*CallResultPayload, error) {
	var p struct {
		Message string `json:"message"`
		Level   string `json:"level"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return nil, fmt.Errorf("parse notify args: %w", err)
	}
	if p.Level == "" {
		p.Level = "info"
	}
	// Prefer the direct notifyFunc (renders in chat area) over the
	// extension.UIContext.Notify (which may be noop if SetUIContext
	// hasn't been called with a full TUI adapter).
	b.mu.RLock()
	fn := b.notifyFunc
	b.mu.RUnlock()
	if fn != nil {
		fn(p.Message, p.Level)
	} else {
		ui.Notify(p.Message, p.Level)
	}
	return &CallResultPayload{}, nil
}

// ═══════════════════════════════════════════════════════════════════════════════
// Agent control
// ═══════════════════════════════════════════════════════════════════════════════

func (b *UIBridge) handleIsIdle(actions *HostCallbacks) (*CallResultPayload, error) {
	idle := true
	if actions != nil && actions.IsIdle != nil {
		idle = actions.IsIdle()
	}
	result, _ := json.Marshal(map[string]any{"idle": idle})
	return &CallResultPayload{Result: result}, nil
}

// handleIsProjectTrusted answers the same question the state push carries, so
// SDKs without a replicated state cache can ask directly. Unbound defaults to
// trusted, matching the upstream runner (runner.ts:280).
func (b *UIBridge) handleIsProjectTrusted(actions *HostCallbacks) (*CallResultPayload, error) {
	trusted := true
	if actions != nil && actions.IsProjectTrusted != nil {
		trusted = actions.IsProjectTrusted()
	}
	result, _ := json.Marshal(map[string]any{"trusted": trusted})
	return &CallResultPayload{Result: result}, nil
}

func (b *UIBridge) handleAbort(actions *HostCallbacks) (*CallResultPayload, error) {
	if actions != nil && actions.Abort != nil {
		actions.Abort()
	}
	return &CallResultPayload{}, nil
}

func (b *UIBridge) handleHasPendingMessages(actions *HostCallbacks) (*CallResultPayload, error) {
	pending := false
	if actions != nil && actions.HasPendingMessages != nil {
		pending = actions.HasPendingMessages()
	}
	result, _ := json.Marshal(map[string]any{"pending": pending})
	return &CallResultPayload{Result: result}, nil
}

func (b *UIBridge) handleShutdown(actions *HostCallbacks) (*CallResultPayload, error) {
	if actions != nil && actions.Shutdown != nil {
		actions.Shutdown()
	}
	return &CallResultPayload{}, nil
}

func (b *UIBridge) handleCompact(ctx context.Context, actions *HostCallbacks, args json.RawMessage) (*CallResultPayload, error) {
	var opts extension.CompactOptions
	var mode struct {
		// AwaitCompletion asks for the call to answer when compaction
		// finishes, with the result or the error: the SDK runs upstream's
		// onComplete/onError from that answer.
		AwaitCompletion bool `json:"awaitCompletion"`
	}
	if len(args) > 0 {
		_ = json.Unmarshal(args, &opts)
		_ = json.Unmarshal(args, &mode)
	}
	if actions == nil || actions.Compact == nil {
		extension.CallInitiated(ctx)
		if mode.AwaitCompletion {
			return &CallResultPayload{Error: &ErrorInfo{Message: "compaction is not available"}}, nil
		}
		return &CallResultPayload{}, nil
	}
	if !mode.AwaitCompletion {
		actions.Compact(ctx, &opts)
		return &CallResultPayload{}, nil
	}
	type outcome struct {
		result extension.CompactionResult
		err    error
	}
	// Upstream settles once: the first outcome answers the call.
	done := make(chan outcome, 1)
	var settle sync.Once
	opts.OnComplete = func(result extension.CompactionResult) {
		settle.Do(func() { done <- outcome{result: result} })
	}
	opts.OnError = func(err error) {
		settle.Do(func() { done <- outcome{err: err} })
	}
	actions.Compact(ctx, &opts)
	select {
	case out := <-done:
		if out.err != nil {
			return &CallResultPayload{Error: &ErrorInfo{Message: out.err.Error()}}, nil
		}
		raw, err := json.Marshal(out.result)
		if err != nil {
			return nil, err
		}
		return &CallResultPayload{Result: raw}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// ═══════════════════════════════════════════════════════════════════════════════
// Command-only session control
// ═══════════════════════════════════════════════════════════════════════════════

func (b *UIBridge) handleWaitForIdle(ctx context.Context, actions *HostCallbacks) (*CallResultPayload, error) {
	if actions == nil || actions.WaitForIdle == nil {
		extension.CallInitiated(ctx)
		return &CallResultPayload{}, nil
	}
	if err := actions.WaitForIdle(ctx); err != nil {
		return &CallResultPayload{Error: &ErrorInfo{Code: "wait_error", Message: err.Error()}}, nil
	}
	return &CallResultPayload{}, nil
}

func (b *UIBridge) handleNewSession(ctx context.Context, actions *HostCallbacks, args json.RawMessage) (*CallResultPayload, error) {
	if actions == nil || actions.NewSession == nil {
		extension.CallInitiated(ctx)
		return &CallResultPayload{Error: &ErrorInfo{Code: "unsupported", Message: "newSession not available"}}, nil
	}
	var opts extension.NewSessionOptions
	if len(args) > 0 {
		_ = json.Unmarshal(args, &opts)
	}
	res, err := actions.NewSession(ctx, &opts)
	if err != nil {
		return &CallResultPayload{Error: &ErrorInfo{Code: "session_error", Message: err.Error()}}, nil
	}
	result, _ := json.Marshal(res)
	return &CallResultPayload{Result: result}, nil
}

func (b *UIBridge) handleFork(ctx context.Context, actions *HostCallbacks, args json.RawMessage) (*CallResultPayload, error) {
	if actions == nil || actions.Fork == nil {
		extension.CallInitiated(ctx)
		return &CallResultPayload{Error: &ErrorInfo{Code: "unsupported", Message: "fork not available"}}, nil
	}
	var p struct {
		EntryID  string `json:"entryId"`
		Position string `json:"position,omitempty"`
	}
	if len(args) > 0 {
		_ = json.Unmarshal(args, &p)
	}
	opts := &extension.ForkOptions{Position: p.Position}
	res, err := actions.Fork(ctx, p.EntryID, opts)
	if err != nil {
		return &CallResultPayload{Error: &ErrorInfo{Code: "fork_error", Message: err.Error()}}, nil
	}
	result, _ := json.Marshal(res)
	return &CallResultPayload{Result: result}, nil
}

func (b *UIBridge) handleNavigateTree(ctx context.Context, actions *HostCallbacks, args json.RawMessage) (*CallResultPayload, error) {
	if actions == nil || actions.NavigateTree == nil {
		extension.CallInitiated(ctx)
		return &CallResultPayload{Error: &ErrorInfo{Code: "unsupported", Message: "navigateTree not available"}}, nil
	}
	var p struct {
		TargetID            string `json:"targetId"`
		Summarize           bool   `json:"summarize,omitempty"`
		CustomInstructions  string `json:"customInstructions,omitempty"`
		ReplaceInstructions bool   `json:"replaceInstructions,omitempty"`
		Label               string `json:"label,omitempty"`
	}
	if len(args) > 0 {
		_ = json.Unmarshal(args, &p)
	}
	opts := &extension.NavigateTreeOptions{
		Summarize:           p.Summarize,
		CustomInstructions:  p.CustomInstructions,
		ReplaceInstructions: p.ReplaceInstructions,
		Label:               p.Label,
	}
	res, err := actions.NavigateTree(ctx, p.TargetID, opts)
	if err != nil {
		return &CallResultPayload{Error: &ErrorInfo{Code: "navigate_error", Message: err.Error()}}, nil
	}
	result, _ := json.Marshal(res)
	return &CallResultPayload{Result: result}, nil
}

func (b *UIBridge) handleSwitchSession(ctx context.Context, actions *HostCallbacks, args json.RawMessage) (*CallResultPayload, error) {
	if actions == nil || actions.SwitchSession == nil {
		extension.CallInitiated(ctx)
		return &CallResultPayload{Error: &ErrorInfo{Code: "unsupported", Message: "switchSession not available"}}, nil
	}
	var p struct {
		SessionPath string `json:"sessionPath"`
	}
	if len(args) > 0 {
		_ = json.Unmarshal(args, &p)
	}
	res, err := actions.SwitchSession(ctx, p.SessionPath, &extension.SwitchSessionOptions{})
	if err != nil {
		return &CallResultPayload{Error: &ErrorInfo{Code: "session_error", Message: err.Error()}}, nil
	}
	result, _ := json.Marshal(res)
	return &CallResultPayload{Result: result}, nil
}

func (b *UIBridge) handleReload(ctx context.Context, actions *HostCallbacks) (*CallResultPayload, error) {
	if actions == nil || actions.Reload == nil {
		extension.CallInitiated(ctx)
		return &CallResultPayload{Error: &ErrorInfo{Code: "unsupported", Message: "reload not available"}}, nil
	}
	if err := actions.Reload(ctx); err != nil {
		return &CallResultPayload{Error: &ErrorInfo{Code: "reload_error", Message: err.Error()}}, nil
	}
	return &CallResultPayload{}, nil
}

func (b *UIBridge) handleSetStatus(args json.RawMessage) (*CallResultPayload, error) {
	var p struct {
		Key  string `json:"key"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return nil, fmt.Errorf("parse setStatus args: %w", err)
	}
	b.mu.Lock()
	b.pendingStatuses[p.Key] = p.Text
	ui := b.uiCtx
	onStateChanged := b.OnStateChanged
	b.mu.Unlock()
	ui.SetStatus(p.Key, p.Text)
	// Push the updated status to every connected extension so a footer or
	// header factory reading footerData.getExtensionStatuses() sees it on
	// its next render, matching upstream's synchronous requestRender.
	if onStateChanged != nil {
		onStateChanged()
	}
	return &CallResultPayload{}, nil
}

func (b *UIBridge) handleSetWorkingIndicator(ui extension.UIContext, args json.RawMessage) (*CallResultPayload, error) {
	// WorkingIndicatorOptions is an opaque (`any`) type in the extension
	// package, so decode the raw JSON into a map and forward it.
	var opts map[string]any
	if err := json.Unmarshal(args, &opts); err != nil {
		return nil, fmt.Errorf("parse setWorkingIndicator args: %w", err)
	}
	ui.SetWorkingIndicator(opts)
	return &CallResultPayload{}, nil
}

func (b *UIBridge) handleSetWorkingMessage(ui extension.UIContext, args json.RawMessage) (*CallResultPayload, error) {
	var p struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return nil, fmt.Errorf("parse setWorkingMessage args: %w", err)
	}
	ui.SetWorkingMessage(p.Message)
	return &CallResultPayload{}, nil
}

func (b *UIBridge) handleSetWorkingVisible(ui extension.UIContext, args json.RawMessage) (*CallResultPayload, error) {
	var p struct {
		Visible bool `json:"visible"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return nil, fmt.Errorf("parse setWorkingVisible args: %w", err)
	}
	ui.SetWorkingVisible(p.Visible)
	return &CallResultPayload{}, nil
}

func (b *UIBridge) handleSetHiddenThinkingLabel(ui extension.UIContext, args json.RawMessage) (*CallResultPayload, error) {
	var p struct {
		Label string `json:"label"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return nil, fmt.Errorf("parse setHiddenThinkingLabel args: %w", err)
	}
	ui.SetHiddenThinkingLabel(p.Label)
	return &CallResultPayload{}, nil
}

// ═══════════════════════════════════════════════════════════════════════════════
// Category 2: User Interaction (blocking)
// ═══════════════════════════════════════════════════════════════════════════════

func (b *UIBridge) handleSelect(ctx context.Context, ui extension.UIContext, args json.RawMessage) (*CallResultPayload, error) {
	var p struct {
		Title   string                             `json:"title"`
		Options []string                           `json:"options"`
		Opts    extension.ExtensionUIDialogOptions `json:"opts"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return nil, fmt.Errorf("parse select args: %w", err)
	}
	if ui == extension.NoopUIContext {
		result, _ := json.Marshal(map[string]any{"selected": "", "ok": false})
		return &CallResultPayload{Result: result}, nil
	}
	selected, err := ui.Select(ctx, p.Title, p.Options, p.Opts)
	if err != nil {
		if !errors.Is(err, context.Canceled) {
			return &CallResultPayload{Error: &ErrorInfo{Code: "ui_error", Message: err.Error()}}, nil
		}
		result, _ := json.Marshal(map[string]any{"selected": "", "ok": false})
		return &CallResultPayload{Result: result}, nil
	}
	result, _ := json.Marshal(map[string]any{"selected": selected, "ok": true})
	return &CallResultPayload{Result: result}, nil
}

func (b *UIBridge) handleConfirm(ctx context.Context, ui extension.UIContext, args json.RawMessage) (*CallResultPayload, error) {
	var p struct {
		Title   string                             `json:"title"`
		Message string                             `json:"message"`
		Opts    extension.ExtensionUIDialogOptions `json:"opts"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return nil, fmt.Errorf("parse confirm args: %w", err)
	}
	if ui == extension.NoopUIContext {
		result, _ := json.Marshal(map[string]any{"confirmed": false})
		return &CallResultPayload{Result: result}, nil
	}
	confirmed, err := ui.Confirm(ctx, p.Title, p.Message, p.Opts)
	if err != nil {
		if !errors.Is(err, context.Canceled) {
			return &CallResultPayload{Error: &ErrorInfo{Code: "ui_error", Message: err.Error()}}, nil
		}
		result, _ := json.Marshal(map[string]any{"confirmed": false})
		return &CallResultPayload{Result: result}, nil
	}
	result, _ := json.Marshal(map[string]any{"confirmed": confirmed})
	return &CallResultPayload{Result: result}, nil
}

func (b *UIBridge) handleInput(ctx context.Context, ui extension.UIContext, args json.RawMessage) (*CallResultPayload, error) {
	var p struct {
		Title       string                             `json:"title"`
		Placeholder string                             `json:"placeholder"`
		Opts        extension.ExtensionUIDialogOptions `json:"opts"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return nil, fmt.Errorf("parse input args: %w", err)
	}
	if ui == extension.NoopUIContext {
		result, _ := json.Marshal(map[string]any{"text": "", "ok": false})
		return &CallResultPayload{Result: result}, nil
	}
	text, err := ui.Input(ctx, p.Title, p.Placeholder, p.Opts)
	if err != nil {
		if !errors.Is(err, context.Canceled) {
			return &CallResultPayload{Error: &ErrorInfo{Code: "ui_error", Message: err.Error()}}, nil
		}
		result, _ := json.Marshal(map[string]any{"text": "", "ok": false})
		return &CallResultPayload{Result: result}, nil
	}
	result, _ := json.Marshal(map[string]any{"text": text, "ok": true})
	return &CallResultPayload{Result: result}, nil
}

func (b *UIBridge) handleCustom(ctx context.Context, extName string, owner *Conn, ui extension.UIContext, args json.RawMessage) (*CallResultPayload, error) {
	// Subprocess ui.custom: the TS shim runtime sends a unique key
	// plus serialisable overlay options, then streams render frames
	// and a final close event via Node→Go notifications. The bridge
	// opens an overlay on the real UI, registers the handle so the
	// notification path can route into it, and forwards every input
	// chunk back to the extension subprocess.
	var p RemoteOverlayOpenPayload
	if err := json.Unmarshal(args, &p); err != nil {
		return nil, fmt.Errorf("parse custom args: %w", err)
	}
	if p.Key == "" {
		// Subprocess Go/Rust SDK extensions that pass no key are
		// expecting upstream's in-process Custom() semantics: that
		// path requires a live factory closure and is unsupported
		// across a process boundary. The TS shim runtime always
		// generates a key when invoking ui.custom, so this path is
		// purely the SDK back-compat surface.
		return &CallResultPayload{
			Error: &ErrorInfo{
				Code:    "unsupported",
				Message: "ui.custom requires in-process extensions (component factories cannot be serialized)",
			},
		}, nil
	}

	// Pre-register a buffering proxy synchronously so render/close
	// notifications that arrive before the UI-side overlay has been
	// constructed are not dropped. The host reserves this proxy before
	// dispatching the call; direct bridge callers create it here.
	proxy := b.customOverlayProxyFor(extName, owner, p.Key)
	defer b.unregisterCustomOverlay(extName, owner, p.Key)

	// One terminal can give focus to one extension component. Queue competing
	// overlays off the TUI loop and let cancellation remove a waiting call.
	select {
	case <-b.interactiveFocus:
		defer func() { b.interactiveFocus <- struct{}{} }()
	case <-ctx.Done():
		return &CallResultPayload{
			Error: &ErrorInfo{Code: "cancelled", Message: ctx.Err().Error()},
		}, nil
	}
	stopCancel := context.AfterFunc(ctx, func() { proxy.Close(nil) })
	defer stopCancel()

	host := &customOverlayHost{ctx: ctx, bridge: b, extName: extName, owner: owner, key: p.Key}
	value, ok := ui.RunRemoteOverlay(
		extension.RemoteOverlayOptions{
			Title:          p.Title,
			WidthFraction:  p.WidthFraction,
			HeightFraction: p.HeightFraction,
			Overlay:        p.Overlay,
			Layout:         p.OverlayOptions,
		},
		host,
		func(h extension.RemoteOverlayHandle) {
			proxy.SetTarget(h)
			extension.CallInitiated(ctx)
			if p.HasHandle && p.Overlay && owner != nil {
				if err := sendCustomOpened(ctx, owner, p.Key, proxy); err != nil {
					proxy.Close(remoteOverlayError{message: err.Error()})
				}
			}
		},
	)
	if !ok {
		return &CallResultPayload{
			Error: &ErrorInfo{Code: "no_ui", Message: "ui.custom requires an interactive TUI"},
		}, nil
	}
	if overlayErr, ok := value.(remoteOverlayError); ok {
		return &CallResultPayload{
			Error: &ErrorInfo{Code: "ui_error", Message: overlayErr.message},
		}, nil
	}
	result, _ := json.Marshal(map[string]any{"result": value, "ok": true})
	return &CallResultPayload{Result: result}, nil
}

// customOverlayHost forwards every input chunk from the Go-side
// overlay back to the Node-side extension over the bridge connection.
type customOverlayHost struct {
	ctx     context.Context
	bridge  *UIBridge
	extName string
	owner   *Conn
	key     string
}

func (h *customOverlayHost) OnInput(data string) {
	h.bridge.sendCustomInput(h.ctx, h.extName, h.owner, h.key, data)
}

// overlayProxy is the buffering RemoteOverlayHandle registered
// synchronously when handleCustom starts. It absorbs UpdateLines/Close
// calls that arrive before the UI-side overlay constructor binds the
// real handle, then transparently forwards once a target is set.
//
// pig-specific: no upstream equivalent.
type overlayProxy struct {
	mu       sync.Mutex
	target   extension.RemoteOverlayHandle
	buffered []string
	width    int
	hasBuf   bool
	closed   bool
	closeVal any
	lastSeq  uint64
}

func newOverlayProxy() *overlayProxy {
	return &overlayProxy{}
}

func (p *overlayProxy) UpdateLines(lines []string) {
	p.UpdateFrame(lines, 0, 0, 0)
}

func (p *overlayProxy) UpdateFrame(lines []string, width int, seq uint64, currentWidth int) {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	if width > 0 && currentWidth > 0 && width != currentWidth {
		p.mu.Unlock()
		return
	}
	if seq > 0 && seq <= p.lastSeq {
		p.mu.Unlock()
		return
	}
	if seq > 0 {
		p.lastSeq = seq
	}
	if p.target != nil {
		target := p.target
		p.mu.Unlock()
		updateOverlayLines(target, lines, width)
		return
	}
	p.buffered = append([]string(nil), lines...)
	p.width = width
	p.hasBuf = true
	p.mu.Unlock()
}

func (p *overlayProxy) Close(result any) {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	p.closed = true
	p.closeVal = result
	if p.target != nil {
		target := p.target
		p.mu.Unlock()
		target.Close(result)
		return
	}
	p.mu.Unlock()
}

// SetTarget binds the real overlay handle. Any buffered lines or close
// signal collected before binding is drained into the target.
func (p *overlayProxy) SetTarget(target extension.RemoteOverlayHandle) {
	if target == nil {
		return
	}
	p.mu.Lock()
	if p.target != nil {
		p.mu.Unlock()
		target.Close(remoteOverlayError{message: "focused overlay target is already bound"})
		return
	}
	p.target = target
	hasBuf := p.hasBuf
	lines := p.buffered
	width := p.width
	closed := p.closed
	closeVal := p.closeVal
	p.buffered = nil
	p.hasBuf = false
	p.mu.Unlock()
	if hasBuf {
		updateOverlayLines(target, lines, width)
	}
	if closed {
		target.Close(closeVal)
	}
}

// A width-aware target checks the terminal geometry again at paint time, since
// a cached frame can become stale after the bridge accepts it.
func updateOverlayLines(target extension.RemoteOverlayHandle, lines []string, width int) {
	if framed, ok := target.(interface{ UpdateLinesAt([]string, int) }); ok {
		framed.UpdateLinesAt(lines, width)
		return
	}
	target.UpdateLines(lines)
}

func (b *UIBridge) handleEditor(ctx context.Context, ui extension.UIContext, args json.RawMessage) (*CallResultPayload, error) {
	var p struct {
		Title   string `json:"title"`
		Prefill string `json:"prefill"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return nil, fmt.Errorf("parse editor args: %w", err)
	}
	if ui == extension.NoopUIContext {
		result, _ := json.Marshal(map[string]any{"text": "", "ok": false})
		return &CallResultPayload{Result: result}, nil
	}
	text, err := ui.Editor(ctx, p.Title, p.Prefill)
	if err != nil {
		if !errors.Is(err, context.Canceled) {
			return &CallResultPayload{Error: &ErrorInfo{Code: "ui_error", Message: err.Error()}}, nil
		}
		result, _ := json.Marshal(map[string]any{"text": "", "ok": false})
		return &CallResultPayload{Result: result}, nil
	}
	result, _ := json.Marshal(map[string]any{"text": text, "ok": true})
	return &CallResultPayload{Result: result}, nil
}

// ═══════════════════════════════════════════════════════════════════════════════
// Category 3: Component Factories
// ═══════════════════════════════════════════════════════════════════════════════

func (b *UIBridge) handleSetFooter(args json.RawMessage) (*CallResultPayload, error) {
	b.footerMu.Lock()
	defer b.footerMu.Unlock()
	var p struct {
		Clear bool     `json:"clear"`
		Lines []string `json:"lines"`
		Width int      `json:"width"`
	}
	_ = json.Unmarshal(args, &p)
	cleared := p.Clear || len(args) == 0 || string(args) == "null"
	var factory any
	if !cleared {
		factory = framedLines(p.Lines, p.Width)
	}
	b.mu.Lock()
	b.pendingFooterSet = true
	if cleared {
		b.pendingFooter = nil
	} else {
		b.pendingFooter = append([]string(nil), p.Lines...)
		b.pendingFooterW = p.Width
	}
	ui := b.uiCtx
	b.mu.Unlock()
	ui.SetFooter(factory)
	return &CallResultPayload{}, nil
}

func (b *UIBridge) handleSetHeader(args json.RawMessage) (*CallResultPayload, error) {
	b.headerMu.Lock()
	defer b.headerMu.Unlock()
	var p struct {
		Clear bool     `json:"clear"`
		Lines []string `json:"lines"`
		Width int      `json:"width"`
	}
	_ = json.Unmarshal(args, &p)
	cleared := p.Clear || len(args) == 0 || string(args) == "null"
	var factory any
	if !cleared {
		factory = framedLines(p.Lines, p.Width)
	}
	b.mu.Lock()
	b.pendingHeaderSet = true
	b.pendingLogin = nil
	if cleared {
		b.pendingHeader = nil
	} else {
		b.pendingHeader = append([]string(nil), p.Lines...)
		b.pendingHeaderW = p.Width
	}
	ui := b.uiCtx
	b.mu.Unlock()
	ui.SetHeader(factory)
	return &CallResultPayload{}, nil
}

// pig additive (D60): apply the typed native login call while preserving the
// shared header slot and the latest pending operation before UI binding.
func (b *UIBridge) handleSetLogin(args json.RawMessage) (*CallResultPayload, error) {
	if _, err := extension.DecodeLoginDefinitionJSON(args); err != nil {
		return &CallResultPayload{Error: &ErrorInfo{Code: "invalid_login", Message: err.Error()}}, nil
	}
	var definition extension.LoginDefinition
	if err := json.Unmarshal(args, &definition); err != nil {
		return &CallResultPayload{Error: &ErrorInfo{Code: "invalid_login", Message: "decode login definition: " + err.Error()}}, nil
	}
	definition = *cloneLoginDefinition(&definition)

	b.headerMu.Lock()
	defer b.headerMu.Unlock()
	b.mu.RLock()
	ui, ready := b.uiCtx, b.uiReady
	b.mu.RUnlock()
	if ready {
		if err := ui.SetLogin(definition); err != nil {
			// A rejected login installs no replacement and leaves the header slot as it was.
			return &CallResultPayload{Error: &ErrorInfo{Code: "ui_error", Message: "set login: " + err.Error()}}, nil
		}
	}

	b.mu.Lock()
	b.pendingHeaderSet = false
	b.pendingHeader = nil
	if ready {
		b.pendingLogin = nil
	} else {
		b.pendingLogin = cloneLoginDefinition(&definition)
	}
	b.mu.Unlock()
	return &CallResultPayload{}, nil
}

func cloneLoginDefinition(definition *extension.LoginDefinition) *extension.LoginDefinition {
	if definition == nil {
		return nil
	}
	clone := *definition
	clone.Brand = append([]string(nil), definition.Brand...)
	clone.Hero = append([]string(nil), definition.Hero...)
	clone.Mascot = append([]string(nil), definition.Mascot...)
	clone.Palette = maps.Clone(definition.Palette)
	return &clone
}

func (b *UIBridge) handleSetTitle(ui extension.UIContext, args json.RawMessage) (*CallResultPayload, error) {
	var p struct {
		Title string `json:"title"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return nil, fmt.Errorf("parse setTitle args: %w", err)
	}
	ui.SetTitle(p.Title)
	return &CallResultPayload{}, nil
}

func (b *UIBridge) handleSetEditorComponent(ui extension.UIContext, args json.RawMessage) (*CallResultPayload, error) {
	// An SDK extension's setEditorComponent(undefined): restore the host's
	// editor, whichever extension installed the current one. A Node
	// extension's editor component runs in its own process and installs
	// through the ui.editor.* notifies (editor_proxy.go).
	var p struct {
		Clear bool `json:"clear"`
	}
	_ = json.Unmarshal(args, &p)
	if !p.Clear {
		return &CallResultPayload{}, nil
	}
	b.mu.Lock()
	current := b.editor
	b.editor = nil
	b.mu.Unlock()
	if current != nil {
		current.Close()
	}
	ui.SetEditorComponent(nil)
	return &CallResultPayload{}, nil
}

// ═══════════════════════════════════════════════════════════════════════════════
// Category 4: Editor Access
// ═══════════════════════════════════════════════════════════════════════════════

func (b *UIBridge) handlePasteToEditor(ui extension.UIContext, args json.RawMessage) (*CallResultPayload, error) {
	var p struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return nil, fmt.Errorf("parse pasteToEditor args: %w", err)
	}
	ui.PasteToEditor(p.Text)
	return &CallResultPayload{}, nil
}

func (b *UIBridge) handleSetEditorText(ui extension.UIContext, args json.RawMessage) (*CallResultPayload, error) {
	var p struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return nil, fmt.Errorf("parse setEditorText args: %w", err)
	}
	ui.SetEditorText(p.Text)
	return &CallResultPayload{}, nil
}

func (b *UIBridge) handleGetEditorText(ui extension.UIContext) (*CallResultPayload, error) {
	text := ui.GetEditorText()
	result, _ := json.Marshal(map[string]any{"text": text})
	return &CallResultPayload{Result: result}, nil
}

func (b *UIBridge) handleGetEditorComponent(ui extension.UIContext) (*CallResultPayload, error) {
	result, _ := json.Marshal(map[string]any{"component": ui.GetEditorComponent()})
	return &CallResultPayload{Result: result}, nil
}

// ═══════════════════════════════════════════════════════════════════════════════
// Category 5: Theme
// ═══════════════════════════════════════════════════════════════════════════════

func (b *UIBridge) handleTheme(ui extension.UIContext) (*CallResultPayload, error) {
	theme := ui.Theme()
	if ui == extension.NoopUIContext {
		// Upstream runner.ts:noOpUIContext still exposes the active theme.
		b.mu.RLock()
		activeTheme := b.theme
		b.mu.RUnlock()
		if activeTheme != nil {
			theme = activeTheme()
		}
	}
	result, err := json.Marshal(map[string]any{"theme": theme})
	if err != nil {
		return nil, fmt.Errorf("serialize theme: %w", err)
	}
	return &CallResultPayload{Result: result}, nil
}

func (b *UIBridge) handleGetAllThemes(ui extension.UIContext) (*CallResultPayload, error) {
	themes := ui.GetAllThemes()
	if themes == nil {
		themes = []extension.ThemeMeta{}
	}
	result, _ := json.Marshal(map[string]any{"themes": themes})
	return &CallResultPayload{Result: result}, nil
}

func (b *UIBridge) handleGetTheme(ui extension.UIContext, args json.RawMessage) (*CallResultPayload, error) {
	var p struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return nil, fmt.Errorf("parse getTheme args: %w", err)
	}
	theme, err := ui.GetTheme(p.Name)
	if err != nil {
		return &CallResultPayload{
			Error: &ErrorInfo{Code: "not_found", Message: err.Error()},
		}, nil
	}
	result, err := json.Marshal(map[string]any{"theme": theme})
	if err != nil {
		return nil, fmt.Errorf("serialize theme %q: %w", p.Name, err)
	}
	return &CallResultPayload{Result: result}, nil
}

func (b *UIBridge) handleSetTheme(ui extension.UIContext, args json.RawMessage) (*CallResultPayload, error) {
	var p struct {
		Theme string `json:"theme"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return nil, fmt.Errorf("parse setTheme args: %w", err)
	}
	res := ui.SetTheme(p.Theme)
	result, _ := json.Marshal(res)
	return &CallResultPayload{Result: result}, nil
}

// ═══════════════════════════════════════════════════════════════════════════════
// Category 6: Tool expansion
// ═══════════════════════════════════════════════════════════════════════════════

func (b *UIBridge) handleGetToolsExpanded(ui extension.UIContext) (*CallResultPayload, error) {
	expanded := ui.GetToolsExpanded()
	result, _ := json.Marshal(map[string]any{"expanded": expanded})
	return &CallResultPayload{Result: result}, nil
}

func (b *UIBridge) handleSetToolsExpanded(ui extension.UIContext, args json.RawMessage) (*CallResultPayload, error) {
	var p struct {
		Expanded bool `json:"expanded"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return nil, fmt.Errorf("parse setToolsExpanded args: %w", err)
	}
	ui.SetToolsExpanded(p.Expanded)
	return &CallResultPayload{}, nil
}

// ═══════════════════════════════════════════════════════════════════════════════
// Category 7: Advanced UI (streaming/callback: protocol stubs)
// ═══════════════════════════════════════════════════════════════════════════════

// handleOnTerminalInput subscribes a subprocess extension to raw terminal
// input through the UI's remote listener registration.
//
// Upstream's handler is synchronous: input waits for its verdict before it is
// handled, and the verdict's `data` replaces the chunk. The registered handler
// therefore asks the extension and waits with no deadline, like upstream's
// awaited listener; the UI calls it off its input loop. An extension whose
// dispatcher stops answering fails its D56 heartbeat, which ends the wait and
// reports the extension unresponsive; the chunk is then delivered unchanged.
func (b *UIBridge) handleOnTerminalInput(extName string, _ json.RawMessage) (*CallResultPayload, error) {
	b.mu.Lock()
	ui := b.uiCtx
	conn := b.extConns[extName]
	b.mu.Unlock()
	if ui == nil || conn == nil {
		return &CallResultPayload{Error: &ErrorInfo{
			Code:    "unsupported",
			Message: "onTerminalInput requires an interactive UI and a connected extension",
		}}, nil
	}

	unsubscribe := ui.OnRemoteTerminalInput(extName, func(ctx context.Context, data string) extension.TerminalInputResult {
		state, ok := ctx.Value(terminalInputStateKey{}).(terminalInputState)
		if !ok {
			state = terminalInputState{editorText: ui.GetEditorText(), toolsExpanded: ui.GetToolsExpanded()}
		}
		args, err := json.Marshal(TerminalInputArgs{Data: data, EditorText: state.editorText, ToolsExpanded: state.toolsExpanded})
		if err != nil {
			return extension.TerminalInputResult{}
		}
		resp, err := conn.Request(ctx, &Envelope{
			Type:    MsgRequest,
			Request: &RequestPayload{Method: "terminal_input", Args: args},
		})
		if err != nil || resp.Response == nil || resp.Response.Error != nil || resp.Response.Result == nil {
			return extension.TerminalInputResult{}
		}
		var out struct {
			Consume bool    `json:"consume"`
			Data    *string `json:"data"`
		}
		if err := json.Unmarshal(resp.Response.Result, &out); err != nil {
			return extension.TerminalInputResult{}
		}
		return extension.TerminalInputResult{Consume: out.Consume, Data: out.Data}
	})

	var once sync.Once
	retire := func() { once.Do(unsubscribe) }
	b.mu.Lock()
	if b.terminalInputSubs == nil {
		b.terminalInputSubs = map[string]func(){}
	}
	// A restarted extension subscribes again on its new connection; the
	// listener bound to the old connection leaves the input path.
	previous := b.terminalInputSubs[extName]
	b.terminalInputSubs[extName] = retire
	b.mu.Unlock()
	if previous != nil {
		previous()
	}

	return &CallResultPayload{}, nil
}

// handleOffTerminalInput releases the extension's raw-input subscription.
func (b *UIBridge) handleOffTerminalInput(extName string, _ json.RawMessage) (*CallResultPayload, error) {
	b.mu.Lock()
	retire := b.terminalInputSubs[extName]
	delete(b.terminalInputSubs, extName)
	b.mu.Unlock()
	if retire != nil {
		retire()
	}
	return &CallResultPayload{}, nil
}

// ═══════════════════════════════════════════════════════════════════════════════
// Category 8: Agent Actions (ExtensionActions)
// ═══════════════════════════════════════════════════════════════════════════════

func (b *UIBridge) handleSendMessage(actions *HostCallbacks, args json.RawMessage) (*CallResultPayload, error) {
	var p struct {
		Message extension.CustomMessageRef `json:"message"`
		Options SendMessageOptions         `json:"options"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return nil, fmt.Errorf("parse sendMessage args: %w", err)
	}
	if actions == nil || actions.SendMessage == nil {
		return &CallResultPayload{
			Error: &ErrorInfo{Code: "not_ready", Message: "agent session not initialized"},
		}, nil
	}
	if err := actions.SendMessage(p.Message, p.Options); err != nil {
		return &CallResultPayload{
			Error: &ErrorInfo{Code: "send_failed", Message: err.Error()},
		}, nil
	}
	return &CallResultPayload{}, nil
}

func (b *UIBridge) handleSendUserMessage(actions *HostCallbacks, args json.RawMessage) (*CallResultPayload, error) {
	var p struct {
		Content any                    `json:"content"`
		Options SendUserMessageOptions `json:"options"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return nil, fmt.Errorf("parse sendUserMessage args: %w", err)
	}
	if actions == nil || actions.SendUserMessage == nil {
		return &CallResultPayload{
			Error: &ErrorInfo{Code: "not_ready", Message: "agent session not initialized"},
		}, nil
	}
	if err := actions.SendUserMessage(p.Content, p.Options); err != nil {
		return &CallResultPayload{
			Error: &ErrorInfo{Code: "send_failed", Message: err.Error()},
		}, nil
	}
	return &CallResultPayload{}, nil
}

// DirectEntryAppend is a custom entry ctx.sessionManager.appendCustomEntry
// appended in the extension process: upstream's SessionManager returns the
// entry's id synchronously, so the extension generates the id and timestamp
// and the host writes the entry with them.
type DirectEntryAppend struct {
	ID        string `json:"id"`
	Timestamp string `json:"timestamp"`
}

func (b *UIBridge) handleAppendEntry(actions *HostCallbacks, args json.RawMessage) (*CallResultPayload, error) {
	var p struct {
		CustomType string             `json:"customType"`
		Data       any                `json:"data"`
		Direct     *DirectEntryAppend `json:"direct"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return nil, fmt.Errorf("parse appendEntry args: %w", err)
	}
	if actions == nil || actions.AppendEntry == nil {
		return &CallResultPayload{}, nil
	}
	if err := actions.AppendEntry(p.CustomType, p.Data, p.Direct); err != nil {
		return &CallResultPayload{
			Error: &ErrorInfo{Code: "append_failed", Message: err.Error()},
		}, nil
	}
	return &CallResultPayload{}, nil
}

func (b *UIBridge) handleExec(ctx context.Context, actions *HostCallbacks, args json.RawMessage) (*CallResultPayload, error) {
	var p struct {
		Command string                 `json:"command"`
		Args    []string               `json:"args"`
		Options *extension.ExecOptions `json:"options,omitempty"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return nil, fmt.Errorf("parse exec args: %w", err)
	}
	if actions == nil || (actions.ExecContext == nil && actions.Exec == nil) {
		extension.CallInitiated(ctx)
		result, _ := json.Marshal(extension.ExecResult{Code: 127, Stderr: "exec not available"})
		return &CallResultPayload{Result: result}, nil
	}
	var (
		res extension.ExecResult
		err error
	)
	if actions.ExecContext != nil {
		res, err = actions.ExecContext(ctx, p.Command, p.Args, p.Options)
	} else {
		res, err = actions.Exec(p.Command, p.Args, p.Options)
		// A legacy callback has no initiation reporter. Its return is the first
		// point at which the bridge knows the callback actually ran.
		extension.CallInitiated(ctx)
	}
	if err != nil {
		return nil, err
	}
	result, _ := json.Marshal(res)
	return &CallResultPayload{Result: result}, nil
}

func (b *UIBridge) flagValue(actions *HostCallbacks, extName, name string) any {
	var value any
	if actions != nil && actions.GetFlag != nil {
		value = actions.GetFlag(extName, name)
	}
	if b.resolveFlag != nil {
		value = b.resolveFlag(extName, name, value)
	}
	return value
}

func (b *UIBridge) handleGetFlag(extName string, actions *HostCallbacks, args json.RawMessage) (*CallResultPayload, error) {
	var p struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return nil, fmt.Errorf("parse getFlag args: %w", err)
	}
	value := b.flagValue(actions, extName, p.Name)
	result, _ := json.Marshal(map[string]any{"value": value})
	return &CallResultPayload{Result: result}, nil
}

func (b *UIBridge) handleGetSessionName(actions *HostCallbacks) (*CallResultPayload, error) {
	if actions == nil || actions.GetSessionName == nil {
		result, _ := json.Marshal(map[string]any{"name": ""})
		return &CallResultPayload{Result: result}, nil
	}
	name := actions.GetSessionName()
	result, _ := json.Marshal(map[string]any{"name": name})
	return &CallResultPayload{Result: result}, nil
}

func (b *UIBridge) handleGetSessionID(actions *HostCallbacks) (*CallResultPayload, error) {
	id := ""
	if actions != nil && actions.GetSessionID != nil {
		id = actions.GetSessionID()
	}
	result, _ := json.Marshal(map[string]any{"sessionId": id})
	return &CallResultPayload{Result: result}, nil
}

func (b *UIBridge) handleGetSessionFile(actions *HostCallbacks) (*CallResultPayload, error) {
	file := ""
	if actions != nil && actions.GetSessionFile != nil {
		file = actions.GetSessionFile()
	}
	result, _ := json.Marshal(map[string]any{"sessionFile": file})
	return &CallResultPayload{Result: result}, nil
}

func (b *UIBridge) handleGetLeafID(actions *HostCallbacks) (*CallResultPayload, error) {
	leafID := ""
	if actions != nil && actions.GetLeafID != nil {
		leafID = actions.GetLeafID()
	}
	result, _ := json.Marshal(map[string]any{"leafId": leafID})
	return &CallResultPayload{Result: result}, nil
}

func (b *UIBridge) handleSetSessionName(actions *HostCallbacks, args json.RawMessage) (*CallResultPayload, error) {
	var p struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return nil, fmt.Errorf("parse setSessionName args: %w", err)
	}
	if actions != nil && actions.SetSessionName != nil {
		if err := actions.SetSessionName(p.Name); err != nil {
			return nil, err
		}
	}
	return &CallResultPayload{}, nil
}

func (b *UIBridge) handleSetLabel(actions *HostCallbacks, args json.RawMessage) (*CallResultPayload, error) {
	var p struct {
		EntryID string `json:"entryId"`
		Label   string `json:"label"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return nil, fmt.Errorf("parse setLabel args: %w", err)
	}
	if actions != nil && actions.SetLabel != nil {
		if err := actions.SetLabel(p.EntryID, p.Label); err != nil {
			return nil, err
		}
	}
	return &CallResultPayload{}, nil
}

func (b *UIBridge) handleGetActiveTools(actions *HostCallbacks) (*CallResultPayload, error) {
	if actions == nil || actions.GetActiveTools == nil {
		result, _ := json.Marshal(map[string]any{"tools": []string{}})
		return &CallResultPayload{Result: result}, nil
	}
	tools := actions.GetActiveTools()
	result, _ := json.Marshal(map[string]any{"tools": tools})
	return &CallResultPayload{Result: result}, nil
}

func (b *UIBridge) handleGetAllTools(actions *HostCallbacks) (*CallResultPayload, error) {
	if actions == nil || actions.GetAllTools == nil {
		result, _ := json.Marshal(map[string]any{"tools": []ToolInfo{}})
		return &CallResultPayload{Result: result}, nil
	}
	tools := actions.GetAllTools()
	type sdkToolInfo struct {
		ToolInfo
		// Deprecated field of the Go, Rust and Python SDK ToolInfo.
		Source string `json:"source,omitempty"`
	}
	out := make([]sdkToolInfo, len(tools))
	for i, tool := range tools {
		out[i] = sdkToolInfo{ToolInfo: tool, Source: tool.Source}
	}
	result, _ := json.Marshal(map[string]any{"tools": out})
	return &CallResultPayload{Result: result}, nil
}

func (b *UIBridge) handleSetActiveTools(actions *HostCallbacks, args json.RawMessage) (*CallResultPayload, error) {
	var p struct {
		Tools []string `json:"tools"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return nil, fmt.Errorf("parse setActiveTools args: %w", err)
	}
	if actions != nil && actions.SetActiveTools != nil {
		actions.SetActiveTools(p.Tools)
	}
	return &CallResultPayload{}, nil
}

func (b *UIBridge) handleRefreshTools(actions *HostCallbacks) (*CallResultPayload, error) {
	if actions != nil && actions.RefreshTools != nil {
		if err := actions.RefreshTools(); err != nil {
			return nil, err
		}
	}
	return &CallResultPayload{}, nil
}

func (b *UIBridge) handleGetCommands(actions *HostCallbacks) (*CallResultPayload, error) {
	if actions == nil || actions.GetCommands == nil {
		result, _ := json.Marshal(map[string]any{"commands": []CommandInfo{}})
		return &CallResultPayload{Result: result}, nil
	}
	commands := actions.GetCommands()
	result, _ := json.Marshal(map[string]any{"commands": commands})
	return &CallResultPayload{Result: result}, nil
}

func (b *UIBridge) handleSetModel(ctx context.Context, actions *HostCallbacks, args json.RawMessage) (*CallResultPayload, error) {
	var p struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return nil, fmt.Errorf("parse setModel args: %w", err)
	}
	if actions == nil || actions.SetModel == nil {
		extension.CallInitiated(ctx)
		result, _ := json.Marshal(map[string]any{"success": false, "error": "not ready"})
		return &CallResultPayload{Result: result}, nil
	}
	ok, err := actions.SetModel(ctx, p.Model)
	if err != nil {
		result, _ := json.Marshal(map[string]any{"success": false, "error": err.Error()})
		return &CallResultPayload{Result: result}, nil
	}
	result, _ := json.Marshal(map[string]any{"success": ok})
	return &CallResultPayload{Result: result}, nil
}

func (b *UIBridge) handleGetThinkingLevel(actions *HostCallbacks) (*CallResultPayload, error) {
	if actions == nil || actions.GetThinkingLevel == nil {
		result, _ := json.Marshal(map[string]any{"level": ""})
		return &CallResultPayload{Result: result}, nil
	}
	level := actions.GetThinkingLevel()
	result, _ := json.Marshal(map[string]any{"level": level})
	return &CallResultPayload{Result: result}, nil
}

func (b *UIBridge) handleSetThinkingLevel(actions *HostCallbacks, args json.RawMessage) (*CallResultPayload, error) {
	var p struct {
		Level string `json:"level"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return nil, fmt.Errorf("parse setThinkingLevel args: %w", err)
	}
	if actions != nil && actions.SetThinkingLevel != nil {
		actions.SetThinkingLevel(p.Level)
	}
	return &CallResultPayload{}, nil
}

func (b *UIBridge) handleGetContextUsage(actions *HostCallbacks) (*CallResultPayload, error) {
	var usage *extension.ContextUsage
	if actions != nil && actions.GetContextUsage != nil {
		usage = actions.GetContextUsage()
	}
	result, _ := json.Marshal(usage)
	return &CallResultPayload{Result: result}, nil
}

func (b *UIBridge) handleGetSystemPrompt(actions *HostCallbacks) (*CallResultPayload, error) {
	if actions == nil || actions.GetSystemPrompt == nil {
		result, _ := json.Marshal(map[string]any{"prompt": ""})
		return &CallResultPayload{Result: result}, nil
	}
	prompt := actions.GetSystemPrompt()
	result, _ := json.Marshal(map[string]any{"prompt": prompt})
	return &CallResultPayload{Result: result}, nil
}

// handleGetSystemPromptOptions returns the base system-prompt inputs as
// the BuildSystemPromptOptions wire shape (same keys as before_agent_start
// event.systemPromptOptions). Defaults to an empty object when unwired.
// upstream: runner.ts:653 (getSystemPromptOptions)
func (b *UIBridge) handleGetSystemPromptOptions(actions *HostCallbacks) (*CallResultPayload, error) {
	if actions == nil || actions.GetSystemPromptOptions == nil {
		result, _ := json.Marshal(extension.BuildSystemPromptOptions{})
		return &CallResultPayload{Result: result}, nil
	}
	result, _ := json.Marshal(actions.GetSystemPromptOptions())
	return &CallResultPayload{Result: result}, nil
}

func (b *UIBridge) handleGetModelInfo(actions *HostCallbacks) (*CallResultPayload, error) {
	if actions == nil || actions.GetModelInfo == nil {
		result, _ := json.Marshal(map[string]any{})
		return &CallResultPayload{Result: result}, nil
	}
	info := actions.GetModelInfo()
	if info == nil {
		result, _ := json.Marshal(map[string]any{})
		return &CallResultPayload{Result: result}, nil
	}
	result, _ := json.Marshal(info)
	return &CallResultPayload{Result: result}, nil
}

func (b *UIBridge) handleGetModel(actions *HostCallbacks, args json.RawMessage) (*CallResultPayload, error) {
	var request struct {
		Provider string `json:"provider"`
		ModelID  string `json:"modelId"`
	}
	if err := json.Unmarshal(args, &request); err != nil {
		return nil, fmt.Errorf("parse getModel args: %w", err)
	}
	if actions == nil || actions.GetModel == nil {
		return &CallResultPayload{Result: json.RawMessage("null")}, nil
	}
	result, err := json.Marshal(actions.GetModel(request.Provider, request.ModelID))
	if err != nil {
		return nil, fmt.Errorf("marshal getModel result: %w", err)
	}
	return &CallResultPayload{Result: result}, nil
}

func (b *UIBridge) handleGetBranch(actions *HostCallbacks) (*CallResultPayload, error) {
	if actions == nil || actions.GetBranch == nil {
		result, _ := json.Marshal(map[string]any{"entries": []any{}})
		return &CallResultPayload{Result: result}, nil
	}
	entries := actions.GetBranch()
	result, _ := json.Marshal(map[string]any{"entries": entries})
	return &CallResultPayload{Result: result}, nil
}

func (b *UIBridge) handleGetEntries(actions *HostCallbacks) (*CallResultPayload, error) {
	if actions == nil || actions.GetEntries == nil {
		result, _ := json.Marshal(map[string]any{"entries": []any{}})
		return &CallResultPayload{Result: result}, nil
	}
	entries := actions.GetEntries()
	result, _ := json.Marshal(map[string]any{"entries": entries})
	return &CallResultPayload{Result: result}, nil
}

func (b *UIBridge) handleGetModelAuth(ctx context.Context, actions *HostCallbacks, args json.RawMessage) (*CallResultPayload, error) {
	var p struct {
		Provider string `json:"provider"`
		ModelID  string `json:"modelId"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return nil, fmt.Errorf("parse getModelAuth args: %w", err)
	}
	if actions == nil || actions.GetModelAuth == nil {
		extension.CallInitiated(ctx)
		result, _ := json.Marshal(map[string]any{"ok": false, "error": "model auth not available"})
		return &CallResultPayload{Result: result}, nil
	}
	result, _ := json.Marshal(actions.GetModelAuth(ctx, p.Provider, p.ModelID))
	return &CallResultPayload{Result: result}, nil
}

func (b *UIBridge) handleGetModelRegistryState() (*CallResultPayload, error) {
	result, err := json.Marshal(b.ModelRegistryState())
	if err != nil {
		return nil, err
	}
	return &CallResultPayload{Result: result}, nil
}

func (b *UIBridge) handleGetProviderAuth(ctx context.Context, actions *HostCallbacks, args json.RawMessage) (*CallResultPayload, error) {
	var p struct {
		Provider string `json:"provider"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return nil, fmt.Errorf("parse getProviderAuth args: %w", err)
	}
	extension.CallInitiated(ctx)
	if actions == nil || actions.GetProviderAuth == nil {
		return &CallResultPayload{Result: json.RawMessage("null")}, nil
	}
	auth, err := actions.GetProviderAuth(ctx, p.Provider)
	if err != nil {
		return nil, err
	}
	result, err := json.Marshal(auth)
	if err != nil {
		return nil, err
	}
	return &CallResultPayload{Result: result}, nil
}

func (b *UIBridge) handleRefreshModelRegistry(ctx context.Context, actions *HostCallbacks, args json.RawMessage) (*CallResultPayload, error) {
	var p struct {
		AllowNetwork *bool    `json:"allowNetwork"`
		Providers    []string `json:"providers"`
		Force        *bool    `json:"force"`
	}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &p); err != nil {
			return nil, fmt.Errorf("parse refreshModelRegistry args: %w", err)
		}
	}
	extension.CallInitiated(ctx)
	refreshed := map[string]any{"aborted": false, "errors": map[string]string{}}
	if actions != nil && actions.RefreshModelRegistry != nil {
		var err error
		if refreshed, err = actions.RefreshModelRegistry(ctx, p.AllowNetwork, p.Providers, p.Force); err != nil {
			return nil, err
		}
	}
	// The caller's synchronous reads after `await refresh()` see the
	// refreshed registry, so the result carries the state it applies.
	refreshed["state"] = b.ModelRegistryState()
	result, err := json.Marshal(refreshed)
	if err != nil {
		return nil, err
	}
	return &CallResultPayload{Result: result}, nil
}

func (b *UIBridge) handleSessionRead(actions *HostCallbacks, args json.RawMessage) (*CallResultPayload, error) {
	var p struct {
		Method string          `json:"method"`
		Args   json.RawMessage `json:"args"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return nil, fmt.Errorf("parse sessionRead args: %w", err)
	}
	if actions == nil || actions.SessionRead == nil {
		return &CallResultPayload{Result: json.RawMessage("null")}, nil
	}
	value, err := actions.SessionRead(p.Method, p.Args)
	if err != nil {
		return nil, err
	}
	result, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return &CallResultPayload{Result: result}, nil
}

func (b *UIBridge) handleComplete(ctx context.Context, actions *HostCallbacks, args json.RawMessage) (*CallResultPayload, error) {
	var p struct {
		Model   map[string]any `json:"model"`
		Request map[string]any `json:"request"`
		Auth    map[string]any `json:"auth"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return nil, fmt.Errorf("parse complete args: %w", err)
	}
	if actions == nil || actions.Complete == nil {
		extension.CallInitiated(ctx)
		result, _ := json.Marshal(map[string]any{"stopReason": "error", "content": []any{}, "error": "complete not available"})
		return &CallResultPayload{Result: result}, nil
	}
	res, err := actions.Complete(ctx, p.Model, p.Request, p.Auth)
	if err != nil {
		return nil, err
	}
	result, _ := json.Marshal(res)
	return &CallResultPayload{Result: result}, nil
}

func decodeModelStreamRequest(raw json.RawMessage) (map[string]any, error) {
	var request map[string]any
	if err := json.Unmarshal(raw, &request); err != nil {
		return nil, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, err
	}
	var rawMessages []json.RawMessage
	if messages, exists := fields["messages"]; exists {
		if err := json.Unmarshal(messages, &rawMessages); err != nil {
			return nil, fmt.Errorf("messages: %w", err)
		}
	}
	messages, _ := request["messages"].([]any)
	for index, rawMessage := range rawMessages {
		if index >= len(messages) {
			break
		}
		var messageFields map[string]json.RawMessage
		if err := json.Unmarshal(rawMessage, &messageFields); err != nil {
			return nil, fmt.Errorf("messages[%d]: %w", index, err)
		}
		var role string
		if err := json.Unmarshal(messageFields["role"], &role); err != nil || role != "system" {
			continue
		}
		rawSections, exists := messageFields["sections"]
		if !exists {
			continue
		}
		var sections ai.OrderedSections
		if err := json.Unmarshal(rawSections, &sections); err != nil {
			return nil, fmt.Errorf("messages[%d].sections: %w", index, err)
		}
		message, ok := messages[index].(map[string]any)
		if !ok {
			return nil, fmt.Errorf("messages[%d] must be an object", index)
		}
		message["sections"] = sections
	}
	return request, nil
}

func (b *UIBridge) handleModelStream(ctx context.Context, owner *Conn, actions *HostCallbacks, args json.RawMessage) (*CallResultPayload, error) {
	var request ModelStreamCall
	if err := json.Unmarshal(args, &request); err != nil {
		return nil, fmt.Errorf("parse modelStream args: %w", err)
	}
	if request.StreamID == "" {
		return nil, errors.New("modelStream requires streamId")
	}
	if owner == nil {
		return nil, errors.New("modelStream requires a live extension connection")
	}
	if actions == nil || actions.StreamModel == nil {
		return nil, errors.New("model streaming is not available")
	}
	decodedRequest, err := decodeModelStreamRequest(request.Request)
	if err != nil {
		return nil, fmt.Errorf("parse modelStream request: %w", err)
	}
	// Cancellation reaches the provider request only; delivery below keeps the
	// call's context so the provider's terminal aborted event still arrives.
	providerCtx, cancel := context.WithCancel(extension.WithProviderStreamSimple(ctx, request.Simple))
	defer cancel()
	providerCtx = extension.WithModelStreamRequest(providerCtx, modelStreamCallbacks(providerCtx, owner, request))
	key := modelStreamKey{owner: owner, streamID: request.StreamID}
	b.modelStreamMu.Lock()
	b.modelStreams[key] = cancel
	b.modelStreamMu.Unlock()
	defer func() {
		b.modelStreamMu.Lock()
		delete(b.modelStreams, key)
		b.modelStreamMu.Unlock()
	}()
	stream, err := actions.StreamModel(providerCtx, request.Model, decodedRequest)
	if err != nil {
		return nil, err
	}
	// Stream creation is the synchronous prefix. Event delivery and final
	// result collection complete independently of later calls in the lane.
	extension.CallInitiated(ctx)
	for event := range stream.Events(ctx) {
		notifyArgs, err := json.Marshal(map[string]any{"streamId": request.StreamID, "event": event})
		if err != nil {
			return nil, fmt.Errorf("marshal model stream event: %w", err)
		}
		if err := owner.sendAndWait(ctx, &Envelope{Type: MsgNotify, Notify: &NotifyPayload{Method: "model_stream_event", Args: notifyArgs}}); err != nil {
			return nil, err
		}
	}
	result, err := json.Marshal(stream.Result())
	if err != nil {
		return nil, fmt.Errorf("marshal model stream result: %w", err)
	}
	return &CallResultPayload{Result: result}, nil
}

type modelStreamKey struct {
	owner    *Conn
	streamID string
}

// handleCancelModelStream cancels the caller's in-flight modelStream call, as
// an aborted upstream options.signal cancels the provider request. The stream
// then ends the way the provider ends a cancelled request. An unknown or
// finished stream is a no-op.
func (b *UIBridge) handleCancelModelStream(owner *Conn, args json.RawMessage) (*CallResultPayload, error) {
	var request struct {
		StreamID string `json:"streamId"`
	}
	if err := json.Unmarshal(args, &request); err != nil {
		return nil, fmt.Errorf("parse cancelModelStream args: %w", err)
	}
	b.modelStreamMu.Lock()
	cancel := b.modelStreams[modelStreamKey{owner: owner, streamID: request.StreamID}]
	b.modelStreamMu.Unlock()
	if cancel != nil {
		cancel()
	}
	return &CallResultPayload{}, nil
}

// ═══════════════════════════════════════════════════════════════════════════════
// Widget management
// ═══════════════════════════════════════════════════════════════════════════════

func (b *UIBridge) handleSetWidget(extName string, args json.RawMessage) (*CallResultPayload, error) {
	var p struct {
		Key     string                           `json:"key"`
		Lines   []string                         `json:"lines"` // legacy/current fast path
		Content []string                         `json:"content"`
		Width   int                              `json:"width"`
		Options extension.ExtensionWidgetOptions `json:"options"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return nil, fmt.Errorf("parse setWidget args: %w", err)
	}
	lines := p.Lines
	if lines == nil {
		lines = p.Content
	}
	b.mu.RLock()
	request := b.widgetRequestFunc
	b.mu.RUnlock()
	if request != nil {
		request(extName, p.Key, lines, p.Options)
		return &CallResultPayload{}, nil
	}

	if lines == nil {
		// Clear the widget.
		key := extName + ":" + p.Key
		b.mu.Lock()
		proxy, ok := b.widgets[key]
		delete(b.widgets, key)
		b.mu.Unlock()
		// Clear outside b.mu: it requests a render through b.Invalidate.
		if ok {
			proxy.Clear()
		}
	} else {
		// A list with no width is a string[] widget: content the host lays out
		// at its own width, as Pi's setExtensionWidget does. A list with a width
		// is a frame a Node component rendered at that width.
		b.HandleWidgetPush(extName, &WidgetPushPayload{Key: p.Key, Lines: lines, Width: p.Width})
	}
	b.notifyWidgetSync()

	return &CallResultPayload{}, nil
}

// handleWatchSessionLog enrolls the calling extension in session-log
// replication and returns the backlog it has not seen. An extension is
// enrolled by its first read of the log, so one that never reads it never pays
// for a copy.
func (b *UIBridge) handleWatchSessionLog(extName string, args json.RawMessage) (*CallResultPayload, error) {
	b.mu.RLock()
	watch := b.WatchSessionLog
	b.mu.RUnlock()
	var request struct {
		Cursor   int  `json:"cursor"`
		Complete bool `json:"complete"`
	}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &request); err != nil {
			return nil, fmt.Errorf("parse watchSessionLog args: %w", err)
		}
	}
	var entries []json.RawMessage
	next := request.Cursor
	more := false
	leafID := ""
	if watch != nil {
		entries, next, more, leafID = watch(extName, request.Cursor, request.Complete)
	}
	result, err := json.Marshal(map[string]any{"entries": entries, "entryCount": next, "hasMore": more, "leafId": leafID})
	if err != nil {
		return nil, fmt.Errorf("marshal session log: %w", err)
	}
	return &CallResultPayload{Result: result}, nil
}

// framedLines passes header/footer lines to the UI context: plain lines when
// the extension reported no render width, otherwise the lines with the width
// they were rendered at, so the host never paints them at another width.
func framedLines(lines []string, width int) any {
	lines = append([]string(nil), lines...)
	if width == 0 {
		return lines
	}
	return extension.WidthLines{Lines: lines, Width: width}
}
