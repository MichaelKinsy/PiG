package codingagent

import (
	"context"
	"errors"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/coding/extension/host/invocation"
	"github.com/MichaelKinsy/PiG/tui"
)

// Pi interactive-mode.ts:2940-2956 appends extension errors and stack frames without inserting spacer components.
func TestExtensionErrorHasNoAddedSpacers(t *testing.T) {
	m := newExtensionErrorProbe(t)
	m.chatContainer.Add(tui.NewText("before"))
	m.queueExtensionError(&extension.ExtensionError{ExtensionPath: "fixture", Error: "failed", Stack: "failed\nfirst-frame\nsecond-frame"})
	m.showPendingExtensionErrors()
	m.chatContainer.Add(tui.NewText("after"))
	lines := strings.Split(chatPlainText(m), "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " ")
	}
	got := strings.Join(lines, "\n")
	want := "before\n Extension \"fixture\" error: failed\n   first-frame\n   second-frame\nafter"
	if got != want {
		t.Fatalf("extension error block = %q, want %q", got, want)
	}
}

func TestInteractiveShortcutDiagnosticOwnership(t *testing.T) {
	for _, lifecycle := range []bool{false, true} {
		t.Run(map[bool]string{false: "handler-error", true: "lifecycle-error"}[lifecycle], func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				m := newExtensionErrorProbe(t)
				m.runCtx = t.Context()
				failure := errors.New("connection closed")
				if lifecycle {
					failure = &invocation.LifecycleError{Err: failure}
				}
				m.newRunner = inproc.NewRunner([]extension.Extension{{Shortcuts: map[extension.KeyID]extension.ExtensionShortcut{
					"ctrl+shift+left": {Shortcut: "ctrl+shift+left", Handler: func(context.Context) error { return failure }},
				}}}, t.TempDir())
				m.setupExtensionShortcutListener(t.Context())
				if !m.notifyTerminalInput("\x1b[1;6D") {
					t.Fatal("shortcut was not consumed")
				}
				synctest.Wait()
				for len(m.uiTaskCh) > 0 {
					(<-m.uiTaskCh)()
				}
				got := chatPlainText(m)
				if lifecycle && strings.TrimSpace(got) != "" {
					t.Fatalf("duplicate lifecycle diagnostic: %s", got)
				}
				if !lifecycle && strings.Count(got, "Shortcut handler error: connection closed") != 1 {
					t.Fatalf("ordinary shortcut error lost or duplicated: %s", got)
				}
			})
		})
	}
}

// Pi agent-session.ts:1778-1788 sends command failures through the extension runner, without a stack. The interactive bridge must use that same diagnostic owner.
func TestInteractiveCommandDiagnosticOwnership(t *testing.T) {
	for _, lifecycle := range []bool{false, true} {
		t.Run(map[bool]string{false: "handler-error", true: "lifecycle-error"}[lifecycle], func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				m := newExtensionErrorProbe(t)
				m.runCtx = t.Context()
				m.slashRegistry = NewSlashRegistry()
				failure := errors.New("connection closed")
				handlerError := failure
				if lifecycle {
					handlerError = &invocation.LifecycleError{Err: failure}
				}
				m.newRunner = inproc.NewRunner([]extension.Extension{{Commands: map[string]extension.RegisteredCommand{
					"fail": {Name: "fail", Handler: func(context.Context, string) error { return handlerError }},
				}}}, t.TempDir())
				m.wireInprocContextActions()
				m.syncExtensionSlashCommands()
				if err := m.slashRegistry.Dispatch(&SlashContext{}, "/fail", &ExtensionContext{}); err != nil {
					t.Fatal(err)
				}
				synctest.Wait()
				for len(m.uiTaskCh) > 0 {
					(<-m.uiTaskCh)()
				}
				m.showPendingExtensionErrors()
				got := strings.TrimSpace(chatPlainText(m))
				want := `Extension "command:fail" error: connection closed`
				if lifecycle {
					want = ""
				}
				if got != want {
					t.Fatalf("command diagnostic = %q, want %q", got, want)
				}
			})
		})
	}
}
