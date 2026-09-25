package inproc

import (
	"context"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

func TestEventSubscriptionChangesApplyToNextDispatchSnapshot(t *testing.T) {
	var calls []string
	ext := extension.Extension{
		Name:     "dynamic-events",
		Handlers: map[string][]extension.HandlerFn{},
	}
	ext.InitializeEventHandlers()

	var removeSecond func()
	first := func(...any) (any, error) {
		calls = append(calls, "first")
		removeSecond()
		ext.AddEventHandler("session_start", 3, func(...any) (any, error) {
			calls = append(calls, "third")
			return nil, nil
		})
		return nil, nil
	}
	second := func(...any) (any, error) {
		calls = append(calls, "second")
		return nil, nil
	}
	ext.AddEventHandler("session_start", 1, first)
	ext.AddEventHandler("session_start", 2, second)
	removeSecond = func() { ext.RemoveEventHandler("session_start", 2) }

	runner := NewRunner([]extension.Extension{ext}, t.TempDir())
	event := extension.SessionStartEvent{Type: "session_start"}
	if _, err := runner.Emit(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(calls, []string{"first", "second"}) {
		t.Fatalf("first dispatch calls = %v", calls)
	}

	calls = nil
	if _, err := runner.Emit(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(calls, []string{"first", "third"}) {
		t.Fatalf("second dispatch calls = %v", calls)
	}

	ext.RemoveEventHandler("session_start", 2)
	ext.RemoveEventHandler("session_start", 2)
}
