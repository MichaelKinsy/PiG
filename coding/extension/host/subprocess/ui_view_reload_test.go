package subprocess

import (
	"encoding/json"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/uiview"
)

// TestSupersedeViewsEndsTheMountsOfTheInstancesAReloadReplaces: at a reload's supersede point the mounts of the named
// extensions end as broken with reason owner_gone, in the order of the names, and no other extension's mount does. The
// connection of an instance that is being replaced mounts nothing more; the connection of the instance that follows
// mounts the key under a new epoch; and the bridge forgets a superseded connection when it is cleared.
func TestSupersedeViewsEndsTheMountsOfTheInstancesAReloadReplaces(t *testing.T) {
	b := NewUIBridge(func() {})
	t.Cleanup(b.StopFrontendPushes)
	ab := uiview.ClientAbilities{Slots: []uiview.Slot{uiview.SlotPanel}, Nodes: []string{"text"},
		Limits: uiview.Limits{Nodes: 8, Depth: 4, SnapshotBytes: 4096, TextBytes: 256, Mounts: 4, IntentArgBytes: 16}}
	var mu sync.Mutex
	var delivered []uiview.Snapshot
	if _, _, err := b.Views().Attach(ab, func(d uiview.Delivery) bool {
		mu.Lock()
		delivered = append(delivered, d.Snapshot)
		mu.Unlock()
		return true
	}); err != nil {
		t.Fatal(err)
	}
	conns := map[string]*Conn{"alpha": {}, "zeta": {}, "kept": {}}
	for name, conn := range conns {
		b.RegisterExtConn(name, conn, false)
	}
	// mount makes the call of the instance on conn and returns the mount's epoch, or the code it was refused with.
	mount := func(name string, conn *Conn) (epoch uint64, code string) {
		t.Helper()
		res, err := b.handleCall(t.Context(), name, conn, &CallPayload{Method: CallUIViewMount, Args: json.RawMessage(`{"key":"board","slot":"panel","view":{"root":{"kind":"text","text":"x"}}}`)})
		if err != nil {
			t.Fatal(err)
		}
		if res.Error != nil {
			return 0, res.Error.Code
		}
		var st uiview.Status
		if err := json.Unmarshal(res.Result, &st); err != nil {
			t.Fatal(err)
		}
		return st.Mount.Epoch, ""
	}
	first := map[string]uint64{}
	for _, name := range []string{"zeta", "kept", "alpha"} {
		epoch, code := mount(name, conns[name])
		if code != "" {
			t.Fatalf("the mount of %s was refused: %s", name, code)
		}
		first[name] = epoch
	}
	mu.Lock()
	before := len(delivered)
	mu.Unlock()

	b.SupersedeViews([]string{"alpha", "zeta"})
	mu.Lock()
	ended := append([]uiview.Snapshot(nil), delivered[before:]...)
	mu.Unlock()
	if len(ended) != 2 {
		t.Fatalf("the supersede delivered %d snapshots, want the mounts of alpha and zeta: %+v", len(ended), ended)
	}
	for i, name := range []string{"alpha", "zeta"} {
		if s := ended[i]; s.Plugin != name || s.Mount.Epoch != first[name] || s.State != uiview.StateBroken || s.Reason != uiview.ReasonOwnerGone {
			t.Fatalf("delivery %d = %s %+v %s %s, want the mount of %s broken with reason owner_gone", i, s.Plugin, s.Mount, s.State, s.Reason, name)
		}
	}
	for _, s := range b.Views().Snapshots() {
		if s.Plugin == "kept" && s.State != uiview.StateLive {
			t.Fatalf("the mount of an extension the reload did not name is %s", s.State)
		}
	}

	if _, code := mount("zeta", conns["zeta"]); code != string(uiview.CodeTerminal) {
		t.Fatalf("a mount over the connection of the superseded instance: code %q, want %s", code, uiview.CodeTerminal)
	}
	next := &Conn{}
	b.RegisterExtConn("zeta", next, false)
	if epoch, code := mount("zeta", next); code != "" || epoch <= first["zeta"] {
		t.Fatalf("the mount of the instance that follows: epoch %d code %q, want an epoch above %d", epoch, code, first["zeta"])
	}
	b.ClearExtensionConn("zeta", conns["zeta"])
	b.ClearExtension("alpha")
	b.mu.RLock()
	held := len(b.viewSuperseded)
	b.mu.RUnlock()
	if held != 0 {
		t.Fatalf("the bridge still holds %d superseded connections after they were cleared", held)
	}
	live := 0
	for _, s := range b.Views().Snapshots() {
		if s.Plugin == "zeta" && s.State == uiview.StateLive {
			live++
		}
	}
	if live != 1 {
		t.Fatalf("after the old connection was cleared zeta has %d live mounts, want the one of the instance that follows", live)
	}
}
