package coding

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

type routeCtxKey struct{}

// virtual-models.ts resolveModel: the router receives the caller's context, the selected model, level and reason, the router state, the conversation, the previous physical response (skipping errored and aborted ones, and none when it came from a virtual model) and a failed physical request; an error from the router is the error of the resolution.
// Pi: packages/coding-agent/src/core/model-runtime.ts resolveModel (:973-1012).
func TestResolveModelBuildsTheRouteRequestForTheRouter(t *testing.T) {
	f := createVirtualRuntime(t)
	var seen ModelRouteRequest
	var seenValue any
	routeErr := errors.New("router unavailable")
	var failWith error
	spy := VirtualModelDefinition{Provider: "router", ID: "spy", Name: "Spy", ThinkingLevels: []ai.ModelThinkingLevel{ai.ThinkingOff},
		Route: func(ctx context.Context, request ModelRouteRequest) (ModelRoute, error) {
			seen, seenValue = request, ctx.Value(routeCtxKey{})
			if failWith != nil {
				return ModelRoute{}, failWith
			}
			return ModelRoute{Model: f.runtime.GetModel("faux", "small")}, nil
		}}
	if err := f.runtime.RegisterVirtualModel(spy); err != nil {
		t.Fatal(err)
	}
	if result := f.runtime.Refresh(t.Context(), ai.ModelsRefreshOptions{AllowNetwork: new(false)}); result.Aborted {
		t.Fatalf("refresh = %+v", result)
	}
	virtual := f.runtime.GetModel("router", "spy")
	small, large := f.runtime.GetModel("faux", "small"), f.runtime.GetModel("faux", "large")

	errored := assistantFromModel(small, "")
	errored.StopReason = ai.StopReasonError
	aborted := assistantFromModel(small, "")
	aborted.StopReason = ai.StopReasonAborted
	answer := assistantFromModel(large, "answer")
	answer.ThinkingLevel = ai.ThinkingHigh
	messages := []ai.Message{userText("one", 1), answer, errored, aborted, userText("two", 2)}
	state := json.RawMessage(`{"n":1}`)
	ctx := context.WithValue(t.Context(), routeCtxKey{}, "caller-context")

	if _, err := f.runtime.ResolveModel(ctx, virtual, messages, ResolveModelOptions{Reason: ModelRouteReasonRetry, ThinkingLevel: ai.ThinkingOff, State: state}); err != nil {
		t.Fatal(err)
	}
	if seenValue != "caller-context" {
		t.Errorf("the router did not receive the caller's context: %v", seenValue)
	}
	if seen.Model.ID != virtual.ID || seen.Reason != ModelRouteReasonRetry || seen.ThinkingLevel != ai.ThinkingOff || string(seen.State) != `{"n":1}` {
		t.Errorf("request = %+v", seen)
	}
	if !reflect.DeepEqual(seen.Messages, messages) {
		t.Errorf("messages = %v, want the conversation", seen.Messages)
	}
	if seen.Previous == nil || seen.Previous.Model.ID != large.ID || seen.Previous.ThinkingLevel != ai.ThinkingHigh {
		t.Errorf("previous = %+v, want the latest successful physical response", seen.Previous)
	}
	if seen.Failed != nil {
		t.Errorf("failed = %+v without a failed response", seen.Failed)
	}

	// The latest successful response came from a virtual model: it is not a physical previous.
	fromVirtual := assistantFromModel(f.virtual, "routed")
	if _, err := f.runtime.ResolveModel(ctx, virtual, []ai.Message{userText("one", 1), answer, fromVirtual, userText("two", 2)}, ResolveModelOptions{Reason: ModelRouteReasonUser}); err != nil {
		t.Fatal(err)
	}
	if seen.Previous != nil {
		t.Errorf("previous = %+v after a response from a virtual model, want none", seen.Previous)
	}

	// A failed physical response is reported with its model and level; a failed virtual one is not.
	failed := assistantFromModel(large, "")
	failed.StopReason, failed.ThinkingLevel = ai.StopReasonError, ai.ThinkingMedium
	if _, err := f.runtime.ResolveModel(ctx, virtual, messages, ResolveModelOptions{Reason: ModelRouteReasonRetry, Failed: &failed}); err != nil {
		t.Fatal(err)
	}
	if seen.Failed == nil || seen.Failed.Model.ID != large.ID || seen.Failed.ThinkingLevel != ai.ThinkingMedium {
		t.Errorf("failed = %+v", seen.Failed)
	}
	failedVirtual := assistantFromModel(f.virtual, "")
	failedVirtual.StopReason = ai.StopReasonError
	if _, err := f.runtime.ResolveModel(ctx, virtual, messages, ResolveModelOptions{Reason: ModelRouteReasonRetry, Failed: &failedVirtual}); err != nil {
		t.Fatal(err)
	}
	if seen.Failed != nil {
		t.Errorf("failed = %+v for a failed virtual model", seen.Failed)
	}

	failWith = routeErr
	if _, err := f.runtime.ResolveModel(ctx, virtual, messages, ResolveModelOptions{Reason: ModelRouteReasonUser}); !errors.Is(err, routeErr) {
		t.Fatalf("router error = %v, want %v", err, routeErr)
	}
}
