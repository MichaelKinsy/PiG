package coding

import (
	"context"
	"reflect"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// Pi: packages/coding-agent/src/core/agent-session.ts:1653 (Session.setScopedModels).
func TestSessionModelCycleBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name, direction, current, want string
		scope                          []int
	}{{"forward wrap", "forward", "faux-2", "faux-1", nil}, {"backward wrap", "backward", "faux-1", "faux-2", nil}, {"missing current follows first", "forward", "unknown", "faux-2", nil}, {"singleton", "forward", "faux-1", "", []int{0}}} {
		t.Run(tc.name, func(t *testing.T) {
			h := newModelExtensionHarness(t, []bool{true, true}, "", true, extension.Extension{}, nil)
			if tc.scope != nil {
				var models []ScopedModel
				for _, i := range tc.scope {
					models = append(models, ScopedModel{Model: h.models[i]})
				}
				h.session.SetScopedModels(models)
			}
			current := new(*h.models[0])
			current.ID = tc.current
			h.session.Agent().SetModel(current)
			result, err := h.session.CycleModel(tc.direction)
			if err != nil {
				t.Fatal(err)
			}
			if tc.want == "" {
				if result != nil {
					t.Fatalf("singleton result=%+v", result)
				}
			} else if result == nil || result.Model.ID != tc.want {
				t.Fatalf("result=%+v want %s", result, tc.want)
			}
		})
	}
}

func TestSessionPromptAwaitsInputBeforeProvider(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		started, release := make(chan struct{}), make(chan struct{})
		ext := extension.Extension{Handlers: map[string][]extension.HandlerFn{"input": {func(...any) (any, error) {
			close(started)
			<-release
			return extension.InputEventResultTransform{Text: "ordered"}, nil
		}}}}
		var observed string
		h := newModelExtensionHarness(t, []bool{false}, "", true, ext, nil, func(messages []ai.Message) *ai.AssistantMessage {
			observed = modelExtensionUserText(messages)
			return fauxReply("done", ai.StopReasonStop, 0)(messages)
		})
		done := make(chan error, 1)
		go func() { err := h.session.Prompt(context.Background(), "original"); done <- err }()
		<-started
		synctest.Wait()
		if observed != "" {
			t.Fatal("provider ran before awaited input handler")
		}
		select {
		case <-done:
			t.Fatal("prompt returned before input handler")
		default:
		}
		close(release)
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		if observed != "ordered" {
			t.Fatalf("provider input=%q", observed)
		}
	})
}

func TestRejectedModelSelectionPreservesState(t *testing.T) {
	h := newModelExtensionHarness(t, []bool{true, true}, "", false, extension.Extension{}, nil)
	previous := h.session.Model()
	entries := h.session.Inner().GetEntries()
	if err := h.session.SetModel(h.models[1], ModelMutationOptions{Persist: true}); err == nil {
		t.Fatal("unconfigured model accepted")
	}
	if h.session.Model() != previous || !reflect.DeepEqual(h.session.Inner().GetEntries(), entries) || h.services.Settings().DefaultModel != "" {
		t.Fatal("rejected selection changed model, history or settings")
	}
}

