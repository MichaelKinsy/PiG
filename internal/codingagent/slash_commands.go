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

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/internal/pigstrip"
	"github.com/MichaelKinsy/PiG/tui"
	"github.com/MichaelKinsy/PiG/tui/widthx"
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
	SlashSourceSkill     SlashCommandSource = "skill"  // /skill:name for each loaded skill
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
	// TakesArguments is true for the commands whose line interactive-mode.ts matches with `text === "/name" || text.startsWith("/name ")`.
	// The others match only the exact text, so "/session x" is not /session but a prompt.
	TakesArguments bool
	// AdditiveArguments accepts argument text Pi's built-in does not take: it reports whether "/name rest" runs the built-in, and headless is true
	// for Session.DispatchSlash, which has no picker to open. /reload takes --explain (D21); a headless /fork takes the entry id to fork at.
	AdditiveArguments func(rest string, headless bool) bool
	Handler           SlashHandler
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
	// AppendBlock appends Spacer(1) and Text(text, 1, 1), the confirmation
	// block upstream handlers such as handleDebugCommand add. The text is not
	// Markdown, so paths keep their backslashes.
	AppendBlock func(string)
	Clear       func() // clear the chat transcript
	Quit        func() // request session exit
	Reset       func() // /new: keep transcript visible but reset agent messages
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
	ModelName func() string
	// SelectedModelKey is `provider/id` of the session's selected model, the key of a single-model /session cost total.
	SelectedModelKey func() string
	ToolNames        func() []string
	// ToolRenderers returns how HTML exports draw tool calls; see [ExportToolRenderers].
	ToolRenderers func() func(name string) *extension.ToolRenderers
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
	ShowHotkeys   func()
	ShowChangelog func()

	// All registered commands: set by Dispatch before calling Handler
	// so /help can enumerate the live set.
	AllCommands []HelpCommandInfo

	// Session accessors. May be nil; handlers must guard.
	CurrentSession func() *Session
	// ExportToJsonl writes the current branch to outputPath (upstream session.exportToJsonl). May be nil: /export then
	// writes the branch of CurrentSession itself.
	ExportToJsonl func(outputPath string) (string, error)
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
	// ResumeStatus, when set, returns the status /resume shows after LoadSessionPath succeeds. The default is "Resumed session".
	ResumeStatus func() string
	// ImportSession imports a session JSONL file through the Session and
	// reports whether session_before_switch cancelled the switch. Mirrors
	// upstream runtimeHost.importFromJsonl.
	ImportSession func(inputPath, cwdOverride string) (cancelled bool, err error)

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
	// ShowExtensionConfirm shows a Yes/No confirmation and reports whether Yes was chosen. Mirrors upstream
	// showExtensionConfirm(title, message). Nil falls back to ShowExtensionSelector.
	ShowExtensionConfirm func(title, message string) bool
	// ShowTrustSelector invokes OnSelect before restoring the editor so persistence precedes closing the selector.
	ShowTrustSelector func(TrustSelectorOptions) (TrustSelection, bool)
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
	// CurrentTuiMode returns the running renderer's mode, independent of the saved default. Nil outside the interactive UI.
	CurrentTuiMode func() string
	// SwitchTuiMode switches the running renderer before the settings list saves a TUI mode, and returns false when the switch is refused. Nil outside the interactive UI.
	SwitchTuiMode func(mode string) bool
	// ShowSettingsSelector builds the settings selector with done and presents it, blocking until done runs. Upstream's showSelector((done) => ...) does the same.
	ShowSettingsSelector func(build func(done func()) *SettingsSelectorComponent)
	// PreviewTheme applies a theme setting without saving it. Used by the selector's theme submenu.
	PreviewTheme func(setting string)
	// ThemeSelection returns the theme setting in effect, or "" when there is none.
	ThemeSelection func() string
	// SettingsModels returns the models the per-model thinking submenu offers and the session's current model.
	SettingsModels func() (available []*ai.Model, current *ai.Model)
	// ApplyModelThinkingLevel applies a changed per-model thinking level to the session when it is for the current model. An empty level means the override was cleared.
	ApplyModelThinkingLevel func(provider, modelID, level string)
	// ShowSelectList presents a non-search submenu selector with optional
	// description column. Used by /settings for upstream-style submenus like
	// thinking level. Returns the chosen value or ("", false) on cancel.
	ShowSelectList func(title, description string, items []tui.SelectItem, currentValue string) (value string, ok bool)
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
	Reload func() error

	// ReloadDiagnostics returns resource counts after a reload.
	// Mirrors upstream showLoadedResources with showDiagnosticsWhenQuiet.
	// May be nil; handler falls back to a static message.
	ReloadDiagnostics func() ReloadDiag
	// ReloadSavedProjectTrust reports, once per reload, whether the reload saved the implicit project trust decision.
	ReloadSavedProjectTrust func() bool

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

	// Provider metadata and modal callbacks for /login and /logout.
	LoginProviders     func() []tui.OAuthProvider
	LogoutProviders    func() ([]tui.OAuthProvider, error)
	SelectAuthProvider func(mode string, providers []tui.OAuthProvider, initialSearch string) (tui.OAuthProvider, bool)
	// SelectAuthMethod returns "oauth" or "api_key", or the ID of a provider the top-level menu (nil providers) offers
	// directly, such as Radius.
	SelectAuthMethod func(providers []tui.OAuthProvider) (choice string, ok bool)
	// StartProviderLogin returns errLoginCancelled when the user cancels the login, which reopens the menu it was
	// started from.
	StartProviderLogin func(provider tui.OAuthProvider) error

	// Logout removes stored credentials for the given provider.
	// Mirrors upstream showOAuthSelector logout branch (interactive-mode.ts:4299).
	// May be nil in headless/test contexts; handlers must guard.
	Logout func(provider string) error

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

