package inproc_test

import (
	"context"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

func BenchmarkExtensionHandlerSnapshots(b *testing.B) {
	const count = 64
	exts := make([]extension.Extension, count)
	for i := range exts {
		exts[i].AddEventHandler("agent_end", 1, func(...any) (any, error) { return nil, nil })
	}
	runner := inproc.NewRunner(exts, ".")
	event := extension.AgentEndEvent{Type: "agent_end"}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, err := runner.Emit(context.Background(), event); err != nil {
			b.Fatal(err)
		}
	}
}