// Pi: agent-session.ts:2570-2600 (_cycleAvailableModel) mutates the model before awaiting model_select; BeginModelCycle is that prefix, so RPC can admit its next command there.
func TestBeginModelCycleSplitsMutationFromNotification(t *testing.T) {
	var calls atomic.Int32
	h := newModelExtensionHarness(t, []bool{true, true}, "", true, extension.Extension{Path: "/ext/model", Handlers: map[string][]extension.HandlerFn{"model_select": {func(args ...any) (any, error) {
		calls.Add(1)
		return nil, nil
	}}}}, nil)
	result, complete, err := h.session.BeginModelCycle(context.Background(), "forward")
	if err != nil || result == nil || result.Model.ID != "faux-2" || result.IsScoped {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if h.session.Model().ID != "faux-2" {
		t.Fatal("model was not applied before the notification")
	}
	if complete == nil {
		t.Fatal("a registered model_select handler left no completion")
	}
	if calls.Load() != 0 {
		t.Fatal("model_select ran before the completion")
	}
	complete()
	if calls.Load() != 1 {
		t.Fatalf("model_select calls=%d after completion", calls.Load())
	}
	if _, _, err := h.session.BeginModelCycle(context.Background(), "sideways"); err == nil {
		t.Fatal("unknown direction accepted")
	}
}

// Pi's cycleModel applies the model and thinking level before awaiting model_select, then reads the thinking level a handler left
// (agent-session.ts:2523-2550). BeginModelCycleOnMain is that prefix for an owner loop: model_select waits for the completion,
// which records the handler's level in the result.
func TestBeginModelCycleOnMainDefersNotificationsToTheCompletion(t *testing.T) {
	var calls atomic.Int32
	var h *modelExtensionHarness
	h = newModelExtensionHarness(t, []bool{true, true}, "", true, extension.Extension{Path: "/ext/model", Handlers: map[string][]extension.HandlerFn{"model_select": {func(args ...any) (any, error) {
		calls.Add(1)
		return nil, h.session.SetThinkingLevel(ai.ThinkingLow)
	}}}}, nil)
	if err := h.session.SetThinkingLevel(ai.ThinkingHigh); err != nil {
		t.Fatal(err)
	}
	result, complete, err := h.session.BeginModelCycleOnMain(context.Background(), "forward", ModelMutationOptions{})
	if err != nil || result == nil || result.Model.ID != "faux-2" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if h.session.Model().ID != "faux-2" || calls.Load() != 0 || complete == nil {
		t.Fatalf("model %s, model_select calls %d before the completion; want the model applied and the handler deferred", h.session.Model().ID, calls.Load())
	}
	complete()
	if calls.Load() != 1 || result.ThinkingLevel != ai.ThinkingLow {
		t.Fatalf("model_select calls=%d thinking=%s after the completion, want 1 and the handler's level", calls.Load(), result.ThinkingLevel)
	}
	if _, _, err := h.session.BeginModelCycleOnMain(context.Background(), "sideways", ModelMutationOptions{}); err == nil {
		t.Fatal("unknown direction accepted")
	}
}

// Pi's shutdown exits without awaiting a pending model_select (interactive-mode.ts:4251-4291). The OnMain splits' notifications
// end with the caller's context, so an owner that stops is not held by a handler.
func TestModelChangeOnMainNotificationsEndWithTheirContext(t *testing.T) {
	for _, cycle := range []bool{true, false} {
		t.Run(map[bool]string{true: "cycle", false: "set"}[cycle], func(t *testing.T) {
			entered := make(chan struct{}, 1)
			h := newModelExtensionHarness(t, []bool{true, true}, "", true, extension.Extension{Path: "/ext/model", Handlers: map[string][]extension.HandlerFn{"model_select": {func(args ...any) (any, error) {
				entered <- struct{}{}
				<-args[1].(context.Context).Done()
				return nil, nil
			}}}}, nil)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() {
				if cycle {
					_, complete, err := h.session.BeginModelCycleOnMain(ctx, "forward", ModelMutationOptions{})
					if complete != nil {
						complete()
					}
					done <- err
					return
				}
				done <- h.session.SetModelOnMain(ctx, h.models[1], ModelMutationOptions{}, nil)
			}()
			<-entered
			cancel()
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("model_select kept the change after its context ended")
			}
		})
	}
}

// The OnMain splits' notifications also end with the Session: closing it while a model_select handler waits ends the change.
func TestModelChangeOnMainNotificationsEndWhenTheSessionCloses(t *testing.T) {
	for _, cycle := range []bool{true, false} {
		t.Run(map[bool]string{true: "cycle", false: "set"}[cycle], func(t *testing.T) {
			entered := make(chan struct{}, 1)
			h := newModelExtensionHarness(t, []bool{true, true}, "", true, extension.Extension{Path: "/ext/model", Handlers: map[string][]extension.HandlerFn{"model_select": {func(args ...any) (any, error) {
				entered <- struct{}{}
				<-args[1].(context.Context).Done()
				return nil, nil
			}}}}, nil)
			done := make(chan error, 1)
			go func() {
				if cycle {
					_, complete, err := h.session.BeginModelCycleOnMain(context.Background(), "forward", ModelMutationOptions{})
					if complete != nil {
						complete()
					}
					done <- err
					return
				}
				done <- h.session.SetModelOnMain(context.Background(), h.models[1], ModelMutationOptions{}, nil)
			}()
			<-entered
			go func() { _ = h.session.Close() }()
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("model_select kept the change after its Session closed")
			}
		})
	}
}
