package coding

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

func TestReplacedContextUserMessageAwaitsTheTurn(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		started := make(chan struct{})
		h := newRuntimeTestHarness(t, runtimeTestOptions{tools: []agent.AgentTool{runtimeAbortTool{started: started}}})
		h.provider.responses = []scriptedResponse{fauxToolCall("block")}
		bindReplacementCommands(t, h.runtime)
		done := make(chan error, 1)
		go func() {
			_, err := h.runtime.NewSession(t.Context(), &extension.NewSessionOptions{WithSession: func(ctx *extension.ReplacedSessionContext) error { return ctx.SendUserMessage("wait", nil) }})
			done <- err
		}()
		select {
		case <-started:
		case err := <-done:
			t.Fatalf("message method returned before its turn started: %v", err)
		}
		synctest.Wait()
		select {
		case err := <-done:
			t.Fatalf("message method detached the active turn: %v", err)
		default:
		}
		h.runtime.Session().RequestAbort()
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	})
}

func TestWithSessionAwaitsCallbackAndPropagatesItsFailure(t *testing.T) {
	h := newRuntimeTestHarness(t, runtimeTestOptions{})
	bindReplacementCommands(t, h.runtime)
	entered, release := make(chan struct{}), make(chan struct{})
	failure := errors.New("callback failure")
	done := make(chan error, 1)
	go func() {
		_, err := h.runtime.NewSession(t.Context(), &extension.NewSessionOptions{WithSession: func(*extension.ReplacedSessionContext) error { close(entered); <-release; return failure }})
		done <- err
	}()
	select {
	case <-entered:
	case err := <-done:
		t.Fatalf("replacement skipped callback: %v", err)
	}
	select {
	case err := <-done:
		close(release)
		t.Fatalf("replacement returned before callback: %v", err)
	default:
	}
	close(release)
	if err := <-done; !errors.Is(err, failure) {
		t.Fatalf("callback error=%v", err)
	}
}

func TestWithSessionFollowsSetupAndRebind(t *testing.T) {
	h := newRuntimeTestHarness(t, runtimeTestOptions{})
	var steps []string
	h.runtime.SetRebindSession(func(ctx context.Context, session *Session) error {
		steps = append(steps, "bind")
		if got := replacedConversation(session); len(got) != 1 || got[0] != "user:seeded" {
			return errors.New("setup was not projected before rebind")
		}
		return session.BindExtensions(ctx, ExtensionBindings{})
	})
	_, err := h.runtime.NewSession(t.Context(), &extension.NewSessionOptions{
		Setup: func(manager extension.SessionManager) error {
			steps = append(steps, "setup")
			_, err := manager.(*SessionManager).AppendMessage(agent.AgentMessage{User: &agent.UserMessage{Role: agent.RoleUser, Content: ai.UserContentBlocks{ai.TextContent{Text: "seeded"}}}})
			return err
		},
		WithSession: func(*extension.ReplacedSessionContext) error { steps = append(steps, "callback"); return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 3 || steps[0] != "setup" || steps[1] != "bind" || steps[2] != "callback" {
		t.Fatalf("steps=%v", steps)
	}
}

func TestWithSessionDoesNotRunAfterCancellationOrRebindFailure(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		t.Run(map[bool]string{false: "rebind error", true: "cancelled"}[cancelled], func(t *testing.T) {
			h := newRuntimeTestHarness(t, runtimeTestOptions{extension: func() extension.Extension {
				return extension.Extension{Handlers: map[string][]extension.HandlerFn{"session_before_switch": {func(...any) (any, error) { return extension.SessionBeforeSwitchResult{Cancel: cancelled}, nil }}}}
			}})
			failure := errors.New("bind failure")
			h.runtime.SetRebindSession(func(context.Context, *Session) error { return failure })
			called := false
			result, err := h.runtime.NewSession(t.Context(), &extension.NewSessionOptions{WithSession: func(*extension.ReplacedSessionContext) error { called = true; return nil }})
			if called || result.Cancelled != cancelled {
				t.Fatalf("called=%v result=%v", called, result)
			}
			if cancelled && err != nil || !cancelled && !errors.Is(err, failure) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}
