package factoryload_test

import (
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/factoryload"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// BenchmarkPiEventsEmitNoError measures one emit on a channel whose pi.events handler does not fail: the wrapper that reports a throwing handler (Pi
// 18e99051be24) must add no allocation to a dispatch that raises no error.
func BenchmarkPiEventsEmitNoError(b *testing.B) {
	bus := extension.CreateEventBus()
	runtime := extension.CreateExtensionRuntime()
	var sink int
	ext, err := factoryload.LoadExtensionFromFactory(func(pi extension.API) error {
		pi.Events().On("bench:tick", func(any) { sink++ })
		return nil
	}, ".", bus, runtime, "<bench>")
	if err != nil {
		b.Fatal(err)
	}
	_ = inproc.NewRunner([]extension.Extension{ext}, ".", runtime)
	b.ReportAllocs()
	for b.Loop() {
		bus.Emit("bench:tick", nil)
	}
	_ = sink
}
