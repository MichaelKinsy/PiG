package subprocess

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension/host/invocation"
)

// BenchmarkNodeAcknowledgedEventNotify is one unawaited event (thinking_level_select) of a Node extension whose handler calls ctx.ui.notify in its synchronous part, in RPC mode,
// under an invocation acknowledgment: the path whose acknowledgment waits for the handler's call (ackHold). BenchmarkNodeToolCallEvent is the same dispatch with no acknowledgment and no call.
func BenchmarkNodeAcknowledgedEventNotify(b *testing.B) {
	root := b.TempDir()
	source := filepath.Join(root, "notify.mjs")
	if err := os.WriteFile(source, []byte(`export default function(pi){pi.on("thinking_level_select",(_event,ctx)=>{ctx.ui.notify("n","info")})}`), 0o600); err != nil {
		b.Fatal(err)
	}
	host := NewHost(root)
	host.SetMode("rpc")
	b.Cleanup(func() { host.Shutdown("benchmark done") })
	loaded, err := host.Load(b.Context(), ExtConfig{Name: "notify", Source: source, Enabled: true})
	if err != nil {
		b.Fatal(err)
	}
	handler := loaded.Handlers["thinking_level_select"][0]
	event := map[string]any{"type": "thinking_level_select", "level": "high", "previousLevel": "off"}
	b.ReportAllocs()
	for b.Loop() {
		acknowledged := false
		ctx := invocation.WithAcknowledgment(b.Context(), func() { acknowledged = true })
		if _, err := handler(event, ctx); err != nil {
			b.Fatal(err)
		}
		if !acknowledged {
			b.Fatal("the handler's invocation was never acknowledged")
		}
	}
}
