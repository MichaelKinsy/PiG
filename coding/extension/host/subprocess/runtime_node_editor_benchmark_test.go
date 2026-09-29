package subprocess_test

import (
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

func BenchmarkEditorStateSnapshot(b *testing.B) {
	bridge := subprocess.NewUIBridge(func() {})
	table := codingagent.DefaultKeybindingsManager().ExtensionKeybindingTable()
	bridge.SetKeybindingsFunc(func() any { return table })
	state := bridge.Snapshot(nil, 0, false)
	encoded, err := json.Marshal(state)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := json.Marshal(bridge.Snapshot(nil, 0, false)); err != nil {
			b.Fatal(err)
		}
	}
	b.ReportMetric(float64(len(encoded)), "wire-bytes")
}
