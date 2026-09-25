package codingagent

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/internal/codingagent/llama"
	"github.com/MichaelKinsy/PiG/tui"
)

// ─── Slash Command Registry ───────────────────────────────────────────────────
//
// Mirrors upstream pi's `core/slash-commands.ts` built-in command list
// (`BUILTIN_SLASH_COMMANDS`) and the per-handler dispatch in
// `dist/modes/interactive/slash-command-handlers/`. Every upstream builtin
// is implemented here; none are stubs.
//
// Extensions register dynamic commands through the existing
// `ExtensionAPI.RegisterCommand` path. The registry below merges
// builtins + extension commands at lookup time so `/help` lists both
// and aliases work uniformly.

// SlashCommandSource mirrors upstream's `SlashCommandSource` discriminator.
type SlashCommandSource string

const (
	SlashSourceBuiltin   SlashCommandSource = "builtin"
	SlashSourceExtension SlashCommandSource = "extension"
	SlashSourcePrompt    SlashCommandSource = "prompt" // <agentDir>/prompts/*.md
)

// SlashHandler runs a builtin slash command. Handlers receive the
// SlashContext (which carries args + accessors + output sinks) and may
// return an error. A non-nil error causes the registry's Dispatch to
// surface "Error: <msg>" to the user.
type SlashHandler func(sc *SlashContext) error

// BuiltinSlashCommand is the canonical shape for every command exposed
// to users. Aliases are first-class. (Note: upstream pi does NOT alias
// /exit → /quit; only /quit exists. We match that behavior: no alias.)
type BuiltinSlashCommand struct {
	Name        string
	Aliases     []string
	Description string
	// ArgumentHint is shown after the command in autocomplete (e.g.
	// "<provider/model>"). Optional. Mirrors upstream argumentHint.
	ArgumentHint string
	Handler      SlashHandler
	// Hidden commands dispatch normally but are omitted from /help and
	// autocomplete. Mirrors upstream commands handled inline in the submit
	// handler that are absent from the canonical slash-commands.js list
	// (e.g. /debug).
	Hidden bool
}

// ReloadDiag holds resource counts and diagnostic warnings surfaced by
// /reload. Mirrors the information upstream showLoadedResources collects
// (interactive-mode.ts:1231-1410). Counts are always populated; the
// Diagnostics slice carries one human-readable warning line per conflict
// (commands, shortcuts, extension load errors).
type ReloadDiag struct {
	ContextFiles int
	Skills       int
	Prompts      int
	Extensions   int
	Themes       int
	// Diagnostics is one human-readable line per warning/error gathered
	// during reload. Mirrors upstream's `[Extension issues]`,
	// `[Skill conflicts]`, `[Prompt conflicts]` sections.
	Diagnostics []string
	// Cells is the placement summary produced by the subprocess host. Empty
	// when no subprocess host is active (e.g. headless commands).
	// pig-specific. Used by /reload --explain.
	Cells []ReloadCellDiag
	// ReloadDuration is the wall time spent in subprocess Reload(). Zero when
	// the host is not active.
	ReloadDuration time.Duration
}

// ReloadCellDiag is a UI-safe copy of subprocess.ReloadCellReport. It is
// duplicated here so internal/codingagent can render /reload --explain without
// importing the subprocess host package directly.
type ReloadCellDiag struct {
	Key           string
	Strategy      string
	Language      string
	Extensions    []string
	Hash          string
	BinaryPath    string
	Cached        bool
	BuildDuration time.Duration
	Replaced      bool
	Quarantined   bool
	Reason        string
}

