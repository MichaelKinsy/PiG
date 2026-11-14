package subprocess

import (
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// BenchmarkEventFrameEncode encodes the arguments of one event request for each event type whose own MarshalJSON writes text: the cost of an event
// frame with no lone surrogate in it, which is every frame but the rare one.
func BenchmarkEventFrameEncode(b *testing.B) {
	for _, eventType := range []reflect.Type{
		reflect.TypeFor[extension.ToolExecutionStartEvent](), reflect.TypeFor[extension.ToolExecutionUpdateEvent](), reflect.TypeFor[extension.ToolExecutionEndEvent](),
		reflect.TypeFor[extension.TurnEndEvent](), reflect.TypeFor[extension.AgentBeforeSettleEvent](), reflect.TypeFor[extension.BeforeAgentStartEvent](),
		reflect.TypeFor[extension.ModelSelectEvent](), reflect.TypeFor[extension.McpServersChangeEvent](),
	} {
		fill := &sweepFill{paths: map[string]string{}, clean: true}
		target := reflect.New(eventType)
		fill.fill(target.Elem(), "", 0)
		event := target.Elem().Interface()
		if _, err := sweepMarshal(event); err != nil {
			b.Fatalf("%s: %v", eventType.Name(), err)
		}
		b.Run(eventType.Name(), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, err := marshalEventWire(boundaryEventWire(wireEventPayload(event), nil)); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
