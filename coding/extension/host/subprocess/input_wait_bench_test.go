package subprocess

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension/host/invocation"
)

// BenchmarkNodeInputHandlerRPC is one input event of a Node extension in RPC mode whose handler returns in its own microtasks, under the suspension acknowledgment rpcAwaitHandler binds: the dispatch every prompt of an RPC session with an input handler pays (D86). The runtime watches the handler's event-loop window for its wait; a handler that does not wait costs no report. BenchmarkNodeToolCallEvent is the dispatch with no acknowledgment and no window, for scale.
func BenchmarkNodeInputHandlerRPC(b *testing.B) {
	root := b.TempDir()
	source := filepath.Join(root, "input.mjs")
	if err := os.WriteFile(source, []byte(`export default function(pi){pi.on("input",async()=>{await null})}`), 0o600); err != nil {
		b.Fatal(err)
	}
	host := NewHost(root)
	host.SetMode("rpc")
	b.Cleanup(func() { host.Shutdown("benchmark done") })
	loaded, err := host.Load(b.Context(), ExtConfig{Name: "input", Source: source, Enabled: true})
	if err != nil {
		b.Fatal(err)
	}
	handler := loaded.Handlers["input"][0]
	event := map[string]any{"type": "input", "text": "hello", "source": "rpc"}
	b.ReportAllocs()
	for b.Loop() {
		ctx := invocation.WithSuspensionAcknowledgment(b.Context(), func() { b.Error("a handler that does not wait was reported suspended") })
		if _, err := handler(event, ctx); err != nil {
			b.Fatal(err)
		}
	}
}