// SlashContext is the per-dispatch context handed to a handler. Accessor
// fields are populated by the host (interactive mode); handlers read
// them and write user-visible output via the `Append` sink. `Quit` is
// the requested-exit signal: only `/exit` and `/quit` set it. `Clear`
// is requested by `/clear` and `/new`.
type SlashContext struct {
	// Raw args after the command name (already trimmed). Empty if user
	// just typed `/foo` with no args.
	Args string

	// Output sinks: must be non-nil when Dispatch is called.
	Append     func(string) // append markdown to chat (Markdown component, 1px padding)
	AppendText func(string) // append plain ANSI text to chat (Text component, 1px padding)
	Clear      func()       // clear the chat transcript
	Quit       func()       // request session exit
	Reset      func()       // /new: keep transcript visible but reset agent messages
	// NewSession creates a fresh session file and resets agent state.
	// Mirrors upstream runtimeHost.newSession(). May be nil in test contexts.
	NewSession func() error
	// FatalRuntimeError records an unrecoverable create/resume/import
	// replacement failure, then requests a status-1 exit after TUI teardown.
	FatalRuntimeError func(prefix string, err error) error

	// ShowStatus appends or updates the status line.
	ShowStatus  func(msg string)
	ShowWarning func(msg string)

	// Accessors. Any of these may be nil; handlers must guard. Keeps
	// the registry decoupled from the full InteractiveMode struct so
	// /help and friends are unit-testable in isolation.
	ModelName       func() string
	ToolNames       func() []string
	RegisteredTools func() []extension.RegisteredTool
	// ShareState returns the system prompt and active tool schemas for the
	// pi.share entry attached to exported transcripts.
	ShareState func() ShareState
	// ShareSession uploads an explicitly requested share artifact. The host
	// captures the command context so cancellation reaches the HTTP request.
	ShareSession  func(session *Session, state ShareState, showStatus func(string)) (string, error)
	SkillNames    func() []string
	SessionInfo   func() (id, dir string, msgCount int)
	LastAssistant func() string
	CopyClipboard func(text string) error
	CostSummary   func() string
	HotkeyLines   func() []string

	// All registered commands: set by Dispatch before calling Handler
	// so /help can enumerate the live set.
	AllCommands []SlashCommandInfo

	// Session accessors. May be nil; handlers must guard.
	CurrentSession func() *Session
	// CacheWarmingStatus reports the Session's cache warmer, or nil when the
	// Session has none.
	CacheWarmingStatus func() *CacheWarmingStatus
	SetSessionName     func(name string) error
	GetSessionName     func() string
	// OnNameChange is called after a successful /name set so callers can
	// update the terminal title and status-line footer.
	OnNameChange func(name string)
	// ForkAtEntry moves the leaf to entryID IN THE SAME FILE (upstream
	// `session.branch`). Used by /tree navigation, not /fork.
	ForkAtEntry func(entryID string) error
	// FlushCompactionQueue delivers messages that were queued while a
	// compaction was running. Tree navigation calls it once the new leaf is in
	// place, mirroring upstream's flushCompactionQueue after "Navigated to
	// selected point" (interactive-mode.ts:1847, 5021), so a message typed
	// during compaction is not left queued by navigating away.
	FlushCompactionQueue func()
	// ForkToNewSession forks the selected user message into a NEW session
	// file (branch copied up to the message's parent), switches to it, and
	// prefills the editor with the message text. Mirrors upstream /fork
	// (agent-session-runtime.ts fork(), position "before").
	ForkToNewSession func(userMsgEntryID string) error
	// WriteDebugLog writes the current frame and message history to a debug
	// log file and returns its path. Backs /debug and the ctrl+shift+d hotkey.
	WriteDebugLog func() (path string, err error)
	CloneCurrent  func() (newPath string, err error)
	ListSessions  func() ([]SessionInfo, error)
	RenderTree    func() string

	// File-based prompt templates loaded from
	// <agentDir>/prompts and <cwd>/.pig/prompts. Read-only snapshot;
	// host populates this each Dispatch.
	PromptTemplates []PromptTemplate

	// Modal selector callbacks. Nil in headless or test
	// contexts; handlers must guard and fall back to text mode.
	PickSession     func() (path string, ok bool)
	PickUserMessage func() (entryID string, ok bool)
	PickTreeEntry   func(initialSelectedID string) (entryID string, ok bool)
	// AppendLabelChange writes a label entry for targetID.
	// label=="" clears any existing label (upstream: undefined→delete).
	AppendLabelChange func(targetID, label string) error
	LoadSessionPath   func(path string) error

	// Manual context compaction.
	// CompactSession triggers a manual compact on the current session.
	// The closure captures the appropriate context internally.
	// nil in headless / test contexts unless explicitly wired.
	CompactSession func(customInstructions string) error

	// /tree summarize-branch flow.
	// ShowExtensionSelector presents a modal string picker with an optional
	// description under the title; returns the chosen option and true, or
	// ("", false) on cancel (Esc). Mirrors upstream showExtensionSelector()
	// and ExtensionSelectorOptions.description.
	ShowExtensionSelector func(title string, options []string, description string) (string, bool)
	// ShowExtensionEditor presents a text prompt with an optional description
	// and prefill; returns the text and true, or ("", false) on cancel (Esc).
	// Mirrors upstream showExtensionEditor() and
	// ExtensionEditorOptions.description.
	ShowExtensionEditor func(title, description, prefill string) (string, bool)
	// BugReportInputs snapshots the process state /bug describes (model,
	// provider, thinking level, extensions, settings). Nil outside the
	// interactive UI.
	BugReportInputs func() (BugReportInputs, error)
	// BugReportProviderName names the current model's provider for the
	// summary consent text. May be nil.
	BugReportProviderName func() string
	// SummarizeForBugReport writes the /bug summary with the session model
	// behind a cancellable loader. aborted reports that the user cancelled.
	// May be nil.
	SummarizeForBugReport func(modelName, hint string) (summary string, aborted bool, err error)
	// UpstreamVersion returns the pinned Pi version. May be nil.
	UpstreamVersion func() string
	// ShowSettingsList presents the dedicated two-column settings selector.
	// Blocks until the user cycles a value (returns id, newValue, true) or
	// cancels (returns "", "", false). Mirrors upstream SettingsSelectorComponent.
	ShowSettingsList func(items []tui.SettingItem) (changedID, changedValue string, ok bool)
	// ShowSelectList presents a non-search submenu selector with optional
	// description column. Used by /settings for upstream-style submenus like
	// thinking level. Returns the chosen value or ("", false) on cancel.
	ShowSelectList func(title, description string, items []tui.SelectItem, currentValue string) (value string, ok bool)
	// ShowThemeSelector presents the theme submenu with live preview.
	// Used by /settings theme to mirror upstream theme preview semantics.
	ShowThemeSelector func(currentTheme string) (themeName string, ok bool)
	// AvailableThinkingLevels returns the currently supported levels for the
	// active model. Used by /settings to build the thinking submenu.
	AvailableThinkingLevels func() []string
	// CurrentThinkingLevel and SelectThinkingLevel back /thinking. May be nil.
	CurrentThinkingLevel func() string
	SelectThinkingLevel  func(level string)
	// ShowThinkingSelector opens the /thinking selector (upstream
	// showThinkingSelector). May be nil; /thinking then falls back to
	// ShowSelectList.
	ShowThinkingSelector func()
	// ModelThinkingSubmenu builds the per-model default override submenu.
	ModelThinkingSubmenu func(string, func(*string)) tui.Component
	// NavigateTreeFull forks to targetID and optionally generates a branch
	// summary. Mirrors upstream AgentSession.navigateTree() with summarize flag.
	NavigateTreeFull func(ctx context.Context, targetID string, summarize bool, customInstructions string) (NavigateTreeResult, error)
	// AbortBranchSummary cancels an in-progress branch summarization.
	AbortBranchSummary func()
	// SetEditorText sets the editor content (e.g. pre-filling user message
	// text when navigating to a user-message entry).
	SetEditorText func(text string)

	// Mid-session model switch.
	SwitchModel           func(spec string) error
	modelSelectionPersist bool
	PickModel             func(initialQuery string) (spec string, ok bool)
	ResolveModel          func(input string) (spec string, ok bool)

	// /scoped-models selector.
	ShowScopedModels func() // opens editor-slot scoped-models list

	// Settings UI.
	// SettingsManager provides read/write access to global settings.
	SettingsManager *SettingsManager

	// Reload triggers a reload of settings and prompt templates.
	// Called by the /reload command. May be nil in headless contexts.
	Reload func()

	// ReloadDiagnostics returns resource counts after a reload.
	// Mirrors upstream showLoadedResources with showDiagnosticsWhenQuiet.
	// May be nil; handler falls back to a static message.
	ReloadDiagnostics func() ReloadDiag

	// OnSettingApplied is called after /settings persists a change.
	// id is the setting identifier (e.g. "hide-thinking"), value is the new value.
	// Interactive mode uses this to apply live state changes (e.g. update
	// m.hideThinking without requiring a restart). May be nil.
	// Mirrors upstream settings-selector.ts callbacks (onHideThinkingChange etc.).
	OnSettingApplied func(id, value string)

	// AgentDir is the pig config dir (typically ~/.pig/agent).
	// Used by /login and /logout for auth.json access.
	AgentDir string

	// Parity harness hooks used only by env-gated test slash commands.
	ProbeClipboardRead     func() (string, error)
	ProbeImageFallback     func() (string, error)
	ProbeCancellableLoader func() (string, error)
	ProbeSelectList        func() (string, error)
	ProbeOAuthShared       func() (string, error)
	ProbeOAuthCallbackPage func() (string, error)
	ProbeCopilotHeaders    func() (string, error)
	ProbeOAuthCopilot      func() (string, error)
	ProbeOAuthCopilotEnv   func() (string, error)
	ProbeOAuthAnthropic    func() (string, error)
	ProbeOAuthCodex        func() (string, error)

	// Login runs the interactive OAuth login flow for the given provider.
	// ShowOAuthSelector opens the OAuth provider picker overlay.
	// mode is "login" or "logout". Returns provider ID and true, or
	// "", false if cancelled. nil in headless/test contexts.
	ShowOAuthSelector func(mode string) (providerID string, ok bool)
	ShowLoginAuthType func() (authType string, ok bool)

	// Mirrors upstream showLoginDialog (interactive-mode.ts:4325-4444).
	// May be nil in headless/test contexts; handlers must guard.
	Login         func(ctx context.Context, provider string) error
	SetAPIKey     func(provider, value string) error
	ShowTextInput func(title, placeholder string) (string, bool)
	// LoginAPIKeyProvider runs a provider's own api-key login flow and
	// reports whether the provider has one (llama.cpp prompts for its server
	// URL and optional key instead of a bare key).
	LoginAPIKeyProvider func(provider string) bool
	// RunLlama runs the built-in /llama command.
	RunLlama func() error

	// Logout removes stored credentials for the given provider.
	// Mirrors upstream showOAuthSelector logout branch (interactive-mode.ts:4299).
	// May be nil in headless/test contexts; handlers must guard.
	Logout             func(provider string) error
	LogoutProviderName func(provider string) string

	// ExtRunner is the extension runner for emitting events from slash commands.
	// May be nil in headless/test contexts; handlers must guard.
	ExtRunner *inproc.Runner

	// Skills is the loaded set of skill definitions for /skill listing.
	Skills []*SkillDef
}

