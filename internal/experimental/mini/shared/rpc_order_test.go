package shared

import (
	"context"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
)

// Pi invokes dispatch and each handler's synchronous prefix before the next frame, even when the handler returns a Promise.
func TestRpcCallBeforeFollowingEvent(t *testing.T) {
	previous := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(previous)
	for _, kind := range []string{"dynamic", "typed", "forward"} {
		t.Run(kind, func(t *testing.T) {
			var calls atomic.Int32
			c := newRecordedConnection()
			zero := float64(0)
			options := PeerOptions{DeadMs: &zero}
			if kind == "forward" {
				options.Forward = func(context.Context, string, []json.RawMessage) (any, error) {
					calls.Add(1)
					return nil, nil
				}
			}
			p := CreatePeer(c, options)
			t.Cleanup(func() { _ = p.Close(); p.Wait() })
			switch kind {
			case "dynamic":
				if err := p.Provide(Worker, Service{"describe": func(context.Context, []json.RawMessage) (any, error) {
					calls.Add(1)
					return nil, nil
				}}); err != nil {
					t.Fatal(err)
				}
			case "typed":
				if err := Provide(p, Worker, WorkerServiceApi{Describe: func(context.Context) (WorkerDescription, error) {
					calls.Add(1)
					return WorkerDescription{}, nil
				}}); err != nil {
					t.Fatal(err)
				}
			}
			var observed int32
			p.OnEvent(func(string, json.RawMessage, *string) { observed = calls.Load() })
			c.deliver(`{"kind":"call","id":1,"method":"worker.describe","args":[]}`)
			c.deliver(`{"kind":"event","service":"worker","payload":null}`)
			if observed != 1 {
				t.Fatalf("call invocation reordered after following event: got %d calls, want 1", observed)
			}
		})
	}
}

// The same coalesced frame batch runs through Pi's createPeer and the Go JSON reader. Calls must enter in wire order and resolve their service before an event replaces it.
func TestRpcInvocationOrderUpstreamOracle(t *testing.T) {
	root, err := filepath.Abs("../../../../.upstream/current")
	if err != nil {
		t.Fatal(err)
	}
	output, err := exec.CommandContext(t.Context(), "node", "testdata/rpc-order-oracle.mjs", root).CombinedOutput()
	if err != nil {
		t.Fatalf("upstream oracle: %v: %s", err, output)
	}
	var want []string
	if err := json.Unmarshal(output, &want); err != nil {
		t.Fatalf("upstream oracle: %v: %s", err, output)
	}
	if !reflect.DeepEqual(want, []string{"old:1", "old:2", "event", "new:3", "end"}) {
		t.Fatalf("upstream invocation contract changed: %s", output)
	}
	p, q := peerPair(t)
	var mu sync.Mutex
	var trace []string
	appendTrace := func(value string) { mu.Lock(); trace = append(trace, value); mu.Unlock() }
	implementation := func(label string) Service {
		return Service{"prompt": func(_ context.Context, args []json.RawMessage) (any, error) {
			appendTrace(label + ":" + string(args[0]))
			return nil, nil
		}}
	}
	if err := q.Provide(Lane, implementation("old")); err != nil {
		t.Fatal(err)
	}
	ended := make(chan struct{})
	q.OnEvent(func(_ string, payload json.RawMessage, _ *string) {
		if string(payload) == `"replace"` {
			appendTrace("event")
			if err := q.Provide(Lane, implementation("new")); err != nil {
				t.Error(err)
			}
		} else {
			appendTrace("end")
			close(ended)
		}
	})
	// One write keeps the adjacent call/event frames in one reader batch, as in Pi's LF decoder.
	batch := "{\"kind\":\"call\",\"id\":1,\"method\":\"lane.prompt\",\"args\":[1]}\n" +
		"{\"kind\":\"call\",\"id\":2,\"method\":\"lane.prompt\",\"args\":[2]}\n" +
		"{\"kind\":\"event\",\"service\":\"lane\",\"payload\":\"replace\"}\n" +
		"{\"kind\":\"call\",\"id\":3,\"method\":\"lane.prompt\",\"args\":[3]}\n" +
		"{\"kind\":\"event\",\"service\":\"lane\",\"payload\":\"end\"}\n"
	connection := p.connection.(*JSONConnection)
	if _, err := connection.output.Write([]byte(batch)); err != nil {
		t.Fatal(err)
	}
	receive(t, ended)
	mu.Lock()
	defer mu.Unlock()
	if !reflect.DeepEqual(trace, want) {
		t.Fatalf("invocation trace: got %q, upstream %q", trace, want)
	}
}
