package subprocess

import (
	"bytes"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// The active model's metadata is a pure function of the model, so the bridge builds and encodes it once per model the host names, and the bytes on the wire equal the map's encoding.
func TestSnapshotEncodesTheModelOncePerModelKey(t *testing.T) {
	var key atomic.Value
	var builds atomic.Int64
	models := map[string]map[string]any{
		"a": {"id": "a", "cost": map[string]any{"input": 1.5}, "input": []string{"text"}},
		"b": {"id": "b", "cost": map[string]any{"input": 3.0}, "input": []string{"text", "image"}},
	}
	key.Store("a")
	bridge := NewUIBridge(func() {})
	bridge.SetHostAction("getModelInfo", func() json.RawMessage {
		builds.Add(1)
		return modelJSON(models[key.Load().(string)])
	})
	bridge.SetHostAction("getModelKey", key.Load)
	encode := func() []byte {
		t.Helper()
		out, err := json.Marshal(bridge.Snapshot(nil, 0, false))
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	// The expected bodies come from a bridge that does not name a model identity: they encode the map directly.
	plain := NewUIBridge(func() {})
	plain.SetHostAction("getModelInfo", func() json.RawMessage { return modelJSON(models[key.Load().(string)]) })
	for _, id := range []string{"a", "b", "a"} {
		key.Store(id)
		wantBytes, err := json.Marshal(plain.Snapshot(nil, 0, false))
		if err != nil {
			t.Fatal(err)
		}
		for range 3 {
			if got := encode(); !bytes.Equal(got, wantBytes) {
				t.Fatalf("model %s: body differs from the map's encoding\n got %s\nwant %s", id, got, wantBytes)
			}
		}
	}
	if got := builds.Load(); got != 3 {
		t.Fatalf("the model metadata was built %d times over a, b, a (3 pushes each), want 3", got)
	}

	// A bridge whose model key stops holding between its two reads keeps no encoding.
	var reads atomic.Int64
	flaky := NewUIBridge(func() {})
	flaky.SetHostAction("getModelInfo", func() json.RawMessage { return modelJSON(models["a"]) })
	flaky.SetHostAction("getModelKey", func() any { return reads.Add(1) })
	for range 3 {
		flaky.Snapshot(nil, 0, false)
	}
	flaky.mu.Lock()
	valid := flaky.modelCache.valid
	flaky.mu.Unlock()
	if valid {
		t.Fatal("an encoding was kept for a key that changed while it was built")
	}
}