// SessionTokenStats holds token usage for the /session display.
// Mirrors upstream SessionStats.tokens (agent-session.ts:2913-2918).
type SessionTokenStats struct {
	Input      int
	Output     int
	CacheRead  int
	CacheWrite int
	Total      int
	Cost       float64 // USD; only shown if > 0
}

// NavigateTreeResult is the TUI-facing result of a tree navigation operation.
// Re-exported here (from coding.NavigateTreeResult) so slash handlers don't
// need to import the public SDK package (cycle prevention).
type NavigateTreeResult struct {
	EditorText string
	Cancelled  bool
	Aborted    bool
}

// SlashCommandInfo is the user-facing summary used by `/help`.
type SlashCommandInfo struct {
	Name        string
	Aliases     []string
	Description string
	Source      SlashCommandSource
}

// SlashRegistry holds builtins + extension-registered dynamic commands
// and resolves aliases. Concurrent-safe.
type SlashRegistry struct {
	mu       sync.RWMutex
	builtins map[string]*BuiltinSlashCommand
	aliases  map[string]string       // alias → canonical name
	dynamic  map[string]SlashCommand // extension-registered
}

// NewSlashRegistry seeds a registry with the built-in commands.
func NewSlashRegistry() *SlashRegistry {
	r := &SlashRegistry{
		builtins: make(map[string]*BuiltinSlashCommand),
		aliases:  make(map[string]string),
		dynamic:  make(map[string]SlashCommand),
	}
	for _, c := range defaultBuiltins() {
		r.Register(c)
	}
	return r
}

// Register adds (or replaces) a builtin and its aliases. Used both
// internally for the seed list and externally if a host wants to inject
// extra builtins (tests do this).
func (r *SlashRegistry) Register(cmd BuiltinSlashCommand) {
	r.mu.Lock()
	defer r.mu.Unlock()
	c := cmd
	r.builtins[cmd.Name] = &c
	for _, a := range cmd.Aliases {
		r.aliases[a] = cmd.Name
	}
}

// ReplaceDynamic replaces every extension-registered slash command with cmds,
// so a command whose extension is no longer loaded stops resolving.
func (r *SlashRegistry) ReplaceDynamic(cmds []SlashCommand) {
	dynamic := make(map[string]SlashCommand, len(cmds))
	for _, cmd := range cmds {
		cmd.Name = strings.TrimPrefix(cmd.Name, "/")
		dynamic[cmd.Name] = cmd
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.dynamic = dynamic
}

// Resolve returns the canonical name for a typed-in command, walking
// aliases. Returns ("", false) if neither builtin nor extension command
// matches.
func (r *SlashRegistry) Resolve(name string) (string, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if _, ok := r.builtins[name]; ok {
		return name, true
	}
	if canon, ok := r.aliases[name]; ok {
		return canon, true
	}
	if _, ok := r.dynamic[name]; ok {
		return name, true
	}
	return "", false
}

// All returns a sorted list of every command (builtin + extension) for
// /help rendering.
func (r *SlashRegistry) All() []SlashCommandInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]SlashCommandInfo, 0, len(r.builtins)+len(r.dynamic))
	for _, b := range r.builtins {
		if b.Hidden {
			continue
		}
		out = append(out, SlashCommandInfo{
			Name:        b.Name,
			Aliases:     append([]string(nil), b.Aliases...),
			Description: b.Description,
			Source:      SlashSourceBuiltin,
		})
	}
	for _, d := range r.dynamic {
		out = append(out, SlashCommandInfo{
			Name:        d.Name,
			Description: d.Description,
			Source:      SlashSourceExtension,
		})
	}
	slices.SortFunc(out, func(a, b SlashCommandInfo) int { return cmp.Compare(a.Name, b.Name) })
	return out
}

