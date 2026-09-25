// extensions.go defines extension event names and the TUI-backed UI surface:
//   - Event name constants
//   - ToolCallEventResult (agent loop hook)
//   - ExtensionUIContext interface + NoopUI (headless fallback)
//   - ExtensionContext (shared mutable state for the agent loop)
//   - SlashCommand type (used by slash_commands.go)
//   - TUIUIContext (interactive mode UI)
package codingagent

import (
	"context"
	"sync"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/tui"
)

// ─── Event Names ─────────────────────────────────────────────────────────────

// Extension event name constants: mirror upstream event type strings.
const (
	// Session lifecycle
	EventSessionStart         = "session_start"
	EventSessionInfoChanged   = "session_info_changed"
	EventSessionShutdown      = "session_shutdown"
	EventSessionBeforeSwitch  = "session_before_switch"
	EventSessionBeforeFork    = "session_before_fork"
	EventSessionBeforeCompact = "session_before_compact"
	EventSessionCompact       = "session_compact"
	EventSessionCompactFailed = "session_compact_failed"
	EventSessionBeforeTree    = "session_before_tree"
	EventSessionTree          = "session_tree"

	// Agent loop
	EventAgentStart        = "agent_start"
	EventAgentEnd          = "agent_end"
	EventAgentBeforeSettle = "agent_before_settle"
	EventAgentSettled      = "agent_settled"
	EventTurnStart         = "turn_start"
	EventTurnEnd           = "turn_end"
	EventBeforeAgentStart  = "before_agent_start"
	EventMessageStart      = "message_start"
	EventMessageUpdate     = "message_update"
	EventMessageEnd        = "message_end"

	// Tools
	EventToolCall            = "tool_call"
	EventToolResult          = "tool_result"
	EventToolExecutionStart  = "tool_execution_start"
	EventToolExecutionUpdate = "tool_execution_update"
	EventToolExecutionEnd    = "tool_execution_end"

	// Context / provider
	EventContext               = "context"
	EventContextWithSystem     = "context_with_system"
	EventBeforeProviderRequest = "before_provider_request"
	EventAfterProviderResponse = "after_provider_response"
	EventBeforeProviderHeaders = "before_provider_headers"

	// Models / input
	EventModelSelect         = "model_select"
	EventThinkingLevelSelect = "thinking_level_select"
	EventInput               = "input"
	// User-initiated bash command (`!cmd` / `!!cmd`).
	// Mirrors upstream `user_bash` event emitted by
	// `interactive-mode.ts:5040-5045::extensionRunner.emitUserBash`.
	// Payload: {type, command, excludeFromContext, cwd}.
	EventUserBash = "user_bash"

	// Resources
	EventResourcesDiscover = "resources_discover"
)

// ─── Tool Call Hook Result ────────────────────────────────────────────────────

// ToolCallEventResult controls whether an extension allows a tool call.
type ToolCallEventResult struct {
	Block  bool   `json:"block"`
	Reason string `json:"reason,omitempty"`
}

// ─── ExtensionUIContext ───────────────────────────────────────────────────────

// ExtensionUIContext mirrors the upstream ExtensionUIContext interface.
// In interactive mode this is backed by the TUI; in headless/print mode
// it falls back to no-op implementations.
type ExtensionUIContext interface {
	Select(title string, options []string) (string, bool)
	Confirm(title, message string) bool
	Input(title, placeholder string) (string, bool)
	Notify(message, level string)
	SetStatus(key, text string)
	SetWidget(key string, lines []string)
}

// NoopUI is a non-interactive ExtensionUIContext for headless mode.
type NoopUI struct{}

func (NoopUI) Select(_ string, _ []string) (string, bool) { return "", false }
func (NoopUI) Confirm(_, _ string) bool                   { return false }
func (NoopUI) Input(_, _ string) (string, bool)           { return "", false }
func (NoopUI) Notify(_, _ string)                         {}
func (NoopUI) SetStatus(_, _ string)                      {}
func (NoopUI) SetWidget(_ string, _ []string)             {}

// ─── ExtensionContext ─────────────────────────────────────────────────────────

// ExtensionContext is shared mutable state for the agent loop.
// Passed to slash command handlers and event bridges.
type ExtensionContext struct {
	UI      ExtensionUIContext
	HasUI   bool
	CWD     string
	Session *Session
	Model   *ai.Model
	IsIdle  func() bool
	// IsProjectTrusted mirrors upstream ExtensionContext.isProjectTrusted
	// (types.ts:332). Trust can be granted mid-session, so it is a function
	// rather than a snapshot.
	IsProjectTrusted func() bool
	AbortSignal      context.Context
	AbortFunc        context.CancelFunc
}

// ─── Slash Command ────────────────────────────────────────────────────────────

// SlashCommand is a command registered via /name.
type SlashCommand struct {
	Name        string
	Description string
	Handler     func(ctx *ExtensionContext, args string) error
}

// ─── TUI-backed UI ───────────────────────────────────────────────────────────

