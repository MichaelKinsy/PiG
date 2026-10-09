package coding

import (
	"context"
	"reflect"
	"sync/atomic"
	"testing"
	"testing/synctest"

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