// ErrUnknownSlashCommand signals that a slash command was not found in the
// registry. The caller should pass the original text to the LLM as a regular
// message (matches upstream behavior where unknown slashes are sent as-is).
var ErrUnknownSlashCommand = errors.New("unknown slash command")

// Dispatch parses a line like `/foo bar baz` and routes it. Returns
// ErrUnknownSlashCommand if the command is not registered (the caller should
// forward the text to the LLM). Returns other errors if the handler failed.
// The host is responsible for surfacing errors to the user: Dispatch
// itself does not call sc.Append for errors.
//
// extCtx is the bridge to extension-registered commands (which take a
// different handler signature). May be nil if no extensions are loaded.
func (r *SlashRegistry) Dispatch(sc *SlashContext, line string, extCtx *ExtensionContext) error {
	name, args := parseSlashLine(line)
	if name == "" {
		return fmt.Errorf("empty slash command")
	}
	canon, ok := r.Resolve(name)
	if !ok {
		return ErrUnknownSlashCommand
	}

	r.mu.RLock()
	if b, ok := r.builtins[canon]; ok {
		handler := b.Handler
		r.mu.RUnlock()
		sc.Args = args
		sc.AllCommands = r.All()
		if handler == nil {
			return fmt.Errorf("/%s: no handler bound", canon)
		}
		return handler(sc)
	}
	d, ok := r.dynamic[canon]
	r.mu.RUnlock()
	if ok {
		if d.Handler == nil {
			return fmt.Errorf("/%s: extension registered with nil handler", canon)
		}
		if extCtx == nil {
			return fmt.Errorf("/%s: no extension context", canon)
		}
		return d.Handler(extCtx, args)
	}
	return fmt.Errorf("unknown command: /%s", name)
}

// parseSlashLine splits "/cmd rest of line" into ("cmd", "rest of line").
// Leading slash is required; whitespace around the name is consumed.
func parseSlashLine(line string) (name, args string) {
	s := strings.TrimSpace(line)
	if !strings.HasPrefix(s, "/") {
		return "", ""
	}
	s = strings.TrimPrefix(s, "/")
	if i := strings.IndexAny(s, " \t"); i >= 0 {
		return strings.TrimSpace(s[:i]), strings.TrimSpace(s[i+1:])
	}
	return strings.TrimSpace(s), ""
}

// ─── Builtin handlers ────────────────────────────────────────────────────────

// BuiltinSlashCommands returns the canonical builtin slash command
// list (name + description + aliases). Single source of truth for
// `/help`, the dispatcher, and the autocomplete popup -
// mirrors upstream's `BUILTIN_SLASH_COMMANDS` re-export from
// `core/slash-commands.ts`.
func BuiltinSlashCommands() []BuiltinSlashCommand { return defaultBuiltins() }

