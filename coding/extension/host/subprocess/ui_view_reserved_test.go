package subprocess

import (
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/uiview"
)

// TestViewCallsOfAnExtensionNamedChordAreRefused: the plugin name chord is reserved for the host's mounts of chord
// surfaces, so an extension of that name cannot mount under it, while any other name can.
func TestViewCallsOfAnExtensionNamedChordAreRefused(t *testing.T) {
	b := NewUIBridge(func() {})
	t.Cleanup(b.StopFrontendPushes)
	ab := uiview.ClientAbilities{Slots: []uiview.Slot{uiview.SlotPanel}, Nodes: []string{"text"},
		Limits: uiview.Limits{Nodes: 8, Depth: 4, SnapshotBytes: 4096, TextBytes: 256, Mounts: 4, IntentArgBytes: 16}}
	if err := b.SetClientAbilities(&ab); err != nil {
		t.Fatal(err)
	}
	call := &CallPayload{Method: CallUIViewMount, Args: json.RawMessage(`{"key":"web","slot":"panel","view":{"root":{"kind":"text","text":"x"}}}`)}
	res, err := b.HandleCall("chord", call)
	if err != nil || res.Error == nil || res.Error.Code != string(uiview.CodeInvalid) || len(b.Views().Snapshots()) != 0 {
		t.Fatalf("a mount by an extension named chord = %+v, %v", res, err)
	}
	res, err = b.HandleCall("chords", call)
	if err != nil || res.Error != nil || len(b.Views().Snapshots()) != 1 {
		t.Fatalf("a mount by another extension = %+v, %v", res, err)
	}
}
