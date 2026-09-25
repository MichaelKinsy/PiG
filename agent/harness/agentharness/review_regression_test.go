package agentharness

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/agent/harness"
)

// Upstream events.ts clears registrations only after the delivery tail settles, including handler_error recipients selected during a queued delivery.
func TestHarnessEventBusCloseReportsBoundListenerFailure(t *testing.T) {
	bus := NewHarnessEventBus()
	failures := &recorder{}
	mustSubscribe(t)(bus.On(EventHandlerError, func(_ harness.Context, event HarnessEvent) error {
		failures.add(event.Payload.(HandlerErrorPayload).Error)
		return nil
	}))
	started, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	mustSubscribe(t)(bus.On(EventRunStart, func(harness.Context, HarnessEvent) error {
		close(started)
		<-release
		return errors.New("queued failure")
	}))
	go func() {
		defer close(done)
		bus.Emit(context.Background(), runStart("run", "main"))
	}()
	<-started
	bus.Close(errors.New("closed"))
	close(release)
	<-done
	if got := failures.get(); !slices.Equal(got, []string{"queued failure"}) {
		t.Fatalf("handler errors during close = %v, want [queued failure]", got)
	}
}

// Upstream hooks.ts tests payload against undefined, not null. A defined BeforePayloadResult carries its required Payload even when that value is nil.
func TestHookRegistryBeforePayloadCanReplaceWithNull(t *testing.T) {
	hooks := NewHookRegistry(ignoreErrors)
	mustOn(t)(hooks.OnBeforePayload(func(harness.Context, BeforePayloadEvent) (*BeforePayloadResult, error) {
		return &BeforePayloadResult{Payload: nil}, nil
	}, HookOptions{}))
	var called bool
	mustOn(t)(hooks.OnBeforePayload(func(_ harness.Context, event BeforePayloadEvent) (*BeforePayloadResult, error) {
		called = true
		if event.Payload != nil {
			t.Errorf("later handler payload = %v, want null", event.Payload)
		}
		return nil, nil
	}, HookOptions{}))
	result, err := hooks.RunBeforePayload(context.Background(), newGate(), BeforePayloadEvent{Payload: map[string]any{"previous": true}})
	if err != nil || result == nil || result.Payload != nil || !called {
		t.Fatalf("result = %+v, err = %v, called = %v; want a null payload", result, err, called)
	}
}

// The settled branch of upstream LaneSnapshotTool requires isError, including false; the running branch omits it.
func TestLaneSnapshotToolJSONKeepsSettledFalse(t *testing.T) {
	for _, tt := range []struct {
		status  string
		isError bool
		want    string
	}{
		{status: "running", want: ""},
		{status: "settled", want: "false"},
		{status: "settled", isError: true, want: "true"},
	} {
		t.Run(tt.status+tt.want, func(t *testing.T) {
			tool := LaneSnapshotTool{Status: tt.status, ToolCallID: "c", ToolName: "t", Args: map[string]any{}, IsError: tt.isError}
			if tt.status == "settled" {
				tool.Result = &harness.AgentToolResult{}
			}
			data, err := json.Marshal(tool)
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(data, &fields); err != nil {
				t.Fatal(err)
			}
			if got := string(fields["isError"]); got != tt.want {
				t.Fatalf("isError = %q, want %q in %s", got, tt.want, data)
			}
		})
	}
}