func defaultBuiltins() []BuiltinSlashCommand {
	cmds := []BuiltinSlashCommand{
		{Name: "settings", Description: "Open settings menu", Handler: settingsHandler},
		{Name: "model", Description: "Select model (opens selector UI)", ArgumentHint: "<provider/model>", Handler: modelHandler},
		{Name: "tree", Description: "Navigate session tree (switch branches)", Handler: treeHandler},
		{Name: "thinking", Description: "Set thinking level", ArgumentHint: "<level>", Handler: thinkingHandler},
		{Name: "scoped-models", Description: "Enable/disable models for Ctrl+P cycling", Handler: scopedModelsHandler},
		{Name: "export", Description: "Export session (HTML default, or specify path: .html/.jsonl)", Handler: exportHandler},
		{Name: "import", Description: "Import and resume a session from a JSONL file", Handler: importHandler},
		// pig divergence (D64): PiG shares through its own expiring gateway, not a GitHub gist.
		{Name: "share", Description: "Share session with an unlisted 30-day link", Handler: shareHandler},
		// pig divergence (D62): /bug exports a local archive for a PiG issue; it never uploads.
		{Name: "bug", Description: "Export a bug report to attach to a PiG issue", ArgumentHint: "<description>", Handler: bugHandler},
		{Name: "copy", Description: "Copy last agent message to clipboard", Handler: copyHandler},
		{Name: "name", Description: "Set session display name", Handler: nameHandler},
		{Name: "session", Description: "Show session info and stats", Handler: sessionHandler},
		{Name: "changelog", Description: "Show changelog entries", Handler: changelogHandler},
		{Name: "hotkeys", Description: "Show all keyboard shortcuts", Handler: hotkeysHandler},
		{Name: "fork", Description: "Create a new fork from a previous user message", Handler: forkHandler},
		{Name: "clone", Description: "Duplicate the current session at the current position", Handler: cloneHandler},
		{Name: "trust", Description: "Save project trust decision for future sessions", Handler: trustHandler},
		{Name: "login", Description: "Configure provider authentication", ArgumentHint: "<provider>", Handler: loginHandler},
		{Name: "logout", Description: "Remove stored provider authentication", Handler: logoutHandler},
		{Name: "new", Description: "Start a new session", Handler: newHandler},
		{Name: "compact", Description: "Manually compact the session context", Handler: compactHandler},
		{Name: "resume", Description: "Resume a different session", Handler: resumeHandler},
		{Name: "reload", Description: "Reload keybindings, extensions, skills, prompts, themes, and context files", Handler: reloadHandler},
		{Name: "quit", Description: "Quit " + AppName, Handler: quitHandler},
		{Name: llama.CommandName, Description: llama.CommandDescription, Handler: llamaHandler},
		// Hidden: dispatchable but absent from /help and autocomplete, matching
		// upstream (handled inline in the submit handler, not in the canonical
		// slash-commands.js completion list). /debug writes a debug log; it is
		// also bound to the ctrl+shift+d global hotkey.
		{Name: "debug", Description: "Write a debug log", Handler: debugHandler, Hidden: true},
	}
	if parityHarnessEnabled() || os.Getenv("PIG_PARITY_HARNESS") == "1" {
		cmds = append(cmds,
			BuiltinSlashCommand{Name: "probe-clipboard-read", Description: "Parity harness clipboard probe", Handler: probeClipboardReadHandler},
			BuiltinSlashCommand{Name: "probe-image-fallback", Description: "Parity harness image fallback probe", Handler: probeImageFallbackHandler},
			BuiltinSlashCommand{Name: "probe-cancellable-loader", Description: "Parity harness cancellable loader probe", Handler: probeCancellableLoaderHandler},
			BuiltinSlashCommand{Name: "probe-select-list", Description: "Parity harness SelectList probe", Handler: probeSelectListHandler},
			BuiltinSlashCommand{Name: "probe-oauth-shared", Description: "Parity harness OAuth shared probe", Handler: probeOAuthSharedHandler},
			BuiltinSlashCommand{Name: "probe-oauth-callback-page", Description: "Parity harness OAuth callback page probe", Handler: probeOAuthCallbackPageHandler},
			BuiltinSlashCommand{Name: "probe-copilot-headers", Description: "Parity harness Copilot headers probe", Handler: probeCopilotHeadersHandler},
			BuiltinSlashCommand{Name: "probe-oauth-copilot", Description: "Parity harness Copilot OAuth probe", Handler: probeOAuthCopilotHandler},
			BuiltinSlashCommand{Name: "probe-oauth-copilot-env", Description: "Parity harness Copilot env-fallback probe", Handler: probeOAuthCopilotEnvHandler},
			BuiltinSlashCommand{Name: "probe-oauth-anthropic", Description: "Parity harness Anthropic OAuth probe", Handler: probeOAuthAnthropicHandler},
			BuiltinSlashCommand{Name: "probe-oauth-codex", Description: "Parity harness OpenAI Codex OAuth probe", Handler: probeOAuthCodexHandler},
			BuiltinSlashCommand{Name: "probe-oauth-openrouter", Description: "Parity harness OpenRouter OAuth probe", Handler: probeOAuthOpenRouterHandler},
			BuiltinSlashCommand{Name: "probe-oauth-xai", Description: "Parity harness xAI OAuth probe", Handler: probeOAuthXaiHandler},
		)
	}
	return cmds
}

// pig divergence (D35): omit Pi's animated /arminsayshi and
// /dementedelves commands.

// llamaHandler runs the command upstream's built-in hidden llama.cpp inline
// extension registers (extensions/llama/index.ts).
func llamaHandler(sc *SlashContext) error {
	if sc.RunLlama == nil {
		sc.Append("llama.cpp is not available in this context.")
		return nil
	}
	return sc.RunLlama()
}

func quitHandler(sc *SlashContext) error {
	if sc.Quit != nil {
		sc.Quit()
	}
	return nil
}

func probeClipboardReadHandler(sc *SlashContext) error {
	if sc.ProbeClipboardRead == nil {
		return fmt.Errorf("clipboard probe unavailable")
	}
	out, err := sc.ProbeClipboardRead()
	if err != nil {
		return err
	}
	sc.Append(out)
	return nil
}

func probeImageFallbackHandler(sc *SlashContext) error {
	if sc.ProbeImageFallback == nil {
		return fmt.Errorf("image fallback probe unavailable")
	}
	out, err := sc.ProbeImageFallback()
	if err != nil {
		return err
	}
	sc.Append(out)
	return nil
}

func probeSelectListHandler(sc *SlashContext) error {
	if sc.ProbeSelectList == nil {
		return fmt.Errorf("select-list probe unavailable")
	}
	out, err := sc.ProbeSelectList()
	if err != nil {
		return err
	}
	sc.Append(out)
	return nil
}

func probeCancellableLoaderHandler(sc *SlashContext) error {
	if sc.ProbeCancellableLoader == nil {
		return fmt.Errorf("cancellable loader probe unavailable")
	}
	out, err := sc.ProbeCancellableLoader()
	if err != nil {
		return err
	}
	sc.Append(out)
	return nil
}

func probeOAuthSharedHandler(sc *SlashContext) error {
	if sc.ProbeOAuthShared == nil {
		return fmt.Errorf("oauth shared probe unavailable")
	}
	out, err := sc.ProbeOAuthShared()
	if err != nil {
		return err
	}
	sc.Append(out)
	return nil
}

func probeOAuthCallbackPageHandler(sc *SlashContext) error {
	if sc.ProbeOAuthCallbackPage == nil {
		return fmt.Errorf("oauth callback page probe unavailable")
	}
	out, err := sc.ProbeOAuthCallbackPage()
	if err != nil {
		return err
	}
	sc.Append(out)
	return nil
}

func probeCopilotHeadersHandler(sc *SlashContext) error {
	if sc.ProbeCopilotHeaders == nil {
		return fmt.Errorf("copilot headers probe unavailable")
	}
	out, err := sc.ProbeCopilotHeaders()
	if err != nil {
		return err
	}
	sc.Append(out)
	return nil
}

func probeOAuthCopilotHandler(sc *SlashContext) error {
	if sc.ProbeOAuthCopilot == nil {
		return fmt.Errorf("copilot oauth probe unavailable")
	}
	out, err := sc.ProbeOAuthCopilot()
	if err != nil {
		return err
	}
	sc.Append(out)
	return nil
}

func probeOAuthCopilotEnvHandler(sc *SlashContext) error {
	if sc.ProbeOAuthCopilotEnv == nil {
		return fmt.Errorf("copilot env oauth probe unavailable")
	}
	out, err := sc.ProbeOAuthCopilotEnv()
	if err != nil {
		return err
	}
	sc.Append(out)
	return nil
}

