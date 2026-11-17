package subprocess_test

import (
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
)

// BenchmarkFusedToolCall is a tool call of a fused extension: the round trip every call of an extension makes through the host's per-call readers of its
// registration (the owner check, the runtime kind).
func BenchmarkFusedToolCall(b *testing.B) {
	h := subprocess.NewHost(b.TempDir())
	h.SetUIBridge(subprocess.NewUIBridge(func() {}))
	b.Cleanup(func() { h.Shutdown("benchmark done") })
	loaded, err := h.LoadInProcess(b.Context(), subprocess.ExtConfig{Name: "fused", Enabled: true}, greetExt("fused").RunWithConn)
	if err != nil {
		b.Fatal(err)
	}
	tool, ok := loaded.Tools["greet"]
	if !ok {
		b.Fatal("the extension registered no greet tool")
	}
	args := json.RawMessage(`{"name":"bench"}`)
	b.ReportAllocs()
	for b.Loop() {
		if _, err := tool.Definition.Execute(b.Context(), "tc", args, nil); err != nil {
			b.Fatal(err)
		}
	}
}
