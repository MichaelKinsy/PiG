package subprocess

import (
	"encoding/json"
	"net"
	"strings"
	"testing"
	"time"
)

// busRequests answers every request on side with an empty response and reports each one.
func busRequests(side net.Conn) <-chan *Envelope {
	requests := make(chan *Envelope, 16)
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
	return requests
}

type dispatchArgs struct {
	HandlerIDs []string `json:"handlerIds"`
	HandlerID  string   `json:"handlerId"`
	Channel    string   `json:"channel"`
	Emission   string   `json:"emission"`
}

func readDispatch(t *testing.T, requests <-chan *Envelope, what string) dispatchArgs {
	t.Helper()
	select {
	case env := <-requests:
		if env.Request.Method != methodEventsDispatch {
			t.Fatalf("%s: method = %s", what, env.Request.Method)
		}
		var args dispatchArgs
		if err := json.Unmarshal(env.Request.Args, &args); err != nil {
			t.Fatal(err)
		}
		return args
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
		return dispatchArgs{}
	}
}

func expectNoDispatch(t *testing.T, requests <-chan *Envelope, what string) {
	t.Helper()
	select {
	case env := <-requests:
		t.Fatalf("%s: an unexpected %s request %s", what, env.Request.Method, env.Request.Args)
	case <-time.After(150 * time.Millisecond):
	}
}

// Consecutive listeners of one connection share one events.dispatch request carrying handlerIds in registry order; a lone listener keeps the single-handler wire form; the next connection and the next run each get their own request (#112, Pi event-bus.ts:15-17).
func TestDispatchEventBatchesConsecutiveListenersOfOneConnection(t *testing.T) {
	h := NewHost(t.TempDir())
	t.Cleanup(func() { h.Shutdown("test done") })
	connA, sideA := startedPipeConn(t, "conn-a")
	connB, sideB := startedPipeConn(t, "conn-b")
	for _, l := range []*busListener{
		{conn: connA, realm: "r1", handlerID: "a1", channel: "ch"},
		{conn: connA, realm: "r1", handlerID: "a2", channel: "ch"},
		{conn: connB, realm: "r2", handlerID: "b1", channel: "ch"},
		{conn: connA, realm: "r1", handlerID: "a3", channel: "ch"},
		{conn: connA, realm: "r1", handlerID: "other-channel", channel: "other"},
	} {
		h.eventBus.add(l)
	}
	requestsA, requestsB := busRequests(sideA), busRequests(sideB)

	if !h.dispatchEvent(t.Context(), "ch", newChannelPayload("ch"), nil, "emitter", "", nil) {
		t.Fatal("dispatchEvent reported no listeners")
	}
	batch := readDispatch(t, requestsA, "the first run of conn-a")
	if len(batch.HandlerIDs) != 2 || batch.HandlerIDs[0] != "a1" || batch.HandlerIDs[1] != "a2" || batch.HandlerID != "" {
		t.Fatalf("batched args = %+v, want handlerIds [a1 a2] and no handlerId", batch)
	}
	single := readDispatch(t, requestsB, "conn-b")
	if single.HandlerID != "b1" || single.HandlerIDs != nil {
		t.Fatalf("lone listener args = %+v, want the single-handler form", single)
	}
	after := readDispatch(t, requestsA, "the second run of conn-a")
	if after.HandlerID != "a3" || after.HandlerIDs != nil {
		t.Fatalf("a run split by another connection = %+v, want a3 alone", after)
	}
	expectNoDispatch(t, requestsA, "conn-a after its two runs")
}

// A packed realm holds several extensions, each with its own connection; a handler runs in the request scope of the connection that carries its request, so a run never crosses connections even inside one realm.
func TestDispatchEventDoesNotBatchAcrossConnectionsOfOneRealm(t *testing.T) {
	h := NewHost(t.TempDir())
	t.Cleanup(func() { h.Shutdown("test done") })
	connA, sideA := startedPipeConn(t, "member-a")
	connB, sideB := startedPipeConn(t, "member-b")
	h.eventBus.add(&busListener{conn: connA, realm: "shared", handlerID: "a", channel: "ch"})
	h.eventBus.add(&busListener{conn: connB, realm: "shared", handlerID: "b", channel: "ch"})
	requestsA, requestsB := busRequests(sideA), busRequests(sideB)
	h.dispatchEvent(t.Context(), "ch", newChannelPayload("ch"), nil, "emitter", "", nil)
	if got := readDispatch(t, requestsA, "member a"); got.HandlerID != "a" || got.HandlerIDs != nil {
		t.Fatalf("member a args = %+v", got)
	}
	if got := readDispatch(t, requestsB, "member b"); got.HandlerID != "b" || got.HandlerIDs != nil {
		t.Fatalf("member b args = %+v", got)
	}
}

// A routed emit from another realm counts one pending dispatch per request, because the runtime quiesces once per request; a batch of three listeners settles after one quiesce, and a request that fails gives its single count back.
func TestDispatchEventCountsOneSettleUnitPerRequest(t *testing.T) {
	h := NewHost(t.TempDir())
	t.Cleanup(func() { h.Shutdown("test done") })
	emitterConn, _ := startedPipeConn(t, "emitter")
	conn, side := startedPipeConn(t, "listener")
	for _, id := range []string{"x", "y", "z"} {
		h.eventBus.add(&busListener{conn: conn, realm: "r2", handlerID: id, channel: "ch"})
	}
	requests := busRequests(side)
	id, emission := h.eventBus.newEmission(emitterConn)
	h.dispatchEvent(t.Context(), "ch", newChannelPayload("ch"), nil, "r1", id, emission)
	args := readDispatch(t, requests, "batched dispatch")
	if len(args.HandlerIDs) != 3 || args.Emission != id {
		t.Fatalf("args = %+v, want three handlers carrying emission %s", args, id)
	}
	h.eventBus.mu.Lock()
	pending := emission.pending[conn]
	h.eventBus.mu.Unlock()
	if pending != 1 {
		t.Fatalf("pending count = %d after one request of three listeners, want 1", pending)
	}
	h.eventBus.quiesce(conn, mustJSONObject(map[string]string{"emission": id}))
	select {
	case <-emission.done:
	default:
		t.Fatal("one quiesce did not settle the emission")
	}
}

// A native (value) listener keeps its per-listener dispatch even between listeners of one connection, so the batch around it is split.
func TestDispatchEventKeepsNativeListenersOutOfBatches(t *testing.T) {
	h := NewHost(t.TempDir())
	t.Cleanup(func() { h.Shutdown("test done") })
	conn, side := startedPipeConn(t, "mixed")
	h.eventBus.add(&busListener{conn: conn, realm: "r", handlerID: "n1", channel: "ch"})
	h.eventBus.add(&busListener{conn: conn, realm: "r", handlerID: "v", channel: "ch", value: true})
	h.eventBus.add(&busListener{conn: conn, realm: "r", handlerID: "n2", channel: "ch"})
	h.eventBus.add(&busListener{conn: conn, realm: "r", handlerID: "n3", channel: "ch"})
	requests := busRequests(side)
	h.dispatchEvent(t.Context(), "ch", newChannelPayload("ch"), nil, "emitter", "", nil)
	var got []string
	for range 3 {
		args := readDispatch(t, requests, "mixed dispatch")
		if len(args.HandlerIDs) != 0 {
			got = append(got, "batch:"+strings.Join(args.HandlerIDs, ","))
		} else {
			got = append(got, args.HandlerID)
		}
	}
	want := []string{"n1", "v", "batch:n2,n3"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("dispatch order = %v, want %v", got, want)
	}
}