func probeOAuthAnthropicHandler(sc *SlashContext) error {
	if sc.ProbeOAuthAnthropic == nil {
		return fmt.Errorf("anthropic oauth probe unavailable")
	}
	out, err := sc.ProbeOAuthAnthropic()
	if err != nil {
		return err
	}
	sc.Append(out)
	return nil
}

func probeOAuthCodexHandler(sc *SlashContext) error {
	if sc.ProbeOAuthCodex == nil {
		return fmt.Errorf("codex oauth probe unavailable")
	}
	out, err := sc.ProbeOAuthCodex()
	if err != nil {
		return err
	}
	sc.Append(out)
	return nil
}

// probeOAuthOpenRouterHandler and probeOAuthXaiHandler call package-level probes
// directly. Unlike the anthropic/codex probes, these need nothing from
// InteractiveMode, so they avoid the SlashContext function-field wiring.
func probeOAuthOpenRouterHandler(sc *SlashContext) error {
	out, err := probeOAuthOpenRouter()
	if err != nil {
		return err
	}
	sc.Append(out)
	return nil
}

func probeOAuthXaiHandler(sc *SlashContext) error {
	out, err := probeOAuthXai()
	if err != nil {
		return err
	}
	sc.Append(out)
	return nil
}

func modelHandler(sc *SlashContext) error {
	current := "(unknown)"
	if sc.ModelName != nil {
		current = sc.ModelName()
	}

	if sc.Args == "" {
		// Empty args: prefer the picker overlay if available.
		if sc.PickModel != nil && sc.SwitchModel != nil {
			spec, ok := sc.PickModel("")
			if !ok {
				sc.Append("Model switch cancelled.")
				return nil
			}
			if err := sc.SwitchModel(spec); err != nil {
				return fmt.Errorf("model switch failed: %w", err)
			}
			showModelSelectionStatus(sc, spec)
			return nil
		}
		// Headless or test context: just print current.
		sc.Append(fmt.Sprintf("Current model: %s", current))
		// Upstream v0.70.0 hint text.
		sc.Append("(Model picker unavailable in this context. Only showing models from configured providers. Use /login to add providers.)")
		return nil
	}

	// Direct switch: /model <spec>
	if sc.SwitchModel == nil {
		sc.Append("Model switching is not available in this context.")
		return nil
	}
	spec := sc.Args
	if sc.ResolveModel != nil {
		resolved, ok := sc.ResolveModel(sc.Args)
		switch {
		case ok:
			spec = resolved
		case sc.PickModel != nil:
			picked, pickedOK := sc.PickModel(sc.Args)
			if !pickedOK {
				return nil
			}
			spec = picked
		default:
			sc.Append("No matching models")
			return nil
		}
	}
	if err := sc.SwitchModel(spec); err != nil {
		return fmt.Errorf("model switch to %q failed: %w", spec, err)
	}
	showModelSelectionStatus(sc, spec)
	return nil
}

func showModelSelectionStatus(sc *SlashContext, spec string) {
	if sc.ShowStatus == nil {
		return
	}
	if sc.modelSelectionPersist {
		sc.ShowStatus("Default model: " + spec)
		return
	}
	_, bareID, found := strings.Cut(spec, "/")
	if !found {
		bareID = spec
	}
	sc.ShowStatus("Model: " + bareID)
}

// scopedModelsHandler implements /scoped-models: enable/disable models
// for Ctrl+P cycling. Mirrors upstream interactive-mode.ts:3925
// (showModelsSelector). Opens a multi-select toggle list via the
// editor-slot pattern.
func scopedModelsHandler(sc *SlashContext) error {
	if sc.ShowScopedModels == nil {
		sc.Append("Scoped models selector not available in this context.")
		return nil
	}
	sc.ShowScopedModels()
	return nil
}

// loginHandler implements /login: interactive OAuth login.
// Mirrors upstream interactive-mode.ts:2452-2453 → showOAuthSelector("login").
func loginHandler(sc *SlashContext) error {
	if sc.ShowOAuthSelector == nil {
		sc.Append("OAuth login is not available in this context.")
		return nil
	}

	providerID := ""
	if sc.ShowLoginAuthType != nil {
		authType, ok := sc.ShowLoginAuthType()
		if !ok {
			return nil
		}
		if authType == "api_key" {
			if sc.ShowOAuthSelector == nil || sc.SetAPIKey == nil || sc.ShowTextInput == nil {
				sc.Append("API key login is not available in this context.")
				return nil
			}
			picked, ok := sc.ShowOAuthSelector("login-api-key")
			if !ok {
				return nil
			}
			if sc.LoginAPIKeyProvider != nil && sc.LoginAPIKeyProvider(picked) {
				return nil
			}
			value, ok := sc.ShowTextInput("Enter API key", "sk-...")
			if !ok {
				return nil
			}
			if err := sc.SetAPIKey(picked, strings.TrimSpace(value)); err != nil {
				sc.Append(fmt.Sprintf("Login failed: %v", err))
			}
			return nil
		}
	}

	if sc.Login == nil {
		sc.Append("OAuth login is not available in this context.")
		return nil
	}

	picked, ok := sc.ShowOAuthSelector("login-oauth")
	if !ok {
		return nil
	}
	providerID = picked

	ctx := context.Background()
	if err := sc.Login(ctx, providerID); err != nil {
		sc.Append(fmt.Sprintf("Login failed: %v", err))
	}
	return nil
}

// logoutHandler implements /logout: remove stored OAuth credentials.
// Mirrors upstream interactive-mode.ts:2457-2458 → showOAuthSelector("logout").
func logoutHandler(sc *SlashContext) error {
	if sc.Logout == nil || sc.ShowOAuthSelector == nil {
		sc.Append("OAuth logout is not available in this context.")
		return nil
	}

	providerID, ok := sc.ShowOAuthSelector("logout")
	if !ok {
		return nil
	}

	if err := sc.Logout(providerID); err != nil {
		sc.Append(fmt.Sprintf("Logout failed: %v", err))
		return nil
	}
	if sc.LogoutProviderName != nil {
		showStatusOrAppend(sc, fmt.Sprintf("Logged out of %s", sc.LogoutProviderName(providerID)))
	}
	return nil
}

