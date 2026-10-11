package subprocess

import (
	"encoding/json"
	"net"
	"sync"
	"testing"
	"time"
)

// #112 §3: consecutive listeners of one realm share a single events.dispatch
// round trip. The batched wire form carries handlerIds; registry order inside
// the group is the slice order.
func TestDispatchEventBatchesSameRealmListeners(t *testing.T) {
	h := NewHost(t.TempDir())
	t.Cleanup(func() { h.Shutdown("test done") })
	connA, sideA := startedPipeConn(t, "realm-a")
	connB, sideB := startedPipeConn(t, "realm-b")
	h.eventBus.add(&busListener{conn: connA, realm: "r1", handlerID: "a", channel: "ch"})
	h.eventBus.add(&busListener{conn: connA, realm: "r1", handlerID: "b", channel: "ch"})
	h.eventBus.add(&busListener{conn: connB, realm: "r2", handlerID: "c", channel: "ch"})

	var mu sync.Mutex
	realmARequests := make(chan *Envelope, 4)
	realmBRequests := make(chan *Envelope, 4)
	serve := func(side net.Conn, out chan<- *Envelope) {
		go func() {
			for {
				env, err := readEnvelopeFrom(side)
				if err != nil {
					return
				}
				switch env.Type {
				case MsgRequest:
					if env.Request != nil {
						mu.Lock()
						out <- env
						mu.Unlock()
						_ = writeEnvelopeTo(side, &Envelope{Type: MsgResponse, ID: env.ID, Response: &ResponsePayload{}})
					}
				case MsgPing:
					_ = writeEnvelopeTo(side, &Envelope{Type: MsgPong, Pong: &PongPayload{Nonce: env.Ping.Nonce}})
				}
			}
		}()
	}
	serve(sideA, realmARequests)
	serve(sideB, realmBRequests)

	payload := newChannelPayload("ch")
	h.dispatchEvent(t.Context(), "ch", payload, nil, "emitter", "", nil)

	envA := readChan(t, realmARequests, "realm a batch")
	if envA.Request.Method != methodEventsDispatch {
		t.Fatalf("realm a method = %s", envA.Request.Method)
	}
	var args struct {
		HandlerIDs []string `json:"handlerIds"`
		HandlerID  string   `json:"handlerId"`
		Channel    string   `json:"channel"`
	}
	if err := json.Unmarshal(envA.Request.Args, &args); err != nil {
		t.Fatal(err)
	}
	if len(args.HandlerIDs) != 2 || args.HandlerIDs[0] != "a" || args.HandlerIDs[1] != "b" {
		t.Fatalf("handlerIds = %v, want [a b]", args.HandlerIDs)
	}
	if args.HandlerID != "" {
		t.Fatalf("batched dispatch must not carry handlerId, got %q", args.HandlerID)
	}

	envB := readChan(t, realmBRequests, "realm b dispatch")
	var single struct {
		HandlerIDs []string `json:"handlerIds"`
		HandlerID  string   `json:"handlerId"`
	}
	if err := json.Unmarshal(envB.Request.Args, &single); err != nil {
		t.Fatal(err)
	}
	if single.HandlerID != "c" || single.HandlerIDs != nil {
		t.Fatalf("lone listener form = %+v, want handlerId c", single)
	}
}

// A single listener of a realm keeps the pre-batching wire form so older
// runtimes stay compatible.
func TestDispatchEventKeepsSingleHandlerWireForm(t *testing.T) {
	h := NewHost(t.TempDir())
	t.Cleanup(func() { h.Shutdown("test done") })
	conn, side := startedPipeConn(t, "solo")
	h.eventBus.add(&busListener{conn: conn, realm: "r1", handlerID: "only", channel: "ch"})
	requests := make(chan *Envelope, 2)
	go func() {
		for {
			env, err := readEnvelopeFrom(side)
			if err != nil {
				return
			}
			switch env.Type {
			case MsgRequest:
				requests <- env
				_ = writeEnvelopeTo(side, &Envelope{Type: MsgResponse, ID: env.ID, Response: &ResponsePayload{}})
			case MsgPing:
				_ = writeEnvelopeTo(side, &Envelope{Type: MsgPong, Pong: &PongPayload{Nonce: env.Ping.Nonce}})
			}
		}
	}()
	h.dispatchEvent(t.Context(), "ch", newChannelPayload("ch"), nil, "r1", "", nil)
	env := readChan(t, requests, "solo dispatch")
	var args struct {
		HandlerIDs []string `json:"handlerIds"`
		HandlerID  string   `json:"handlerId"`
	}
	if err := json.Unmarshal(env.Request.Args, &args); err != nil {
		t.Fatal(err)
	}
	if args.HandlerID != "only" || args.HandlerIDs != nil {
		t.Fatalf("args = %+v, want handlerId only", args)
	}
}

