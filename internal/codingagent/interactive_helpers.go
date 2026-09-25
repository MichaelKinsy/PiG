package codingagent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/coding/extension"
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

func (m *InteractiveMode) formatLoadedResourceRef(path, display string) string {
	if display == "" {
		display = filepath.Base(path)
	}
	info, ok := m.resourceSourceInfo[path]
	if !ok {
		return fmt.Sprintf("%s: %s", display, shortenPath(path))
	}
	label := info.Source
	if info.Origin == "top-level" || label == "" {
		label = "local"
	}
	short := shortenPath(path)
	if info.BaseDir != "" {
		if rel, err := filepath.Rel(info.BaseDir, path); err == nil && rel != "." {
			short = filepath.ToSlash(rel)
		}
	}
	if info.Scope != "" {
		return fmt.Sprintf("%s: %s (%s) %s", display, label, info.Scope, short)
	}
	return fmt.Sprintf("%s: %s %s", display, label, short)
}

func (m *InteractiveMode) resourceCollisionDiagnostics() []string {
	var out []string
	out = append(out, m.collisionDiagnosticsForSkills()...)
	out = append(out, m.collisionDiagnosticsForPrompts()...)
	return out
}

func (m *InteractiveMode) collisionDiagnosticsForSkills() []string {
	groups := map[string][]ResourceSourceInfo{}
	for _, info := range m.resourceSourceInfo {
		if info.ResourceType == "skills" && info.Enabled {
			groups[info.DisplayName()] = append(groups[info.DisplayName()], info)
		}
	}
	winners := map[string]string{}
	for _, s := range m.opts.Skills {
		winners[s.Name] = s.Path
	}
	return m.buildCollisionDiagnostics("skill", groups, winners)
}

func (m *InteractiveMode) collisionDiagnosticsForPrompts() []string {
	groups := map[string][]ResourceSourceInfo{}
	for _, info := range m.resourceSourceInfo {
		if info.ResourceType == "prompts" && info.Enabled {
			groups[info.DisplayName()] = append(groups[info.DisplayName()], info)
		}
	}
	winners := map[string]string{}
	for _, p := range m.promptTemplates {
		winners[p.Name] = p.FilePath
	}
	return m.buildCollisionDiagnostics("prompt", groups, winners)
}

func (m *InteractiveMode) buildCollisionDiagnostics(kind string, groups map[string][]ResourceSourceInfo, winners map[string]string) []string {
	keys := make([]string, 0, len(groups))
	for name, infos := range groups {
		if len(infos) > 1 && winners[name] != "" {
			keys = append(keys, name)
		}
	}
	slices.Sort(keys)
	out := make([]string, 0, len(keys))
	for _, name := range keys {
		winnerPath := winners[name]
		infos := groups[name]
		var winner *ResourceSourceInfo
		losers := make([]ResourceSourceInfo, 0, len(infos)-1)
		for i := range infos {
			if infos[i].Path == winnerPath {
				winner = &infos[i]
				continue
			}
			losers = append(losers, infos[i])
		}
		if winner == nil || len(losers) == 0 {
			continue
		}
		var msg strings.Builder
		fmt.Fprintf(&msg, "[%s] %q collision: ✓ %s", kind, name, m.formatLoadedResourceRef(winner.Path, name))
		for _, loser := range losers {
			fmt.Fprintf(&msg, "; ✗ %s (skipped)", m.formatLoadedResourceRef(loser.Path, name))
		}
		out = append(out, msg.String())
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

// debugLog writes to /tmp/pig-debug.log when PIG_DEBUG is set. Used for
// diagnosing TUI event flow without polluting the alt-screen.
func debugLog(format string, args ...any) {
	if os.Getenv("PIG_DEBUG") == "" {
		return
	}
	f, err := os.OpenFile("/tmp/pig-debug.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()
	_, _ = fmt.Fprintf(f, "[%s] ", time.Now().Format("15:04:05.000"))
	_, _ = fmt.Fprintf(f, format+"\n", args...)
}

// showWarning appends a padded, theme-colored warning with Pi's textual prefix.
func (m *InteractiveMode) showWarning(msg string) {
	m.appendChatBlock(tui.NewPaddedText(tui.ActiveTheme().FgText("warning", "Warning: "+msg), 1, 0, nil))
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

// addKeyPressListener registers a terminal-input listener that sees presses
// only, and is the registration every in-tree consumer with press-once
// semantics should use.
//
// Terminal-input listeners are raw by design, mirroring upstream's
// addInputListener: an extension may legitimately want releases, and upstream's
// own space-invaders and doom examples set wantsKeyRelease to get them. But the
// consumers pig ships: the footer games: all want a keypress to act once, and
// leaving that to each of them means each must remember, in a codebase where
// forgetting is silent. Only one of the three did; the other two were saved
// only by matching exact byte literals, which a switch to MatchesKeyID would
// have quietly undone.
//
// Registering the filter here keeps the raw path intact for extensions while
// giving in-tree consumers one place that cannot be forgotten.
func (m *InteractiveMode) addKeyPressListener(handler func(string) bool) func() {
	return m.addTerminalInputListener(func(data string) bool {
		if tui.IsKeyRelease(data) {
			return false
		}
		return handler(data)
	})
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