// HelpCommandInfo is the user-facing summary used by `/help`. It is not upstream's SlashCommandInfo, which is [SlashCommandInfo].
type HelpCommandInfo struct {
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
	for _, c := range BuiltinSlashCommands() {
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

// IsBuiltin reports whether name (or an alias) is a built-in command, the
// commands upstream's onSubmit handles in its builtin chain.
func (r *SlashRegistry) IsBuiltin(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if _, ok := r.builtins[name]; ok {
		return true
	}
	_, ok := r.aliases[name]
	return ok
}

// All returns a sorted list of every command (builtin + extension) for
// /help rendering.
func (r *SlashRegistry) All() []HelpCommandInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]HelpCommandInfo, 0, len(r.builtins)+len(r.dynamic))
	for _, b := range r.builtins {
		if b.Hidden {
			continue
		}
		out = append(out, HelpCommandInfo{
			Name:        b.Name,
			Aliases:     append([]string(nil), b.Aliases...),
			Description: b.Description,
			Source:      SlashSourceBuiltin,
		})
	}
	for _, d := range r.dynamic {
		out = append(out, HelpCommandInfo{
			Name:        d.Name,
			Description: d.Description,
			Source:      SlashSourceExtension,
		})
	}
	slices.SortFunc(out, func(a, b HelpCommandInfo) int { return cmp.Compare(a.Name, b.Name) })
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
	return r.dispatch(sc, line, extCtx, false)
}

// DispatchHeadless is [SlashRegistry.Dispatch] for Session.DispatchSlash, where a built-in without a picker reads its argument instead.
func (r *SlashRegistry) DispatchHeadless(sc *SlashContext, line string, extCtx *ExtensionContext) error {
	return r.dispatch(sc, line, extCtx, true)
}

func (r *SlashRegistry) dispatch(sc *SlashContext, line string, extCtx *ExtensionContext, headless bool) error {
	match, ok := r.match(line, headless)
	if !ok {
		if name, _ := parseSlashLine(line); name == "" {
			return fmt.Errorf("empty slash command")
		}
		return ErrUnknownSlashCommand
	}
	if match.Builtin != nil {
		handler := match.Builtin.Handler
		sc.Args = match.Args
		sc.AllCommands = r.All()
		if handler == nil {
			return fmt.Errorf("/%s: no handler bound", match.Builtin.Name)
		}
		return handler(sc)
	}
	d := match.Dynamic
	if d.Handler == nil {
		return fmt.Errorf("/%s: extension registered with nil handler", match.Name)
	}
	if extCtx == nil {
		return fmt.Errorf("/%s: no extension context", match.Name)
	}
	return d.Handler(extCtx, match.Args)
}

// SlashMatch is a submitted line resolved to the command it runs.
type SlashMatch struct {
	// Name is the command name as written, without the slash.
	Name string
	// Args is the argument text: for a built-in, the trimmed remainder of a command that takes arguments; for an extension command,
	// everything after the first space (agent-session.ts _tryExecuteExtensionCommand).
	Args string
	// Builtin or Dynamic is the resolved command.
	Builtin *BuiltinSlashCommand
	Dynamic *SlashCommand
}

