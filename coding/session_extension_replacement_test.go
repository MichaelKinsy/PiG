package coding

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// agent-session-runtime.ts:197-353 awaits before hooks, abort and shutdown,
// installs the destination, then emits session_start. A cancelled hook keeps
// both identity and history. fork defaults to before a user message; at accepts
// any entry. Command contexts return only cancelled in all modes.
func TestExtensionSessionReplacementActions(t *testing.T) {
	var trace []string
	cancel := false
	runner := inproc.NewRunner([]extension.Extension{{Path: "/replacement", Handlers: map[string][]extension.HandlerFn{
		"session_before_switch": {func(args ...any) (any, error) {
			event := args[0].(extension.SessionBeforeSwitchEvent)
			trace = append(trace, "before:"+event.Reason)
			return map[string]any{"cancel": cancel}, nil
		}},
		"session_before_fork": {func(args ...any) (any, error) {
			event := args[0].(extension.SessionBeforeForkEvent)
			trace = append(trace, "before:fork:"+event.Position)
			return map[string]any{"cancel": cancel}, nil
		}},
		"session_shutdown": {func(args ...any) (any, error) {
			trace = append(trace, "shutdown:"+args[0].(extension.SessionShutdownEvent).Reason)
			return nil, nil
		}},
		"session_start": {func(args ...any) (any, error) {
			trace = append(trace, "start:"+args[0].(extension.SessionStartEvent).Reason)
			return nil, nil
		}},
	}}}, t.TempDir())
	sess, err := NewSession(newTestServices(t), SessionOptions{Model: fakeModel(), Runner: runner, SessionDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sess.Close() })
	drainSessionEvents(t, sess)
	first := appendUser(t, sess, "first")
	appendAsst(t, sess, "reply")
	original, originalID := sess.Path(), sess.ID()
	commands := runner.CreateCommandContext()

	cancel = true
	result, err := commands.NewSession(nil)
	if err != nil || !result.Cancelled || sess.ID() != originalID {
		t.Fatalf("cancel = %+v, %v, id=%s", result, err, sess.ID())
	}
	if !slices.Equal(trace, []string{"before:new"}) {
		t.Fatalf("cancel trace = %v", trace)
	}
	cancel, trace = false, nil
	result, err = commands.Fork(first, &extension.ForkOptions{Position: "at"})
	if err != nil || result.Cancelled || sess.ID() == originalID {
		t.Fatalf("fork = %+v, %v, id=%s", result, err, sess.ID())
	}
	if leaf := sess.LeafID(); leaf == nil || *leaf != first {
		t.Fatalf("fork at leaf = %v", leaf)
	}
	if !slices.Equal(trace, []string{"before:fork:at", "shutdown:fork", "start:fork"}) {
		t.Fatalf("fork trace = %v", trace)
	}
	trace = nil
	result, err = commands.SwitchSession(original, nil)
	if err != nil || result.Cancelled || sess.ID() != originalID {
		t.Fatalf("resume = %+v, %v, id=%s", result, err, sess.ID())
	}
	if !slices.Equal(trace, []string{"before:resume", "shutdown:resume", "start:resume"}) {
		t.Fatalf("resume trace = %v", trace)
	}
	trace = nil
	result, err = commands.NewSession(&extension.NewSessionOptions{ParentSession: original})
	if err != nil || result.Cancelled || sess.ID() == originalID {
		t.Fatalf("new = %+v, %v, id=%s", result, err, sess.ID())
	}
	if !slices.Equal(trace, []string{"before:new", "shutdown:new", "start:new"}) {
		t.Fatalf("new trace = %v", trace)
	}
	if result, err := sess.ExtensionCommandActions().ForkContext(context.Background(), "missing", nil); err == nil || result.Cancelled {
		t.Fatalf("invalid fork = %+v, %v", result, err)
	}
	before := sess.ID()
	cancelledContext, cancelContext := context.WithCancel(t.Context())
	cancelContext()
	if _, err := sess.ExtensionCommandActions().NewSessionContext(cancelledContext, nil); !errors.Is(err, context.Canceled) || sess.ID() != before {
		t.Fatalf("cancelled call changed session or lost error: %v", err)
	}
	active, endRun := sess.beginAgentRun(t.Context())
	done := make(chan error, 1)
	go func() {
		_, err := sess.ExtensionCommandActions().NewSessionContext(t.Context(), nil)
		done <- err
	}()
	<-active.Done()
	select {
	case err := <-done:
		t.Fatalf("replacement returned before outgoing run drained: %v", err)
	default:
	}
	endRun()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestExtensionNewSessionKeepsParentInMemory(t *testing.T) {
	sess, err := NewSession(newTestServices(t), SessionOptions{NoSession: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sess.Close() })
	result, err := sess.ExtensionCommandActions().NewSessionContext(t.Context(), &extension.NewSessionOptions{ParentSession: "/parent.jsonl"})
	if err != nil || result.Cancelled {
		t.Fatalf("newSession = %+v, %v", result, err)
	}
	if sess.Path() != "" || sess.Inner().ParentSession() != "/parent.jsonl" {
		t.Fatalf("in-memory header lost parent: %+v", sess.Inner().Header())
	}
}
