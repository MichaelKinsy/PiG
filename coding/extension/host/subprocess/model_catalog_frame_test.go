package subprocess

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
)

func standardCatalogFrame(b *UIBridge) ([]byte, error) {
	args, err := json.Marshal(b.ModelRegistryState())
	if err != nil {
		return nil, err
	}
	return json.Marshal(&Envelope{Type: MsgNotify, Notify: &NotifyPayload{Method: "model_registry_update", Args: args}})
}

func TestModelCatalogFramesPreserveBytesAndOrder(t *testing.T) {
	bridge := NewUIBridge(nil)
	state := map[string]any{"models": []map[string]any{{"id": "<model>\u2028", "input": []string{}, "compat": nil}}, "error": "error <detail>"}
	bridge.SetHostAction("getModelRegistryState", func() map[string]any { return state })
	first, second := NewConn("first", nil), NewConn("second", nil)
	first.modelRegistry, second.modelRegistry = true, true
	bridge.extConns = map[string]*Conn{"first": first, "second": second}
	for _, status := range []string{"initial", "changed", "changed"} {
		state["providers"] = map[string]any{"provider": map[string]any{"status": status, "configured": false}}
		want, err := standardCatalogFrame(bridge)
		if err != nil {
			t.Fatal(err)
		}
		bridge.PublishModelCatalog()
		for _, conn := range []*Conn{first, second} {
			frame := <-conn.outCh
			conn.endWork()
			if !bytes.Equal(frame.data, want) {
				t.Fatalf("catalog frame changed bytes: %s != %s", frame.data, want)
			}
		}
	}
	state["invalid"] = make(chan int)
	bridge.PublishModelCatalog()
	for _, conn := range []*Conn{first, second} {
		select {
		case <-conn.outCh:
			t.Fatal("invalid state was published")
		default:
		}
	}
}

func TestModelCatalogFrameAvoidsSecondJSONPass(t *testing.T) {
	bridge := NewUIBridge(nil)
	models := make([]map[string]any, 2000)
	for i := range models {
		models[i] = map[string]any{"id": fmt.Sprint(i), "name": "name <escaped>", "cost": map[string]any{"input": 1.25}}
	}
	bridge.SetHostAction("getModelRegistryState", func() map[string]any { return map[string]any{"models": models} })
	frame := bridge.modelCatalogUpdate()
	conn := NewConn("test", nil)
	if err := conn.sendEncoded(frame); err != nil {
		t.Fatal(err)
	}
	queued := <-conn.outCh
	conn.endWork()
	if len(frame) == 0 || len(queued.data) != len(frame) || &queued.data[0] != &frame[0] {
		t.Fatal("already-encoded catalog was copied or serialized again before queueing")
	}
}

func TestEncodedCatalogFrameKeepsSizeAndClosedChecks(t *testing.T) {
	conn := NewConn("test", nil)
	frame := make(encodedEnvelope, MaxFrameSize+1)
	if err := conn.sendEncoded(frame[:MaxFrameSize]); err != nil {
		t.Fatal(err)
	}
	<-conn.outCh
	conn.endWork()
	var tooLarge *FrameTooLargeError
	if err := conn.sendEncoded(frame); !errors.As(err, &tooLarge) || tooLarge.Size != len(frame) || tooLarge.Max != MaxFrameSize {
		t.Fatalf("oversized frame error = %v", err)
	}
	conn.closing.Store(true)
	_, standard := conn.marshalEnvelope(&Envelope{Type: MsgNotify})
	if err := conn.sendEncoded(frame); err == nil || standard == nil || err.Error() != standard.Error() {
		t.Fatalf("closed error = %v, want %v", err, standard)
	}
	select {
	case <-conn.outCh:
		t.Fatal("rejected frame entered queue")
	default:
	}
}

func TestModelCatalogEncoderRetainsGetterAndReplacement(t *testing.T) {
	bridge := NewUIBridge(nil)
	conn := NewConn("test", nil)
	conn.modelRegistry = true
	bridge.extConns = map[string]*Conn{"test": conn}
	reads, encodes := 0, 0
	bridge.SetModelCatalog(func() []map[string]any {
		reads++
		return []map[string]any{{"id": "first"}}
	}, func() (json.RawMessage, error) {
		encodes++
		return json.RawMessage(`[{"id":"first"}]`), nil
	})
	first := <-conn.outCh
	conn.endWork()
	if reads != 0 || encodes != 1 || !bytes.Contains(first.data, []byte(`"id":"first"`)) {
		t.Fatal("publication did not use the equivalent encoder")
	}
	if bridge.ModelCatalog()[0]["id"] != "first" || reads != 1 {
		t.Fatal("public value getter changed")
	}
	bridge.SetHostAction("getModels", func() []map[string]any { return []map[string]any{{"id": "replacement"}} })
	second := <-conn.outCh
	conn.endWork()
	if encodes != 1 || !bytes.Contains(second.data, []byte(`"id":"replacement"`)) {
		t.Fatal("replacement retained a stale encoder")
	}
	bridge.SetModelCatalog(func() []map[string]any { return nil }, func() (json.RawMessage, error) {
		return nil, errors.New("encoding failed")
	})
	select {
	case <-conn.outCh:
		t.Fatal("failed encoding was published")
	default:
	}
	bridge.SetActions(&HostCallbacks{GetModels: func() []map[string]any { return []map[string]any{{"id": "actions"}} }})
	bridge.PublishModelCatalog()
	last := <-conn.outCh
	conn.endWork()
	if !bytes.Contains(last.data, []byte(`"id":"actions"`)) {
		t.Fatal("SetActions retained a stale encoder")
	}
}

func BenchmarkModelCatalogFrame(b *testing.B) {
	bridge := NewUIBridge(nil)
	models := make([]map[string]any, 2000)
	for i := range models {
		models[i] = map[string]any{"id": fmt.Sprint(i), "name": "name <escaped>", "cost": map[string]any{"input": 1.25}}
	}
	bridge.SetHostAction("getModelRegistryState", func() map[string]any { return map[string]any{"models": models} })
	conn := NewConn("test", nil)
	conn.modelRegistry = true
	bridge.extConns = map[string]*Conn{"test": conn}
	b.ReportAllocs()
	for b.Loop() {
		bridge.PublishModelCatalog()
		<-conn.outCh
		conn.endWork()
	}
}
