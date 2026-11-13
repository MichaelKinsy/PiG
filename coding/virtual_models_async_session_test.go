package coding

// pi: packages/coding-agent/src/core/virtual-models.ts

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

func registerAsyncRouter(t *testing.T, f virtualRuntimeFixture, id string, route func(context.Context, ModelRouteRequest) (ModelRoute, error)) *ai.Model {
	t.Helper()
	definition := VirtualModelDefinition{Provider: "router", ID: id, Name: id, ThinkingLevels: []ai.ModelThinkingLevel{ai.ThinkingLow}, Route: route}
	if err := f.runtime.RegisterVirtualModel(definition); err != nil {
		t.Fatal(err)
	}
	if result := f.runtime.Refresh(t.Context(), ai.ModelsRefreshOptions{AllowNetwork: new(false)}); result.Aborted || len(result.Errors) != 0 {
		t.Fatalf("refresh = %+v", result)
	}
	return f.runtime.GetModel("router", id)
}

func stateEntries(manager *icodingagent.Session) int {
	n := 0
	for _, entry := range manager.GetBranch() {
		if entry.Base().Type == "custom" {
			n++
		}
	}
	return n
}

func lastAssistantOf(t *testing.T, session *Session) *ai.AssistantMessage {
	t.Helper()
	var last *ai.AssistantMessage
	for _, message := range session.Messages() {
		if message.Assistant != nil {
			m := message.Assistant
			last = &ai.AssistantMessage{StopReason: m.StopReason, ErrorMessage: m.ErrorMessage}
		}
	}
	if last == nil {
		t.Fatal("the run produced no assistant message")
	}
	return last
}

// virtual-models.ts:69,101 / agent-session.ts routeRequest: the router receives the request signal. A router awaiting that
// signal when the run is aborted sees it cancelled, the request ends as aborted (not as a routing error) and no router state
// entry is appended for the request that never got a route.
func TestVirtualModelRouterReceivesTheRequestSignalAndAbortEndsTheRequestAsAborted(t *testing.T) {
	f := createVirtualRuntime(t)
	entered := make(chan struct{})
	seen := make(chan error, 1)
	virtual := registerAsyncRouter(t, f, "blocking", func(ctx context.Context, _ ModelRouteRequest) (ModelRoute, error) {
		close(entered)
		select {
		case <-ctx.Done():
			seen <- ctx.Err()
			return ModelRoute{}, ctx.Err()
		case <-time.After(10 * time.Second):
			seen <- nil
			return ModelRoute{Model: f.runtime.GetModel("faux", "small"), ThinkingLevel: ai.ThinkingLow, State: []byte(`{"n":1}`)}, nil
		}
	})
	manager := icodingagent.NewSession("virtual-abort", t.TempDir())
	session, err := openVirtualSession(t, f, manager, virtual)
	if err != nil {
		t.Fatal(err)
	}
	before := stateEntries(manager)
	prompted := make(chan error, 1)
	go func() { prompted <- session.Prompt(t.Context(), "hello") }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the router was never asked")
	}
	session.RequestAbort()
	select {
	case <-prompted:
	case <-time.After(5 * time.Second):
		t.Fatal("the prompt did not settle after the abort: the router signal is not the request signal")
	}
	if err := <-seen; !errors.Is(err, context.Canceled) {
		t.Fatalf("the router saw %v, want context.Canceled", err)
	}
	if got := lastAssistantOf(t, session); got.StopReason != ai.StopReasonAborted {
		t.Fatalf("stop reason = %q (%s), want aborted", got.StopReason, got.ErrorMessage)
	}
	if n := stateEntries(manager); n != before {
		t.Fatalf("router state entries = %d, want %d: no route, no state", n, before)
	}
}

// A router that fails ends the request as an error response carrying the router's message and records no state entry.
func TestVirtualModelRouterFailureEndsTheRequestAsAnErrorWithoutRecordingState(t *testing.T) {
	f := createVirtualRuntime(t)
	virtual := registerAsyncRouter(t, f, "failing", func(context.Context, ModelRouteRequest) (ModelRoute, error) {
		return ModelRoute{}, errors.New("router down")
	})
	manager := icodingagent.NewSession("virtual-fail", t.TempDir())
	session, err := openVirtualSession(t, f, manager, virtual)
	if err != nil {
		t.Fatal(err)
	}
	before := stateEntries(manager)
	_ = session.Prompt(t.Context(), "hello")
	got := lastAssistantOf(t, session)
	if got.StopReason != ai.StopReasonError || got.ErrorMessage != "router down" {
		t.Fatalf("assistant = %+v, want an error response with the router's message", got)
	}
	if n := stateEntries(manager); n != before {
		t.Fatalf("router state entries = %d, want %d", n, before)
	}
}
