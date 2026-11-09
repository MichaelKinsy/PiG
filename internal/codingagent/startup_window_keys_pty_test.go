//go:build linux

package codingagent

import (
	"os"
	"slices"
	"testing"
)

const startupWindowKeysChildEnv = "PIG_TEST_STARTUP_WINDOW_KEYS_CHILD"

// interactive-mode.ts:1013-1018 init() binds app.clear to the Ctrl-C handler, Ctrl-D to the exit handler and submit to handleStartupSubmit on the default editor
// before the UI starts, and setupKeyHandlers binds the remaining app actions only after the startup header and managed-tool setup (1017-1028). Until then the
// CustomEditor passes every other key to the editor, so Ctrl-O does not expand the tools. Init needs a terminal for raw mode, so the check feeds the real Init's
// startup input in a child on a pseudo-terminal.
func TestInitBindsOnlyTheClearAndExitKeysDuringStartup(t *testing.T) {
	runInitPTYChild(t, "TestInitBindsOnlyTheClearAndExitKeysDuringStartupChild", startupWindowKeysChildEnv)
}

func TestInitBindsOnlyTheClearAndExitKeysDuringStartupChild(t *testing.T) {
	report := os.Getenv(startupWindowKeysChildEnv)
	if report == "" {
		t.Skip("runs only as the pty child of TestInitBindsOnlyTheClearAndExitKeysDuringStartup")
	}
	// The production startup this child runs selects a theme and replaces the registry; TestMain fails a binary that leaves them.
	restoreStartupTheme(t)
	t.Cleanup(func() {
		if t.Failed() {
			_ = os.WriteFile(report, []byte(childFailure), 0o600)
		}
	})
	m := NewInteractiveMode(nil, InteractiveModeOptions{CWD: t.TempDir(), AgentDir: t.TempDir(), NoThemes: true})
	feed := func(keys ...string) {
		for _, key := range keys {
			m.handleStartupInput(inputChunk{data: []byte(key)}, false)
		}
	}
	var marks []string
	m.opts.StartupMark = func(label string) {
		if marks = append(marks, label); label != "tui-started" {
			return
		}
		if m.editor.OnSubmit == nil {
			childFatalf(t, "the editor has no submit handler once the UI started")
		}
		feed("d", "r", "a", "f", "t")
		if got := m.editor.Text(); got != "draft" {
			childFatalf(t, "typed text = %q, want draft", got)
		}
		// Ctrl-D leaves a non-empty editor to the editor (custom-editor.ts: delete-char-forward) and never exits.
		feed("\x04")
		if m.requestExit.Load() || m.editor.Text() != "draft" {
			childFatalf(t, "Ctrl-D with text: exit requested %v, text %q, want neither", m.requestExit.Load(), m.editor.Text())
		}
		// Ctrl-O is the tools-expand action, which setupKeyHandlers binds later: the startup editor ignores it.
		m.toolMu.Lock()
		expanded := m.toolsExpanded
		m.toolMu.Unlock()
		feed("\x0f")
		m.toolMu.Lock()
		changed := m.toolsExpanded != expanded
		m.toolMu.Unlock()
		if changed || m.editor.Text() != "draft" {
			childFatalf(t, "Ctrl-O during startup changed the tools expansion (%v) or the text (%q); setupKeyHandlers has not bound it yet", changed, m.editor.Text())
		}
		// app.clear: the first Ctrl-C clears the draft and does not exit.
		feed("\x03")
		if m.editor.Text() != "" || m.requestExit.Load() {
			childFatalf(t, "first Ctrl-C: text %q, exit requested %v, want the draft cleared and no exit", m.editor.Text(), m.requestExit.Load())
		}
		// The release of the key, which the Kitty keyboard protocol reports, never acts (tui.ts isKeyRelease).
		feed("\x1b[99;5:3u")
		if m.requestExit.Load() {
			childFatalf(t, "the release of Ctrl-C acted as a second Ctrl-C")
		}
		// A second Ctrl-C within 500 ms exits (handleCtrlC).
		feed("\x03")
		if !m.requestExit.Load() {
			childFatalf(t, "a second Ctrl-C right after the first did not request the exit")
		}
		m.requestExit.Store(false)
		// app.exit on the empty editor exits (handleCtrlD).
		feed("\x04")
		if !m.requestExit.Load() {
			childFatalf(t, "Ctrl-D with an empty editor did not request the exit")
		}
		m.requestExit.Store(false)
	}
	if err := m.Init(t.Context()); err == nil {
		childFatalf(t, "Init without a SessionHandle succeeded")
	}
	if !slices.Contains(marks, "tui-started") {
		childFatalf(t, "marks %q: the UI never started", marks)
	}
	// Startup completed: the remaining app actions are bound and the editor takes the application keys as before.
	for _, action := range []AppKeybinding{AppClear, AppToolsExpand, AppThinkingCycle, AppModelSelect} {
		if _, ok := m.customEditor.ActionHandler(action); !ok {
			childFatalf(t, "the default editor has no handler for %s after startup", action)
		}
	}
	m.toolMu.Lock()
	expanded := m.toolsExpanded
	m.toolMu.Unlock()
	if err := m.handleCustomEditorKey(t.Context(), "\x0f"); err != nil {
		childFatalf(t, "%v", err)
	}
	m.toolMu.Lock()
	changed := m.toolsExpanded != expanded
	m.toolMu.Unlock()
	if !changed {
		childFatalf(t, "Ctrl-O after startup did not toggle the tools expansion")
	}
	if err := m.Stop(); err != nil {
		childFatalf(t, "%v", err)
	}
}