// TUIUIContext implements ExtensionUIContext for interactive mode.
type TUIUIContext struct {
	tui    tui.Renderer
	mu     sync.Mutex
	status map[string]string

	// interactiveMode is set by InteractiveMode.Run() to enable real
	// Select/Input/Confirm implementations that use the editor slot.
	interactiveMode *InteractiveMode

	// NotifyFunc receives styled notification text when no interactive mode is attached.
	NotifyFunc func(message string)
}

// NewTUIUIContext creates a TUI-backed ExtensionUIContext.
func NewTUIUIContext(t tui.Renderer) *TUIUIContext {
	return &TUIUIContext{tui: t, status: make(map[string]string)}
}

// renderer returns the renderer to paint through. When wired to an
// InteractiveMode it resolves m.tuiInst dynamically, so a live tui-mode switch
// (switchTuiMode) that replaces the renderer paints through the new one rather
// than the stopped renderer captured at construction. This is pig's equivalent
// of upstream's createInteractiveTuiReference stable reference for the extension
// UI context. Falls back to the captured renderer when standalone (tests).
// withRenderer runs op against the current renderer. When wired to an
// InteractiveMode it resolves m.tuiInst dynamically AND holds m.rendererMu across
// op, so a live tui-mode switch (which holds the write lock across stop+swap)
// cannot let an extension-goroutine paint land on a renderer after its
// preserve-screen stop. This is pig's race-safe equivalent of upstream's
// createInteractiveTuiReference stable reference for the extension UI context.
// Falls back to the captured renderer when standalone (tests).
func (u *TUIUIContext) withRenderer(op func(tui.Renderer)) {
	if u.interactiveMode != nil {
		u.interactiveMode.rendererMu.RLock()
		defer u.interactiveMode.rendererMu.RUnlock()
		op(u.interactiveMode.tuiInst)
		return
	}
	op(u.tui)
}

func (u *TUIUIContext) renderNow() {
	if u.interactiveMode != nil {
		defer func() {
			if value := recover(); value != nil {
				u.interactiveMode.forwardRenderCrash(value)
			}
		}()
	}
	u.withRenderer(tui.Renderer.Render)
}
func (u *TUIUIContext) requestRender() { u.withRenderer(tui.Renderer.RequestRender) }

func (u *TUIUIContext) Select(title string, options []string) (string, bool) {
	if u.interactiveMode != nil && u.interactiveMode.layout != nil {
		sel := tui.NewExtensionSelector(title, options)
		idx, ok := u.interactiveMode.runEditorSlotExtensionSelector(sel)
		if !ok || idx < 0 || idx >= len(options) {
			return "", false
		}
		return options[idx], true
	}
	return "", false
}

func (u *TUIUIContext) Confirm(title, message string) bool {
	choice, ok := u.Select(title+": "+message, []string{"Yes", "No"})
	return ok && choice == "Yes"
}

func (u *TUIUIContext) Input(title, placeholder string) (string, bool) {
	if u.interactiveMode != nil && u.interactiveMode.layout != nil {
		input := tui.NewExtensionInputComponent(title, placeholder)

		u.interactiveMode.layout.Remove(u.interactiveMode.editor)
		u.interactiveMode.layout.Add(input)
		u.renderNow()

		defer func() {
			u.interactiveMode.layout.Remove(input)
			u.interactiveMode.layout.Add(u.interactiveMode.editor)
			u.renderNow()
		}()

		inputCh, releaseInput := u.interactiveMode.acquireModalInputChannel()
		defer releaseInput()
		for !input.Done() {
			buf := <-inputCh
			for _, chunk := range dropKeyReleases(input, []string{string(buf)}) {
				input.HandleInput(chunk)
				if input.Done() {
					break
				}
			}
			if !input.Done() {
				u.renderNow()
			}
		}

		if input.Cancelled() {
			return "", false
		}
		return input.Text(), true
	}
	return "", false
}

func (u *TUIUIContext) Notify(message, level string) {
	if u.interactiveMode != nil {
		(&ExtUIContext{m: u.interactiveMode}).Notify(message, level)
		return
	}
	if message == "" {
		return
	}
	prefix := ""
	switch level {
	case "error":
		prefix = "\033[31m❌ "
	case "warning":
		prefix = "\033[33m⚠️  "
	default:
		prefix = ""
	}
	suffix := ""
	if level == "error" || level == "warning" {
		suffix = "\033[0m"
	}
	if u.NotifyFunc != nil {
		u.NotifyFunc(prefix + message + suffix)
	}
}

func (u *TUIUIContext) SetStatus(key, text string) {
	u.mu.Lock()
	u.status[key] = text
	u.mu.Unlock()
	// Forward to the StatusLine so the footer renders extension statuses.
	if u.interactiveMode != nil && u.interactiveMode.statusLine != nil {
		u.interactiveMode.statusLine.SetExtensionStatus(key, text)
	}
	u.requestRender()
}

func (u *TUIUIContext) SetWidget(key string, lines []string) {
	u.requestRender()
}

// Ensure interface satisfaction
var _ ExtensionUIContext = (*TUIUIContext)(nil)
var _ ExtensionUIContext = NoopUI{}
