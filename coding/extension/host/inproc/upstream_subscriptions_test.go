package inproc_test

import (
	"context"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

func TestUpstreamRunnerEventSubscriptions(t *testing.T) {
	type fixture struct {
		calls  []string
		ext    extension.Extension
		runner *inproc.Runner
		nextID int
	}
	create := func(t *testing.T) *fixture {
		f := &fixture{ext: extension.Extension{Handlers: map[string][]extension.HandlerFn{}}}
		f.ext.InitializeEventHandlers()
		f.runner = inproc.NewRunner([]extension.Extension{f.ext}, t.TempDir())
		return f
	}
	on := func(f *fixture, callback func()) func() {
		f.nextID++
		id := f.nextID
		f.ext.AddEventHandler("agent_end", id, func(...any) (any, error) { callback(); return nil, nil })
		return func() { f.ext.RemoveEventHandler("agent_end", id) }
	}
	emit := func(t *testing.T, f *fixture) {
		t.Helper()
		if _, err := f.runner.Emit(t.Context(), extension.AgentEndEvent{Type: "agent_end", Messages: []extension.AgentMessage{}}); err != nil {
			t.Fatal(err)
		}
	}
	check := func(t *testing.T, f *fixture, want ...string) {
		t.Helper()
		if !slices.Equal(f.calls, want) {
			t.Fatalf("calls=%v, want %v", f.calls, want)
		}
	}
	// .upstream/v0.87.1/packages/coding-agent/test/extensions-runner.test.ts:1230
	t.Run("allows self-removal without skipping neighboring handlers", func(t *testing.T) {
		f := create(t)
		var unsubscribe func()
		unsubscribe = on(f, func() { f.calls = append(f.calls, "A"); unsubscribe() })
		on(f, func() { f.calls = append(f.calls, "B") })
		emit(t, f)
		check(t, f, "A", "B")
		emit(t, f)
		check(t, f, "A", "B", "B")
	})
	// .upstream/v0.87.1/packages/coding-agent/test/extensions-runner.test.ts:1249
	t.Run("removes duplicate registrations independently and cleans up the last handler", func(t *testing.T) {
		f := create(t)
		shared := func() { f.calls = append(f.calls, "shared") }
		stopFirst := on(f, shared)
		stopB := on(f, func() { f.calls = append(f.calls, "B") })
		stopSecond := on(f, shared)
		stopSecond()
		stopSecond()
		emit(t, f)
		check(t, f, "shared", "B")
		stopFirst()
		emit(t, f)
		check(t, f, "shared", "B", "B")
		stopB()
		if _, present := f.ext.Handlers["agent_end"]; present {
			t.Fatal("last removal left the event key")
		}
		if f.runner.HasHandlers("agent_end") {
			t.Fatal("last removal left a live handler")
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/extensions-runner.test.ts:1279
	t.Run("keeps removed pending handlers in the current dispatch", func(t *testing.T) {
		f := create(t)
		var stopB func()
		on(f, func() { f.calls = append(f.calls, "A"); stopB() })
		stopB = on(f, func() { f.calls = append(f.calls, "B") })
		on(f, func() { f.calls = append(f.calls, "C") })
		emit(t, f)
		check(t, f, "A", "B", "C")
		emit(t, f)
		check(t, f, "A", "B", "C", "A", "C")
	})
	// .upstream/v0.87.1/packages/coding-agent/test/extensions-runner.test.ts:1300
	t.Run("defers registrations made during dispatch until the next dispatch", func(t *testing.T) {
		f := create(t)
		on(f, func() { f.calls = append(f.calls, "A"); on(f, func() { f.calls = append(f.calls, "C") }) })
		on(f, func() { f.calls = append(f.calls, "B") })
		emit(t, f)
		check(t, f, "A", "B")
		emit(t, f)
		check(t, f, "A", "B", "A", "B", "C")
	})
	// .upstream/v0.87.1/packages/coding-agent/test/extensions-runner.test.ts:1320
	t.Run("uses a fresh handler list for nested dispatches", func(t *testing.T) {
		f := create(t)
		var stopA, stopB func()
		stopA = on(f, func() {
			f.calls = append(f.calls, "A")
			stopA()
			stopB()
			on(f, func() { f.calls = append(f.calls, "C") })
			emit(t, f)
		})
		stopB = on(f, func() { f.calls = append(f.calls, "B") })
		emit(t, f)
		check(t, f, "A", "C", "B")
	})
}

// Upstream runner.ts:snapshotEventHandlers snapshots ALL extensions before the
// first callback. A callback may synchronously change a later extension's list.
func TestRunnerSnapshotsAllExtensionsBeforeDispatch(t *testing.T) {
	for _, kind := range []string{"agent_end", "context", "context_with_system", "before_provider_request", "before_provider_headers", "message_end", "input", "tool_call", "tool_result", "user_bash", "before_agent_start", "resources_discover", "agent_before_settle", "project_trust"} {
		t.Run(kind, func(t *testing.T) {
			first := extension.Extension{Path: "first"}
			first.InitializeEventHandlers()
			second := extension.Extension{Path: "second"}
			second.InitializeEventHandlers()
			var calls []string
			first.AddEventHandler(kind, 1, func(...any) (any, error) {
				calls = append(calls, "A")
				second.RemoveEventHandler(kind, 2)
				second.AddEventHandler(kind, 3, func(...any) (any, error) { calls = append(calls, "C"); return nil, nil })
				return nil, nil
			})
			second.AddEventHandler(kind, 2, func(...any) (any, error) { calls = append(calls, "B"); return nil, nil })
			runner := inproc.NewRunner([]extension.Extension{first, second}, t.TempDir())
			dispatch := func() error { return dispatchSubscriptionEvent(t.Context(), runner, kind) }
			if err := dispatch(); err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(calls, []string{"A", "B"}) {
				t.Fatalf("current dispatch=%v, want [A B]", calls)
			}
			if err := dispatch(); err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(calls, []string{"A", "B", "A", "C"}) {
				t.Fatalf("next dispatch=%v, want [A B A C]", calls)
			}
		})
	}
}

func dispatchSubscriptionEvent(ctx context.Context, r *inproc.Runner, kind string) error {
	var err error
	switch kind {
	case "agent_end":
		_, err = r.Emit(ctx, extension.AgentEndEvent{Type: kind})
	case "context":
		_, err = r.EmitContext(ctx, nil)
	case "context_with_system":
		_, err = r.EmitContextWithSystem(ctx, nil)
	case "before_provider_request":
		_, err = r.EmitBeforeProviderRequest(ctx, map[string]any{})
	case "before_provider_headers":
		_, err = r.EmitBeforeProviderHeaders(ctx, extension.ProviderHeaders{})
	case "message_end":
		_, err = r.EmitMessageEnd(ctx, nil)
	case "input":
		_, err = r.EmitInput(ctx, "hello", nil, extension.InputSource("interactive"), "")
	case "tool_call":
		_, err = r.EmitToolCall(ctx, extension.CustomToolCallEvent{})
	case "tool_result":
		_, err = r.EmitToolResult(ctx, extension.CustomToolResultEvent{})
	case "user_bash":
		_, err = r.EmitUserBash(ctx, extension.UserBashEvent{Type: kind, Command: "pwd"})
	case "before_agent_start":
		_, err = r.EmitBeforeAgentStart(ctx, "hello", nil, "base", extension.BuildSystemPromptOptions{})
	case "resources_discover":
		_, err = r.EmitResourcesDiscover(ctx, "cwd", "startup")
	case "agent_before_settle":
		_, err = r.EmitBoundary(ctx, &extension.AgentBeforeSettleEvent{Type: kind, BoundaryState: extension.BoundaryState{Outcome: extension.AgentActivityCompleted}}, func([]extension.SessionBoundaryDraft) (extension.BoundaryContextPreview, error) {
			return extension.BoundaryContextPreview{}, nil
		})
	case "project_trust":
		_, _, err = inproc.EmitProjectTrust(r, ctx, extension.ProjectTrustEvent{Type: kind})
	}
	return err
}
