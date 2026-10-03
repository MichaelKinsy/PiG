package codingagent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/invocation"
	"github.com/MichaelKinsy/PiG/tui"
)

// ─── Helpers ──────────────────────────────────────────────────────────────────

// filterAllowedTools returns tools whose Name() is in allow. Other tools
// are dropped from the slice (so the LLM never sees them in schemas, and
// thus can't call them).
func filterAllowedTools(in []agent.AgentTool, allow map[string]struct{}) []agent.AgentTool {
	out := make([]agent.AgentTool, 0, len(in))
	for _, t := range in {
		if _, ok := allow[t.Name()]; ok {
			out = append(out, t)
		}
	}
	return out
}

// shortenPath returns a `~/foo` form for paths under $HOME, otherwise
// the path unchanged. Used in the interactive banner.
func shortenPath(p string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return p
	}
	if p == home {
		return "~"
	}
	// Upstream formatDisplayPath shortens any path that starts with home, so
	// on Windows a / after the home prefix counts as well as \.
	if strings.HasPrefix(p, home) && len(p) > len(home) && os.IsPathSeparator(p[len(home)]) {
		return "~" + p[len(home):]
	}
	return p
}

// debugLog writes to pig-debug.log in the temporary directory when PIG_DEBUG is set. Used for
// diagnosing TUI event flow without polluting the alt-screen.
func debugLog(format string, args ...any) {
	if os.Getenv("PIG_DEBUG") == "" {
		return
	}
	f, err := os.OpenFile(filepath.Join(os.TempDir(), "pig-debug.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()
	_, _ = fmt.Fprintf(f, "[%s] ", time.Now().Format("15:04:05.000"))
	_, _ = fmt.Fprintf(f, format+"\n", args...)
}

// showWarning appends a padded, theme-colored warning with Pi's textual prefix.
func (m *InteractiveMode) showWarning(msg string) {
	m.appendChatBlock(themedNotice("warning", "Warning: "+msg, 1))
	m.tuiInst.Render()
}

// addTerminalInputListener registers a raw terminal input listener.
// Returns an unsubscribe function. Mirrors upstream
// addExtensionTerminalInputListener (interactive-mode.ts:1848-1858).
func (m *InteractiveMode) addTerminalInputListener(handler func(string) bool) func() {
	return m.addTerminalInputHandler(func(data string) extension.TerminalInputResult {
		return extension.TerminalInputResult{Consume: handler(data)}
	})
}

// addTerminalInputHandler registers a listener that may also rewrite the
// input, as upstream's TerminalInputHandler result `data` does.
func (m *InteractiveMode) addTerminalInputHandler(handler func(string) extension.TerminalInputResult) func() {
	return m.addTerminalInputListenerEntry(terminalInputListener{handler: handler})
}

// addTerminalInputListenerEntry appends listener in registration order and
// returns its unsubscribe function.
func (m *InteractiveMode) addTerminalInputListenerEntry(listener terminalInputListener) func() {
	m.terminalInputMu.Lock()
	m.terminalInputListenerID++
	id := m.terminalInputListenerID
	listener.id = id
	m.terminalInputListeners = append(m.terminalInputListeners, listener)
	m.terminalInputMu.Unlock()
	return func() {
		m.terminalInputMu.Lock()
		defer m.terminalInputMu.Unlock()
		for i, listener := range m.terminalInputListeners {
			if listener.id == id {
				m.terminalInputListeners = append(m.terminalInputListeners[:i], m.terminalInputListeners[i+1:]...)
				break
			}
		}
	}
}

// addKeyPressListener registers a built-in press-only shortcut. Raw extension listeners retain release events and can accept input during an awaited extension UI phase; a built-in shortcut does not enable early editor input.
func (m *InteractiveMode) addKeyPressListener(handler func(string) bool) func() {
	return m.addTerminalInputListenerEntry(terminalInputListener{builtin: true, handler: func(data string) extension.TerminalInputResult {
		if tui.IsKeyRelease(data) {
			return extension.TerminalInputResult{}
		}
		return extension.TerminalInputResult{Consume: handler(data)}
	}})
}

// setupExtensionShortcutListener binds extension shortcuts from the current
// runner to raw terminal input.
func (m *InteractiveMode) setupExtensionShortcutListener(ctx context.Context) {
	m.terminalInputMu.Lock()
	defer m.terminalInputMu.Unlock()
	m.extensionShortcutListener = nil
	if m.newRunner == nil {
		return
	}
	shortcuts := m.newRunner.Shortcuts(m.keybindings.ResolvedBindings())
	if len(shortcuts) == 0 {
		return
	}
	m.extensionShortcutListener = func(data string) bool {
		for keyID, sc := range shortcuts {
			if tui.MatchesKeyID(data, keyID) {
				go func() {
					if err := sc.Handler(ctx); err != nil {
						// pig divergence (D56): the lifecycle handler reports a failed subprocess connection once, including interrupted shortcuts.
						if _, owned := errors.AsType[*invocation.LifecycleError](err); owned {
							return
						}
						m.runOnMain(m.runCtx, func() {
							m.showWarning(fmt.Sprintf("Shortcut handler error: %v", err))
						})
					}
				}()
				return true
			}
		}
		return false
	}
}

// projectTrusted reports the current project's trust state to extensions.
// Trust can be granted mid-session, so this reads the settings manager each
// time rather than snapshotting. Defaults to trusted when no settings manager
// is bound, matching upstream's unbound default (runner.ts:280).
func (m *InteractiveMode) projectTrusted() bool {
	if m.opts.SettingsManager == nil {
		return true
	}
	return m.opts.SettingsManager.IsProjectTrusted()
}