func copyHandler(sc *SlashContext) error {
	if sc.LastAssistant == nil || sc.CopyClipboard == nil {
		sc.Append("Clipboard unavailable in this build.")
		return nil
	}
	// Mirrors upstream handleCopyCommand(): failures surface through
	// showError, which the dispatcher renders for a returned error.
	text := sc.LastAssistant()
	if text == "" {
		return errors.New("No agent messages to copy yet.")
	}
	if err := sc.CopyClipboard(text); err != nil {
		return err
	}
	showStatusOrAppend(sc, "Copied last agent message to clipboard")
	return nil
}

func sessionHandler(sc *SlashContext) error {
	// Mirrors upstream handleSessionCommand (interactive-mode.ts:5169-5207).
	// Uses ANSI styling (bold + dim) via Text component, not Markdown.
	bold := func(s string) string { return "\033[1m" + s + "\033[22m" }
	dim := func(s string) string { return "\033[2m" + s + "\033[22m" }

	var b strings.Builder
	b.WriteString(bold("Session Info") + "\n\n")

	var sessionName string
	if sc.GetSessionName != nil {
		sessionName = sc.GetSessionName()
	}
	if sessionName != "" {
		fmt.Fprintf(&b, "%s %s\n", dim("Name:"), sessionName)
	}

	// File + ID from session.
	if sc.CurrentSession != nil {
		if s := sc.CurrentSession(); s != nil {
			path := s.Path()
			if path == "" {
				path = "In-memory"
			}
			fmt.Fprintf(&b, "%s %s\n", dim("File:"), path)
			fmt.Fprintf(&b, "%s %s\n", dim("ID:"), s.ID())
		}
	} else if sc.SessionInfo != nil {
		id, _, _ := sc.SessionInfo()
		if id != "" {
			fmt.Fprintf(&b, "%s %s\n", dim("ID:"), id)
		}
	}

	var stats SessionAccounting
	if sc.CurrentSession != nil {
		if s := sc.CurrentSession(); s != nil {
			stats = s.Accounting()
		}
	}
	b.WriteString("\n" + bold("Messages") + "\n")
	fmt.Fprintf(&b, "%s %d\n", dim("Total:"), stats.TotalMessages)
	fmt.Fprintf(&b, "%s %d\n", dim("User:"), stats.UserMessages)
	fmt.Fprintf(&b, "%s %d\n", dim("Assistant:"), stats.AssistantMessages)
	fmt.Fprintf(&b, "%s %d calls, %d results\n", dim("Tools:"), stats.ToolCalls, stats.ToolResults)

	ts := stats.Tokens
	promptTokens := ts.Input + ts.CacheRead + ts.CacheWrite
	b.WriteString("\n" + bold("Tokens") + "\n")
	fmt.Fprintf(&b, "%s %s\n", dim("Input:"), formatNumber(promptTokens))
	if promptTokens > 0 && (ts.CacheRead > 0 || ts.CacheWrite > 0) {
		fmt.Fprintf(&b, "  %s %s %s\n", dim("Cached:"), formatNumber(ts.CacheRead), dim("("+tui.JSToFixed((float64(ts.CacheRead)/float64(promptTokens))*100, 1)+"%)"))
		written := ""
		if ts.CacheWrite > 0 {
			written = " " + dim(fmt.Sprintf("(%s written to cache)", formatNumber(ts.CacheWrite)))
		}
		fmt.Fprintf(&b, "  %s %s%s\n", dim("Uncached:"), formatNumber(ts.Input+ts.CacheWrite), written)
	}
	fmt.Fprintf(&b, "%s %s\n", dim("Output:"), formatNumber(ts.Output))
	fmt.Fprintf(&b, "%s %s\n", dim("Total:"), formatNumber(ts.Total))
	writeCacheWarmingSection(&b, sc, bold, dim)

	if ts.Cost > 0 || stats.CacheWaste.MissedTokens > 0 {
		b.WriteString("\n" + bold("Cost") + "\n")
		fmt.Fprintf(&b, "%s $%s", dim("Total:"), tui.JSToFixed(ts.Cost, 3))
		if len(stats.UsageBreakdown) > 1 {
			for _, entry := range stats.UsageBreakdown {
				fmt.Fprintf(&b, "\n  %s $%s %s", dim(entry.Key+":"), tui.JSToFixed(entry.Cost, 3), dim(fmt.Sprintf("(%s tokens)", formatTokens(entry.Tokens))))
			}
		}
		if stats.CacheWaste.MissedTokens > 0 {
			missLabel := fmt.Sprintf("%d misses", stats.CacheWaste.MissCount)
			if stats.CacheWaste.MissCount == 1 {
				missLabel = "1 miss"
			}
			detail := fmt.Sprintf("%s tokens, %s", formatNumber(stats.CacheWaste.MissedTokens), missLabel)
			if stats.CacheWaste.MissedCost >= 0.0001 {
				fmt.Fprintf(&b, "\n%s $%s %s", dim("Cache Re-billed:"), tui.JSToFixed(stats.CacheWaste.MissedCost, 3), dim("("+detail+")"))
			} else {
				fmt.Fprintf(&b, "\n%s %s", dim("Cache Re-billed:"), detail)
			}
		}
	}

	// Use AppendText (not Append/Markdown): upstream uses new Text(info, 1, 0)
	// for /session output (plain ANSI text with 1px left padding).
	if sc.AppendText != nil {
		sc.AppendText(b.String())
	} else {
		sc.Append(b.String())
	}
	return nil
}