// Match resolves a submitted line as interactive-mode.ts does. The line is trimmed with ECMAScript whitespace. A built-in matches the exact text
// "/name" and, when it takes arguments, "/name " followed by anything (one ASCII space: a tab or a no-break space does not separate); any other
// line is not that built-in. A line that is not a built-in is an extension command when the name before its first space is registered.
func (r *SlashRegistry) Match(line string) (SlashMatch, bool) { return r.match(line, false) }

func (r *SlashRegistry) match(line string, headless bool) (SlashMatch, bool) {
	text := widthx.JSTrim(line)
	if !strings.HasPrefix(text, "/") {
		return SlashMatch{}, false
	}
	name, rest, hasArgs := strings.Cut(text[1:], " ")
	r.mu.RLock()
	defer r.mu.RUnlock()
	canonical := name
	if alias, ok := r.aliases[name]; ok {
		canonical = alias
	}
	if b, ok := r.builtins[canonical]; ok {
		switch {
		case !hasArgs:
			return SlashMatch{Name: name, Builtin: b}, true
		case b.TakesArguments, b.AdditiveArguments != nil && b.AdditiveArguments(rest, headless):
			return SlashMatch{Name: name, Args: widthx.JSTrim(rest), Builtin: b}, true
		}
	}
	if d, ok := r.dynamic[name]; ok {
		return SlashMatch{Name: name, Args: rest, Dynamic: &d}, true
	}
	return SlashMatch{}, false
}

// parseSlashLine splits "/cmd rest of line" at the first space into ("cmd", "rest of line"). The leading slash is required.
func parseSlashLine(line string) (name, args string) {
	text := widthx.JSTrim(line)
	if !strings.HasPrefix(text, "/") {
		return "", ""
	}
	name, args, _ = strings.Cut(text[1:], " ")
	return name, args
}

// ─── Builtin handlers ────────────────────────────────────────────────────────

// BuiltinSlashCommands returns the canonical builtin slash command
// list (name + description + aliases). Single source of truth for
// `/help`, the dispatcher, and the autocomplete popup -
// mirrors upstream's `BUILTIN_SLASH_COMMANDS` re-export from
// `core/slash-commands.ts`.
// pig additive (D92): a command the process strips (pigstrip commands), or whose feature it strips (featureCommands), is
// not in the list, so no registry, dispatcher or completion derived from it offers the command, and typing it sends it to
// the model like any unknown slash command.
func BuiltinSlashCommands() []BuiltinSlashCommand {
	return slices.DeleteFunc(defaultBuiltins(), func(command BuiltinSlashCommand) bool {
		feature, owned := featureCommands[command.Name]
		return pigstrip.Has(pigstrip.ListCommands, "/"+command.Name) || owned && pigstrip.Has(pigstrip.ListFeatures, feature)
	})
}

// featureCommands are the built-in commands that only show a strippable feature, by command name.
var featureCommands = map[string]string{"changelog": pigstrip.Changelog}

// upstreamBuiltinCommandNames is the set of names in Pi's BUILTIN_SLASH_COMMANDS (core/slash-commands.ts): the commands /help and
// autocomplete list. The hidden /debug and the parity harness probes dispatch but are not in Pi's list, so an extension command may
// use those names without a conflict.
func upstreamBuiltinCommandNames() map[string]struct{} {
	names := make(map[string]struct{})
	for _, command := range BuiltinSlashCommands() {
		if !command.Hidden && !strings.HasPrefix(command.Name, "probe-") {
			names[command.Name] = struct{}{}
		}
	}
	return names
}

