package coding

import (
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// Pi: packages/coding-agent/src/core/agent-session-runtime.ts:196-207 switchSession. `emitBeforeSwitch("resume", sessionPath)` hands the handlers the reason "resume" and the
// target session file; a handler that cancels ends the switch with { cancelled: true } before the target is opened, so the current Session stays in place. The extension command
// context binds the same behaviour (print-mode and rpc-mode command actions).
// mutation-checked: the mutant "switchSession returns not-cancelled when a handler cancels" (session_extension_replacement.go:111) fails it.
// mutation-checked: the mutant "session_before_switch for resume carries no target session file" (session_extension_replacement.go:109) fails it.
func TestExtensionSwitchSessionCancelledByAHandlerKeepsTheCurrentSession(t *testing.T) {
	var events []extension.SessionBeforeSwitchEvent
	cancel := true
	runner := inproc.NewRunner([]extension.Extension{{Path: "/switch-cancel", Handlers: map[string][]extension.HandlerFn{
		"session_before_switch": {func(args ...any) (any, error) {
			events = append(events, args[0].(extension.SessionBeforeSwitchEvent))
			return map[string]any{"cancel": cancel}, nil
		}},
	}}}, t.TempDir())
	sess, err := NewSession(newTestServices(t), SessionOptions{Model: fakeModel(), Runner: runner, SessionDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sess.Close() })
	drainSessionEvents(t, sess)
	appendUser(t, sess, "first")
	appendAsst(t, sess, "reply")
	original := sess.Path()
	commands := runner.CreateCommandContext()

	cancel = false
	if result, err := commands.NewSession(nil); err != nil || result.Cancelled {
		t.Fatalf("new = %+v, %v", result, err)
	}
	second, secondID := sess.Path(), sess.ID()
	if second == original {
		t.Fatal("NewSession kept the original Session")
	}

	events, cancel = nil, true
	result, err := commands.SwitchSession(original, nil)
	if err != nil || !result.Cancelled {
		t.Fatalf("SwitchSession with a cancelling handler = %+v, %v; want cancelled", result, err)
	}
	if sess.Path() != second || sess.ID() != secondID {
		t.Fatalf("a cancelled switch replaced the Session: path %q id %q", sess.Path(), sess.ID())
	}
	if len(events) != 1 || events[0].Reason != "resume" || events[0].TargetSessionFile != original {
		t.Fatalf("session_before_switch events = %+v, want one resume event targeting %q", events, original)
	}
}