// A quit the startup keys request ends init() once the startup window closes: the session is not bound and run() takes the exit.
func TestInitEndsWhenAStartupKeyQuits(t *testing.T) {
	runInitPTYChild(t, "TestInitEndsWhenAStartupKeyQuitsChild", startupQuitChildEnv)
}

const startupQuitChildEnv = "PIG_TEST_STARTUP_QUIT_CHILD"

func TestInitEndsWhenAStartupKeyQuitsChild(t *testing.T) {
	report := os.Getenv(startupQuitChildEnv)
	if report == "" {
		t.Skip("runs only as the pty child of TestInitEndsWhenAStartupKeyQuits")
	}
	// The production startup this child runs selects a theme and replaces the registry; TestMain fails a binary that leaves them.
	restoreStartupTheme(t)
	t.Cleanup(func() {
		if t.Failed() {
			_ = os.WriteFile(report, []byte(childFailure), 0o600)
		}
	})
	m := NewInteractiveMode(nil, InteractiveModeOptions{CWD: t.TempDir(), AgentDir: t.TempDir(), NoThemes: true})
	m.opts.StartupMark = func(label string) {
		if label == "terminal-colors-ready" {
			m.handleStartupInput(inputChunk{data: []byte("\x04")}, false)
		}
	}
	// Without a SessionHandle a started bind fails, so a nil result shows the bind never began.
	if err := m.Init(t.Context()); err != nil {
		childFatalf(t, "Init after a quit key = %v, want it to end without binding the session", err)
	}
	if m.slashRegistry != nil || !m.requestExit.Load() {
		childFatalf(t, "slash registry %v, exit requested %v: Init went on past the quit", m.slashRegistry != nil, m.requestExit.Load())
	}
	if err := m.Stop(); err != nil {
		childFatalf(t, "%v", err)
	}
}

// A quit the startup keys request ends the session before any startup work: run() takes the exit instead of submitting the initial message to the session Init never
// bound (a slash command met the unset slash registry and crashed; a plain message started a prompt on the unbound session).
func TestRunDoesNotSubmitTheInitialMessageAfterAStartupQuit(t *testing.T) {
	t.Run("slash command", func(t *testing.T) {
		runInitPTYChild(t, "TestRunStartupQuitWithASlashCommandChild", startupQuitSlashChildEnv)
	})
	t.Run("plain message", func(t *testing.T) {
		runInitPTYChild(t, "TestRunStartupQuitWithAPlainMessageChild", startupQuitPlainChildEnv)
	})
}

const (
	startupQuitSlashChildEnv = "PIG_TEST_STARTUP_QUIT_SLASH_CHILD"
	startupQuitPlainChildEnv = "PIG_TEST_STARTUP_QUIT_PLAIN_CHILD"
)

func TestRunStartupQuitWithASlashCommandChild(t *testing.T) {
	runStartupQuitWithInitialMessageChild(t, startupQuitSlashChildEnv, "/hello")
}

func TestRunStartupQuitWithAPlainMessageChild(t *testing.T) {
	runStartupQuitWithInitialMessageChild(t, startupQuitPlainChildEnv, "hello")
}

// runStartupQuitWithInitialMessageChild runs Run with an initial message and a Ctrl-D at the end of the colour wait. Without a SessionHandle any submission that
// reaches the session fails, so a clean return shows the message never left the host.
func runStartupQuitWithInitialMessageChild(t *testing.T, envKey, message string) {
	report := os.Getenv(envKey)
	if report == "" {
		t.Skip("runs only as a pty child")
	}
	// The production startup this child runs selects a theme and replaces the registry; TestMain fails a binary that leaves them.
	restoreStartupTheme(t)
	t.Cleanup(func() {
		if t.Failed() {
			_ = os.WriteFile(report, []byte(childFailure), 0o600)
		}
	})
	m := NewInteractiveMode(nil, InteractiveModeOptions{CWD: t.TempDir(), AgentDir: t.TempDir(), NoThemes: true, InitialMessage: message})
	m.opts.StartupMark = func(label string) {
		if label == "terminal-colors-ready" {
			m.handleStartupInput(inputChunk{data: []byte("\x04")}, false)
		}
	}
	if err := m.Run(t.Context()); err != nil {
		childFatalf(t, "Run with the initial message %q and a startup Ctrl-D = %v, want a clean exit", message, err)
	}
	if !m.requestExit.Load() {
		childFatalf(t, "the startup Ctrl-D did not request the exit")
	}
	if m.slashRegistry != nil || m.preflight != nil || len(m.preflightQueue) != 0 || m.turnActive.Load() {
		childFatalf(t, "the initial message %q reached the submission path after the quit", message)
	}
}