// pushStateTo skips a write whose payload would not change the recipient's
// state and sends a partial payload (no tools/commands) when only light
// fields drift; a catalog change goes out in full.
func TestPushStateToSkipsIdenticalAndPartialsCatalog(t *testing.T) {
	h := NewHost(t.TempDir())
	t.Cleanup(func() { h.Shutdown("test done") })
	conn, side := startedPipeConn(t, "push")
	me := withConn(&managedExt{config: ExtConfig{Name: "push"}, host: h}, conn)
	h.exts = map[string]*managedExt{"push": me}

	bridge := NewUIBridge(func() {})
	var version uint64 = 1
	tools := []ToolInfo{{Name: "toolA", Description: "first"}}
	bridge.SetHostAction("getAllTools", func() []ToolInfo { return tools })
	bridge.SetHostAction("getAllToolsVersion", func() uint64 { return version })
	bridge.SetHostAction("getCommands", func() []CommandInfo { return []CommandInfo{{Name: "cmdA"}} })
	h.SetUIBridge(bridge)

	frames := make(chan *Envelope, 8)
	go func() {
		for {
			env, err := readEnvelopeFrom(side)
			if err != nil {
				return
			}
			switch env.Type {
			case MsgNotify:
				if env.Notify != nil && env.Notify.Method == "state_update" {
					frames <- env
				}
			case MsgRequest:
				_ = writeEnvelopeTo(side, &Envelope{Type: MsgResponse, ID: env.ID, Response: &ResponsePayload{}})
			case MsgPing:
				_ = writeEnvelopeTo(side, &Envelope{Type: MsgPong, Pong: &PongPayload{Nonce: env.Ping.Nonce}})
			}
		}
	}()

	if err := h.pushStateTo(t.Context(), me, conn); err != nil {
		t.Fatal(err)
	}
	first := readChan(t, frames, "first push")
	var payload struct {
		State struct {
			AllTools json.RawMessage `json:"allTools"`
			Commands json.RawMessage `json:"commands"`
		} `json:"state"`
	}
	if err := json.Unmarshal(first.Notify.Args, &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.State.AllTools) == 0 {
		t.Fatal("first push must carry allTools")
	}

	// Identical state: no frame may leave the host.
	if err := h.pushStateTo(t.Context(), me, conn); err != nil {
		t.Fatal(err)
	}
	select {
	case env := <-frames:
		t.Fatalf("identical push still sent %d bytes", len(env.Notify.Args))
	case <-time.After(150 * time.Millisecond):
	}

	// Catalog change: the next push carries the new tools in full.
	version = 2
	tools = []ToolInfo{{Name: "toolA", Description: "first"}, {Name: "toolB", Description: "second"}}
	if err := h.pushStateTo(t.Context(), me, conn); err != nil {
		t.Fatal(err)
	}
	second := readChan(t, frames, "catalog push")
	var next struct {
		State struct {
			AllTools json.RawMessage `json:"allTools"`
			Commands json.RawMessage `json:"commands"`
		} `json:"state"`
	}
	if err := json.Unmarshal(second.Notify.Args, &next); err != nil {
		t.Fatal(err)
	}
	if len(next.State.AllTools) == 0 {
		t.Fatal("catalog change must deliver allTools")
	}
	if len(next.State.Commands) != 0 {
		t.Fatal("unchanged commands must stay omitted in the catalog-change push")
	}
}

func readChan(t *testing.T, ch <-chan *Envelope, what string) *Envelope {
	t.Helper()
	select {
	case env := <-ch:
		return env
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
		return nil
	}
}
