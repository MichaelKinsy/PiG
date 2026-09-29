package agentharness

import (
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/agent/harness"
)

// upstream: packages/agent/src/harness/events.ts:34-46; packages/agent/test/harness/runtime/lane.test.ts:229-256
func TestHarnessEventBusRejectsUncloneableBatchBeforeBinding(t *testing.T) {
	t.Parallel()
	bus := NewHarnessEventBus()
	var delivered []string
	_, err := bus.On(EventRunStart, func(_ harness.Context, event HarnessEvent) error {
		delivered = append(delivered, event.Payload.(RunStartPayload).RunID)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	func() {
		defer func() {
			failure := recover()
			if failure == nil {
				t.Error("batch with a function did not throw DataCloneError")
			} else if got := reflect.Indirect(reflect.ValueOf(failure)).Type().Name(); got != "DataCloneError" {
				t.Errorf("exception = %s, want DataCloneError", got)
			}
		}()
		bus.EmitBatch(t.Context(), []HarnessEvent{
			{Payload: RunStartPayload{RunID: "must not bind"}},
			{Payload: ToolStartPayload{Args: map[string]any{"nested": []any{func() {}}}}},
		})
	}()
	bus.Emit(t.Context(), HarnessEvent{Payload: RunStartPayload{RunID: "next batch"}})
	if !reflect.DeepEqual(delivered, []string{"next batch"}) {
		t.Fatalf("delivery = %v, want only the next batch", delivered)
	}
}

func BenchmarkCloneHarnessEvent(b *testing.B) {
	event := HarnessEvent{Lane: "main", Payload: ToolStartPayload{RunID: "run", Args: map[string]any{"path": "file.go", "lines": []any{1, 2, 3}}}}
	for b.Loop() {
		_ = CloneHarnessEvent(event)
	}
}