// writeCacheWarmingSection renders the /session "Cache Warming" block.
// Mirrors upstream handleSessionCommand (interactive-mode.ts:6472-6480).
func writeCacheWarmingSection(b *strings.Builder, sc *SlashContext, bold, dim func(string) string) {
	mode := defaultCacheWarmingMode
	if sc.SettingsManager != nil {
		mode = sc.SettingsManager.GetCacheWarmingMode()
	}
	var status *CacheWarmingStatus
	if sc.CacheWarmingStatus != nil {
		status = sc.CacheWarmingStatus()
	}
	statusText := "Inactive (cache warming unavailable)"
	if status != nil {
		statusText = FormatCacheWarmingStatus(*status, time.Now().UnixMilli())
	}
	b.WriteString("\n" + bold("Cache Warming") + "\n")
	fmt.Fprintf(b, "%s %s\n", dim("Mode:"), mode)
	fmt.Fprintf(b, "%s %s\n", dim("Status:"), statusText)
	if status != nil && status.Decision != nil && status.Decision.EconomicsAvailable {
		fmt.Fprintf(b, "%s $%s\n", dim("Cache miss penalty:"), jsToFixed(status.Decision.MissCost, 3))
		fmt.Fprintf(b, "%s $%s\n", dim("Refresh cost:"), jsToFixed(status.Decision.WarmCost, 3))
	}
}

// formatNumber adds comma separators to an integer, matching
// upstream's toLocaleString() for readability.
func formatNumber(n int) string {
	if n < 0 {
		return "-" + formatNumber(-n)
	}
	s := fmt.Sprintf("%d", n)
	if len(s) <= 3 {
		return s
	}
	var buf strings.Builder
	leader := len(s) % 3
	if leader > 0 {
		buf.WriteString(s[:leader])
	}
	for i := leader; i < len(s); i += 3 {
		if buf.Len() > 0 {
			buf.WriteByte(',')
		}
		buf.WriteString(s[i : i+3])
	}
	return buf.String()
}

func hotkeysHandler(sc *SlashContext) error {
	var lines []string
	if sc.HotkeyLines != nil {
		lines = sc.HotkeyLines()
	}
	if len(lines) == 0 {
		// Fallback for headless/test contexts where the interactive keybindings
		// manager is not wired.
		lines = []string{
			"Enter       : submit",
			"Ctrl+J      : newline",
			"Shift+Enter : newline (kitty/iTerm)",
			"Alt+Enter   : follow-up (while working) / submit (idle)",
			"Alt+Up      : dequeue follow-up messages",
			"Esc         : abort (while working) / cancel",
			"Esc Esc     : open /tree (500ms window)",
			"Ctrl+C      : abort (working) / clear editor (idle)",
			"Ctrl+D      : exit pig",
			"Ctrl+O      : toggle all tool details",
			"Ctrl+G      : open external editor ($VISUAL / $EDITOR)",
			"Ctrl+L      : model picker",
			"Ctrl+P      : cycle model forward",
			"Shift+Ctrl+P: cycle model backward",
			"Ctrl+T      : toggle thinking block visibility",
			"Ctrl+V      : paste image from clipboard",
			"Ctrl+Z      : suspend (resume with fg)",
			"Shift+Tab   : cycle thinking level",
			"Ctrl+U      : kill line (cut to start)",
			"Ctrl+K      : kill to end of line",
			"Ctrl+Y      : yank (paste from kill ring)",
			"Alt+Y       : cycle yank ring",
			"Ctrl+/      : undo",
			"Ctrl+A      : move to start of line",
			"Ctrl+E      : move to end of line",
			"Alt+B       : word backward",
			"Alt+F       : word forward",
			"Alt+Bksp    : delete word backward",
			"Alt+D       : delete word forward",
			"Ctrl+Del    : delete forward",
			"Up/Down     : history navigation (empty editor)",
		}
	}
	var b strings.Builder
	b.WriteString("**Hotkeys**\n\n")
	for _, l := range lines {
		fmt.Fprintf(&b, "  %s\n", l)
	}
	sc.Append(b.String())
	return nil
}

func newHandler(sc *SlashContext) error {
	// Upstream: /new calls runtimeHost.newSession() which creates a fresh
	// JSONL file + resets agent messages + emits session lifecycle events.
	if sc.NewSession != nil {
		if err := sc.NewSession(); err != nil {
			if sc.FatalRuntimeError != nil {
				return sc.FatalRuntimeError("Failed to create session", err)
			}
			return fmt.Errorf("/new: %w", err)
		}
		sc.Append("✓ New session started")
		return nil
	}
	// Fallback for test contexts where NewSession is not wired.
	if sc.Reset != nil {
		sc.Reset()
	}
	if sc.Clear != nil {
		sc.Clear()
	}
	sc.Append("Started a fresh conversation. Message history cleared.")
	return nil
}

// thinkingHandler mirrors upstream handleThinkingCommand: without an argument
// it opens the thinking selector; with one it selects the matching available
// level, ignoring case.
func thinkingHandler(sc *SlashContext) error {
	if sc.AvailableThinkingLevels == nil || sc.SelectThinkingLevel == nil {
		sc.Append("Thinking level selection is not available in this context.")
		return nil
	}
	levels := sc.AvailableThinkingLevels()
	if search := strings.TrimSpace(sc.Args); search != "" {
		for _, level := range levels {
			if strings.EqualFold(level, search) {
				sc.SelectThinkingLevel(level)
				return nil
			}
		}
		return fmt.Errorf("Unknown thinking level %q. Available levels: %s.", search, strings.Join(levels, ", "))
	}
	if sc.ShowThinkingSelector != nil {
		sc.ShowThinkingSelector()
		return nil
	}
	if sc.ShowSelectList == nil {
		sc.Append("Available thinking levels: " + strings.Join(levels, ", "))
		return nil
	}
	options := make([]tui.SelectItem, len(levels))
	for i, level := range levels {
		options[i] = tui.SelectItem{Value: level, Label: level, Description: thinkingDescriptions[level]}
	}
	current := ""
	if sc.CurrentThinkingLevel != nil {
		current = sc.CurrentThinkingLevel()
	}
	if chosen, ok := sc.ShowSelectList("Thinking Level", "Select reasoning depth for thinking-capable models", options, current); ok && chosen != "" {
		sc.SelectThinkingLevel(chosen)
	}
	return nil
}
