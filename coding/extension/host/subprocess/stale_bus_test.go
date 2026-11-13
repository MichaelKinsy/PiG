package subprocess

import (
	"context"
	"encoding/json"
	"testing"
)

// events.off on a listener that migrateEventBus moved to the routed bus removes it; the migrated listener once had no unsubscribe and the call panicked.
func TestEventsOffRemovesAMigratedListener(t *testing.T) {
	h := NewHostWithConfigRoot(t.TempDir(), t.TempDir())
	conn, peer := startedPipeConn(t, "member")
	me := withConn(&managedExt{config: ExtConfig{Name: "member"}, host: h, nodeRealm: true}, conn)
	h.mu.Lock()
	h.exts["member"] = me
	h.mu.Unlock()
	h.eventBus.mu.Lock()
	h.eventBus.initLocked()
	h.eventBus.local[processIdentity(me)] = me
	h.eventBus.mu.Unlock()
	go func() {
		req, err := tryReadFramed(peer)
		if err != nil {
			return
		}
		result, _ := json.Marshal(map[string]any{"realm": "realm-a", "listeners": []map[string]string{{"channel": "c", "handlerId": "h", "extension": "member"}}})
		writeFramed(t, peer, Envelope{Type: MsgResponse, ID: req.ID, Response: &ResponsePayload{Result: result}})
	}()
	if err := h.migrateEventBus(context.Background(), me); err != nil {
		t.Fatal(err)
	}
	key := busHandlerKey{"realm-a", "h"}
	if h.eventBus.lookup(key) == nil {
		t.Fatal("the migrated listener is not registered")
	}
	h.runCall(me, conn, "off", &CallPayload{Method: callEventsOff, Args: []byte(`{"handlerId":"h"}`)}, nil)
	env := readFramed(t, peer)
	for env.Type == MsgNotify {
		env = readFramed(t, peer)
	}
	if env.Type != MsgCallResult || env.CallResult == nil || env.CallResult.Error != nil {
		t.Fatalf("events.off reply = %+v", env)
	}
	if h.eventBus.lookup(key) != nil {
		t.Fatal("events.off left the migrated listener registered")
	}
}

// A stale Host that joined a cell registry still serves events.off and xref.hello, so a shared runtime that releases its listeners on invalidate survives; a Host without one refuses them as it refuses every call.
func TestStaleHostServesBusReleaseOnlyWithARegistry(t *testing.T) {
	for _, shared := range []bool{true, false} {
		h := NewHostWithConfigRoot(t.TempDir(), t.TempDir())
		if shared {
			h.ShareCells(NewCellRegistry())
		}
		h.Invalidate("stale")
		conn, peer := startedPipeConn(t, "member")
		me := withConn(&managedExt{config: ExtConfig{Name: "member"}, host: h, nodeRealm: true}, conn)
		for _, call := range []*CallPayload{
			{Method: callXrefHello, Args: []byte(`{"realm":"realm-a"}`)},
			{Method: callEventsOff, Args: []byte(`{"handlerId":"h"}`)},
		} {
			h.runCall(me, conn, "id", call, nil)
			env := readFramed(t, peer)
			refused := env.CallResult != nil && env.CallResult.Error != nil && env.CallResult.Error.Message == "stale"
			if refused == shared {
				t.Errorf("shared=%v %s reply = %+v", shared, call.Method, env.CallResult)
			}
		}
	}
}
