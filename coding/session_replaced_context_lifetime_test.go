package coding

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// Pi's createReplacedSessionContext (agent-session.ts:4375-4383) hands the callback a command context and the Session's own awaited
// sendCustomMessage and sendUserMessage. Go carries the AbortSignal-style request lifetime in the context CreateReplacedSessionContext receives:
// cancelling it stops the turn a replaced-context send started, and the command context the callback holds waits on that same lifetime.
func TestCreateReplacedSessionContextFollowsTheRequestLifetime(t *testing.T) {
	sends := map[string]func(*extension.ReplacedSessionContext) error{
		"sendMessage": func(replaced *extension.ReplacedSessionContext) error {
			return replaced.SendMessage(extension.CustomMessageRef{CustomType: "note", Content: "wake", Display: true}, &extension.SendMessageOptions{TriggerTurn: new(true)})
		},
		"sendUserMessage": func(replaced *extension.ReplacedSessionContext) error {
			return replaced.SendUserMessage("hello", nil)
		},
	}
	for name, send := range sends {
		t.Run(name+" stops with the cancelled lifetime and the command context shares it", func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				started := make(chan struct{})
				h := newRuntimeTestHarness(t, runtimeTestOptions{tools: []agent.AgentTool{runtimeAbortTool{started: started}}})
				h.provider.responses = []scriptedResponse{fauxToolCall("block")}
				bindReplacementCommands(t, h.runtime)
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				replaced := h.runtime.Session().CreateReplacedSessionContext(ctx)
				done := make(chan error, 1)
				go func() { done <- send(replaced) }()
				<-started
				synctest.Wait()
				select {
				case err := <-done:
					t.Fatalf("%s returned before the turn settled or the lifetime ended: %v", name, err)
				default:
				}
				waited := make(chan error, 1)
				go func() { waited <- replaced.WaitForIdle() }()
				synctest.Wait()
				cancel()
				synctest.Wait()
				select {
				case err := <-waited:
					if !errors.Is(err, context.Canceled) {
						t.Errorf("WaitForIdle after cancelling the lifetime = %v, want context.Canceled", err)
					}
				default:
					t.Error("the command context's WaitForIdle did not follow the cancelled lifetime")
				}
				select {
				case <-done:
				default:
					h.runtime.Session().RequestAbort()
					<-done
					t.Errorf("%s kept running after its lifetime was cancelled", name)
				}
			})
		})
	}
}

// agent-session.ts:4378 copies `this._extensionRunner.createCommandContext()`: the session's own runner supplies the context and stays bound.
// A Session that has no runner yet binds one first (Go has no always-present runner), and a later call reuses it.
func TestCreateReplacedSessionContextKeepsTheBoundRunner(t *testing.T) {
	h := newRuntimeTestHarness(t, runtimeTestOptions{})
	bindReplacementCommands(t, h.runtime)
	session := h.runtime.Session()
	bound := session.currentRunner()
	if bound == nil {
		t.Fatal("the harness bound no runner")
	}
	session.CreateReplacedSessionContext(t.Context())
	if got := session.currentRunner(); got != bound {
		t.Fatalf("CreateReplacedSessionContext replaced the bound runner %p with %p", bound, got)
	}

	fresh := newRuntimeTestHarness(t, runtimeTestOptions{}).runtime.Session()
	fresh.ReplaceRunner(nil)
	if fresh.currentRunner() != nil {
		t.Skip("the Session cannot run without a runner")
	}
	fresh.CreateReplacedSessionContext(t.Context())
	created := fresh.currentRunner()
	if created == nil {
		t.Fatal("a Session without a runner got none")
	}
	fresh.CreateReplacedSessionContext(t.Context())
	if fresh.currentRunner() != created {
		t.Fatal("a second call replaced the runner the first created")
	}
}
