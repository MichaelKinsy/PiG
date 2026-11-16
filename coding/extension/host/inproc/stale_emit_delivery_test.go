package inproc_test

import (
	"context"
	"errors"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// emitCase is one emit method of the runner with the event its handlers register for.
type emitCase struct {
	event string
	emit  func(*inproc.Runner) error
}

// emitCases is every emit method of the runner that checks whether the runner is retired on entry.
func emitCases() []emitCase {
	background := context.Background()
	return []emitCase{
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
}

// upstream: runner.ts emitToolCall (:1256), emitToolResult (:1197), emitInput (:1532), emitContext (:1312), emitBeforeProviderRequest (:1375),
// emitBeforeProviderHeaders (:1406), emitMessageEnd (:1158), emitUserBash (:1276), emitBeforeAgentStart (:1434), emitResourcesDiscover (:1486) and
// emitCacheWarmingDecision (:1135) never call assertActive: only the ctx getters and actions do (:883-1037). A session whose reload failed keeps the
// runner session.reload() invalidated (agent-session.ts:3825-3826), so the next prompt, tool call and provider request still reach that runner's
// handlers, which see a stale ctx. Pi's probe of a failed interactive /reload: the next prompt logs input, before_agent_start, agent_start and
// turn_start from the old instance and its tool executes.
func TestEveryEmitDeliversToAnInvalidatedRunner(t *testing.T) {
	for _, c := range emitCases() {
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
			r.InvalidateForReload("replaced")
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
	r.InvalidateForReload("replaced")
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
	r.InvalidateForReload("replaced")
	blocked, err := r.EmitToolCall(context.Background(), extension.CustomToolCallEvent{})
	if err != nil {
		t.Fatalf("EmitToolCall: %v", err)
	}
	if blocked == nil || !blocked.Block {
		t.Fatalf("EmitToolCall = %+v, want the handler's block", blocked)
	}
}

// A retired runner has left service: Close, dispose or session replacement invalidated it, or a reload that loaded replaced it. Pi's runner has no check there either, but no Pi
// path emits to such a runner (dispose() clears the listeners, agent-session.ts:1477-1496; _buildRuntime drops the old runner, :3844), so pig refuses a late emit of its own
// goroutines with the stale error and never runs the handlers. A failed reload that is followed by Close or by a reload that loaded retires the runner it left stale too:
// retiring happens outside the once that keeps the first stale message.
func TestEveryEmitOfARetiredRunnerReturnsTheStaleError(t *testing.T) {
	states := []struct {
		name    string
		retire  func(*inproc.Runner)
		message string
	}{
		{"closed", func(r *inproc.Runner) { r.Invalidate("runtime closed") }, "runtime closed"},
		{"superseded by a reload that loaded", func(r *inproc.Runner) { r.Invalidate("replaced") }, "replaced"},
		{"superseded with the runtime handed over", func(r *inproc.Runner) { r.InvalidateKeepingRuntime("replaced") }, "replaced"},
		{"failed reload, then Close", func(r *inproc.Runner) { r.InvalidateForReload("reload failed"); r.Invalidate("runtime closed") }, "reload failed"},
		{"failed reload, then a reload that loaded", func(r *inproc.Runner) { r.InvalidateForReload("reload failed"); r.InvalidateKeepingRuntime("replaced") }, "reload failed"},
	}
	for _, state := range states {
		for _, c := range emitCases() {
			t.Run(state.name+"/"+c.event, func(t *testing.T) {
				delivered := 0
				ext := newFakeExtension("/ext/a")
				ext.Handlers[c.event] = []extension.HandlerFn{func(...any) (any, error) {
					delivered++
					return nil, nil
				}}
				r := inproc.NewRunner([]extension.Extension{ext}, ".")
				state.retire(r)
				err := c.emit(r)
				if !errors.Is(err, extension.ErrStaleContext) {
					t.Fatalf("emit on a retired runner: %v, want the stale-context error", err)
				}
				var stale *inproc.StaleError
				if !errors.As(err, &stale) || stale.Message != state.message {
					t.Fatalf("emit error = %v, want the stale message %q", err, state.message)
				}
				if delivered != 0 {
					t.Fatalf("a retired runner delivered %d times, want 0", delivered)
				}
			})
		}
	}
}

// Only an invalidation that retires makes the runner refuse emits: a runner a reload invalidated is stale, and still delivers until Close or a reload that loaded retires it.
func TestInvalidateForReloadStalesTheRunnerWithoutRetiringIt(t *testing.T) {
	r := inproc.NewRunner(nil, ".")
	r.InvalidateForReload("")
	if !r.IsStale() {
		t.Fatal("a runner a reload invalidated must be stale")
	}
	if _, err := r.EmitInput(context.Background(), "x", nil, "interactive", ""); err != nil {
		t.Fatalf("EmitInput on a runner a reload invalidated: %v", err)
	}
	r.Invalidate("runtime closed")
	if _, err := r.EmitInput(context.Background(), "x", nil, "interactive", ""); !errors.Is(err, extension.ErrStaleContext) {
		t.Fatalf("EmitInput after Close = %v, want the stale-context error", err)
	}
	if got := r.StaleMessage(); got != extension.DefaultStaleRuntimeMessage {
		t.Fatalf("StaleMessage = %q, want the first invalidation's message %q", got, extension.DefaultStaleRuntimeMessage)
	}
}