func defaultBuiltins() []BuiltinSlashCommand {
	cmds := []BuiltinSlashCommand{
		{Name: "settings", Description: "Open settings menu", Handler: settingsHandler},
		{Name: "model", Description: "Select model (opens selector UI)", ArgumentHint: "<provider/model>", TakesArguments: true, Handler: modelHandler},
		{Name: "tree", Description: "Navigate session tree (switch branches)", Handler: treeHandler},
		{Name: "thinking", Description: "Set thinking level", ArgumentHint: "<level>", TakesArguments: true, Handler: thinkingHandler},
		{Name: "scoped-models", Description: "Enable/disable models for Ctrl+P cycling", Handler: scopedModelsHandler},
		{Name: "export", Description: "Export session (HTML default, or specify path: .html/.jsonl)", TakesArguments: true, Handler: exportHandler},
		{Name: "import", Description: "Import and resume a session from a JSONL file", TakesArguments: true, Handler: importHandler},
		// pig divergence (D64): PiG shares through its own expiring gateway, not a GitHub gist.
		{Name: "share", Description: "Share session with an unlisted 30-day link", Handler: shareHandler},
		// pig divergence (D62): /bug exports a local archive for a PiG issue; it never uploads.
		{Name: "bug", Description: "Export a bug report to attach to a PiG issue", ArgumentHint: "<description>", TakesArguments: true, Handler: bugHandler},
		{Name: "copy", Description: "Copy last agent message to clipboard", Handler: copyHandler},
		{Name: "name", Description: "Set session display name", TakesArguments: true, Handler: nameHandler},
		{Name: "session", Description: "Show session info and stats", Handler: sessionHandler},
		{Name: "changelog", Description: "Show changelog entries", Handler: changelogHandler},
		{Name: "hotkeys", Description: "Show all keyboard shortcuts", Handler: hotkeysHandler},
		{Name: "fork", Description: "Create a new fork from a previous user message", AdditiveArguments: func(rest string, headless bool) bool { return headless && widthx.JSTrim(rest) != "" }, Handler: forkHandler},
		{Name: "clone", Description: "Duplicate the current session at the current position", Handler: cloneHandler},
		{Name: "trust", Description: "Save project trust decision for future sessions", Handler: trustHandler},
		{Name: "login", Description: "Configure provider authentication", ArgumentHint: "<provider>", TakesArguments: true, Handler: loginHandler},
		{Name: "logout", Description: "Remove provider authentication", Handler: logoutHandler},
		{Name: "new", Description: "Start a new session", Handler: newHandler},
		{Name: "compact", Description: "Manually compact the session context", TakesArguments: true, Handler: compactHandler},
		{Name: "resume", Description: "Resume a different session", Handler: resumeHandler},
		{Name: "reload", Description: "Reload keybindings, extensions, skills, prompts, themes, and context files", AdditiveArguments: func(rest string, _ bool) bool { return reloadExplainRequested(rest) }, Handler: reloadHandler},
		{Name: "quit", Description: "Quit " + AppName, Handler: quitHandler},
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

// loginHandler configures provider authentication, using an optional provider ID or display name.
func loginHandler(sc *SlashContext) error {
	if sc.LoginProviders == nil || sc.StartProviderLogin == nil {
		sc.Append("Provider login is not available in this context.")
		return nil
	}
	return handleLoginCommand(sc)
}

// logoutHandler removes a stored credential without changing environment or models.json configuration.
func logoutHandler(sc *SlashContext) error {
	if sc.LogoutProviders == nil || sc.Logout == nil {
		sc.Append("Provider logout is not available in this context.")
		return nil
	}
	return handleLogoutCommand(sc)
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
	// Uses theme.bold and theme.fg("dim") through a Text component, not Markdown.
	bold := func(s string) string { return "\033[1m" + s + "\033[22m" }
	dim := func(s string) string { return tui.ActiveTheme().Fg("dim", s) }

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
		// A single entry repeats the total, unless it names a model other than the selected one.
		// upstream: packages/coding-agent/src/modes/interactive/interactive-mode.ts handleSessionCommand renderInfo
		selectedModelKey := "undefined/undefined"
		if sc.SelectedModelKey != nil {
			selectedModelKey = sc.SelectedModelKey()
		}
		if len(stats.UsageBreakdown) > 1 || (len(stats.UsageBreakdown) == 1 && stats.UsageBreakdown[0].Key != selectedModelKey) {
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
	if sc.ShowHotkeys != nil {
		sc.ShowHotkeys()
	} else {
		sc.Append(hotkeysMarkdown(nil))
	}
	return nil
}

func newHandler(sc *SlashContext) error {
	// Upstream: /new calls runtimeHost.newSession() which creates a fresh
	// JSONL file + resets agent messages + emits session lifecycle events.
	// NewSession also shows "✓ New session started" in the cleared
	// transcript, as upstream handleClearCommand does.
	if sc.NewSession != nil {
		if err := sc.NewSession(); err != nil {
			if sc.FatalRuntimeError != nil {
				return sc.FatalRuntimeError("Failed to create session", err)
			}
			return fmt.Errorf("/new: %w", err)
		}
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
	if search := jsTrim(sc.Args); search != "" {
		for _, level := range levels {
			if strings.EqualFold(level, search) {
				sc.SelectThinkingLevel(level)
				return nil
			}
		}
		// upstream: interactive-mode.ts handleThinkingCommand interpolates the term as written inside double quotes, not as a quoted literal.
		return fmt.Errorf("Unknown thinking level \"%s\". Available levels: %s.", search, strings.Join(levels, ", "))
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
