package coding

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// Pi agent-session.ts:3477-3479 publishes each delta after the callback, before executeBash resolves.
func TestBashOutputPublishesSessionEventsBeforeReturn(t *testing.T) {
	h := newRecoveryHarness(t, harnessOptions{emptySessionManager: true})
	var order []string
	unsubscribe := h.session.Subscribe(func(event agent.AgentEvent) {
		order = append(order, fmt.Sprintf("%T", event))
	})
	operations := bashPersistenceOperations(func(_ context.Context, _, _ string, options extension.BashOperationsExecOptions) (extension.BashOperationsResult, error) {
		options.OnData([]byte("hello "))
		options.OnData([]byte("world"))
		return extension.BashOperationsResult{ExitCode: new(0)}, nil
	})
	_, err := h.session.ExecuteBashWithOperations(t.Context(), "custom", false, func(delta string) { order = append(order, delta) }, operations, nil)
	unsubscribe()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"hello ", "agent.BashExecutionUpdateEvent", "world", "agent.BashExecutionUpdateEvent"}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("order=%v want=%v", order, want)
	}
}

// Pi agent-session-bash-persistence.test.ts:310 supplies the two exact deltas and correlated execution ID.
func TestUpstreamBashPersistenceOutputUpdates(t *testing.T) {
	h := newRecoveryHarness(t, harnessOptions{emptySessionManager: true})
	var callbackDeltas []string
	var updates []agent.BashExecutionUpdateEvent
	unsubscribe := h.session.Subscribe(func(event agent.AgentEvent) {
		if update, ok := event.(agent.BashExecutionUpdateEvent); ok {
			updates = append(updates, update)
		}
	})
	operations := bashPersistenceOperations(func(_ context.Context, _, _ string, options extension.BashOperationsExecOptions) (extension.BashOperationsResult, error) {
		options.OnData([]byte("hello "))
		options.OnData([]byte("world"))
		return extension.BashOperationsResult{ExitCode: new(0)}, nil
	})
	_, err := h.session.ExecuteBashWithOperations(t.Context(), "custom", false, func(delta string) { callbackDeltas = append(callbackDeltas, delta) }, operations, new("bash-1"))
	unsubscribe()
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"hello ", "world"}; !reflect.DeepEqual(callbackDeltas, want) {
		t.Fatalf("callback=%v want=%v", callbackDeltas, want)
	}
	want := []agent.BashExecutionUpdateEvent{{ID: new("bash-1"), Delta: "hello "}, {ID: new("bash-1"), Delta: "world"}}
	if !reflect.DeepEqual(updates, want) {
		t.Fatalf("events=%+v want=%+v", updates, want)
	}
}

// Pi also emits updates when there is no callback, retaining absent and explicitly empty IDs separately.
func TestBashOutputUpdatesWithoutCallback(t *testing.T) {
	for _, id := range []*string{nil, new("")} {
		h := newRecoveryHarness(t, harnessOptions{emptySessionManager: true})
		var updates []agent.BashExecutionUpdateEvent
		unsubscribe := h.session.Subscribe(func(event agent.AgentEvent) {
			if update, ok := event.(agent.BashExecutionUpdateEvent); ok {
				updates = append(updates, update)
			}
		})
		operations := bashPersistenceOperations(func(_ context.Context, _, _ string, options extension.BashOperationsExecOptions) (extension.BashOperationsResult, error) {
			options.OnData([]byte("output"))
			return extension.BashOperationsResult{ExitCode: new(0)}, nil
		})
		_, err := h.session.ExecuteBashWithOperations(t.Context(), "custom", false, nil, operations, id)
		unsubscribe()
		if err != nil {
			t.Fatal(err)
		}
		if want := []agent.BashExecutionUpdateEvent{{ID: id, Delta: "output"}}; !reflect.DeepEqual(updates, want) {
			t.Fatalf("updates=%+v; want=%+v", updates, want)
		}
	}
}
