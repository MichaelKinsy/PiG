package inproc_test

import (
	"context"
	"errors"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// upstream: runner.ts emitToolCall (:1256), emitToolResult (:1197), emitInput (:1532), emitContext (:1312), emitBeforeProviderRequest (:1375),
// emitBeforeProviderHeaders (:1406), emitMessageEnd (:1158), emitUserBash (:1276), emitBeforeAgentStart (:1434), emitResourcesDiscover (:1486) and
// emitCacheWarmingDecision (:1135) never call assertActive: only the ctx getters and actions do (:883-1037). A session whose reload failed keeps the
// runner session.reload() invalidated (agent-session.ts:3825-3826), so the next prompt, tool call and provider request still reach that runner's
// handlers, which see a stale ctx. Pi's probe of a failed interactive /reload: the next prompt logs input, before_agent_start, agent_start and
// turn_start from the old instance and its tool executes.
func TestEveryEmitDeliversToAnInvalidatedRunner(t *testing.T) {
	background := context.Background()
	cases := []struct {
		event string
		emit  func(*inproc.Runner) error
	}{
		{"tool_call", func(r *inproc.Runner) error {
			_, err := r.EmitToolCall(background, extension.CustomToolCallEvent{})
			return err
		}},
		{"tool_result", func(r *inproc.Runner) error {
			_, err := r.EmitToolResult(background, extension.BashToolResultEvent{})
			return err
		}},
		{"input", func(r *inproc.Runner) error {
			_, err := r.EmitInput(background, "hello", nil, "interactive", "")
			return err
		}},
		{"context", func(r *inproc.Runner) error {
			_, err := r.EmitContext(background, nil)
			return err
		}},
		{"context_with_system", func(r *inproc.Runner) error {
			_, err := r.EmitContextWithSystem(background, nil)
			return err
		}},
		{"before_provider_request", func(r *inproc.Runner) error {
			_, err := r.EmitBeforeProviderRequest(background, nil)
			return err
		}},
		{"before_provider_headers", func(r *inproc.Runner) error {
			_, err := r.EmitBeforeProviderHeaders(background, nil)
			return err
		}},
		{"message_end", func(r *inproc.Runner) error {
			_, err := r.EmitMessageEnd(background, extension.MessageEndEvent{Type: "message_end"})
			return err
		}},
		{"user_bash", func(r *inproc.Runner) error {
			_, err := r.EmitUserBash(background, extension.UserBashEvent{})
			return err
		}},
		{"before_agent_start", func(r *inproc.Runner) error {
			_, err := r.EmitBeforeAgentStart(background, extension.UserPromptTrigger("p", nil), extension.BuildSystemPromptOptions{})
			return err
		}},
		{"resources_discover", func(r *inproc.Runner) error {
			_, err := r.EmitResourcesDiscover(background, ".", "reload")
			return err
		}},
		{"cache_warming_decision", func(r *inproc.Runner) error {
			_, err := r.EmitCacheWarmingDecision(background, extension.CacheWarmingDecisionEvent{Type: "cache_warming_decision", Action: extension.CacheWarmingActionWarm})
			return err
		}},
	}
	for _, c := range cases {
		t.Run(c.event, func(t *testing.T) {
			delivered := 0
			var ctxErr error
			ext := newFakeExtension("/ext/a")
			ext.Handlers[c.event] = []extension.HandlerFn{func(args ...any) (any, error) {
				delivered++
				ctx, _ := args[len(args)-1].(context.Context)
				if extCtx := extension.FromContext(ctx); extCtx != nil {
					_, ctxErr = extCtx.CWD()
				} else {
					ctxErr = errors.New("no extension context")
				}
				return nil, nil
			}}
			r := inproc.NewRunner([]extension.Extension{ext}, ".")
			r.Invalidate("replaced")
			if err := c.emit(r); err != nil {
				t.Fatalf("emit on an invalidated runner: %v; want the delivery upstream makes (the stale error belongs to the ctx)", err)
			}
			if delivered != 1 {
				t.Fatalf("handler delivered %d times, want 1", delivered)
			}
			var stale *inproc.StaleError
			if !errors.As(ctxErr, &stale) || stale.Message != "replaced" {
				t.Fatalf("handler ctx.CWD() err = %v, want the runner's stale message", ctxErr)
			}
		})
	}
}

// A handler of an invalidated runner still decides: the result it returns reaches the caller, as upstream's emitInput and emitToolCall return it.
func TestEmitInputAndToolCallOnAnInvalidatedRunnerHonourTheHandlers(t *testing.T) {
	input := extWithInputHandler("/ext/a", func(extension.InputEvent, context.Context) extension.InputEventResult {
		return extension.InputEventResultHandled{}
	})
	r := inproc.NewRunner([]extension.Extension{input}, ".")
	r.Invalidate("replaced")
	got, err := r.EmitInput(context.Background(), "hello", nil, "interactive", "")
	if err != nil {
		t.Fatalf("EmitInput: %v", err)
	}
	if _, ok := got.(extension.InputEventResultHandled); !ok {
		t.Fatalf("EmitInput = %T, want the handler's InputEventResultHandled", got)
	}

	block := extWithToolCallHandler("/ext/a", func(extension.ToolCallEvent, context.Context) *extension.ToolCallEventResult {
		return &extension.ToolCallEventResult{Block: true, Reason: "blocked by the stale instance"}
	})
	r = inproc.NewRunner([]extension.Extension{block}, ".")
	r.Invalidate("replaced")
	blocked, err := r.EmitToolCall(context.Background(), extension.CustomToolCallEvent{})
	if err != nil {
		t.Fatalf("EmitToolCall: %v", err)
	}
	if blocked == nil || !blocked.Block {
		t.Fatalf("EmitToolCall = %+v, want the handler's block", blocked)
	}
}
