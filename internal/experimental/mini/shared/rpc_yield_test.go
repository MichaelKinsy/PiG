package shared

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"testing/synctest"
)

func TestRpcYieldKeepsHandlerOwned(t *testing.T) {
	for _, mode := range []string{"cancel", "close"} {
		t.Run(mode, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				p, c := recordedPeer(t)
				release := make(chan struct{})
				releaseWork := sync.OnceFunc(func() { close(release) })
				defer releaseWork()
				atEntry, cancelled := make(chan error, 1), make(chan error, 1)
				if err := Provide(p, Worker, WorkerServiceApi{Describe: func(ctx context.Context) (WorkerDescription, error) {
					atEntry <- context.Cause(ctx)
					derived, cancel := context.WithCancel(ctx)
					defer cancel()
					Yield(derived)
					Yield(derived)
					<-derived.Done()
					cancelled <- context.Cause(derived)
					<-release
					return WorkerDescription{}, context.Cause(derived)
				}}); err != nil {
					t.Fatal(err)
				}
				receive(t, c.sent) // announcement
				c.deliver(`{"kind":"call","id":1,"method":"worker.describe","args":[]}`)
				want := "Connection closed"
				if mode == "cancel" {
					c.deliver(`{"kind":"cancel","id":1}`)
					want = "Cancelled by caller"
				}
				if err := p.Close(); err != nil {
					t.Fatal(err)
				}
				if err := receive(t, atEntry); err != nil {
					t.Fatalf("later %s ran before handler entry: %v", mode, err)
				}
				if err := receive(t, cancelled); err == nil || err.Error() != want {
					t.Fatalf("cancellation cause: %v, want %q", err, want)
				}
				waited := make(chan struct{})
				go func() { p.Wait(); close(waited) }()
				synctest.Wait()
				select {
				case <-waited:
					t.Fatal("Wait returned while the yielded handler still owned work")
				default:
				}
				releaseWork()
				receive(t, waited)
			})
		})
	}
}

func TestRpcYieldedHandlerErrors(t *testing.T) {
	for _, mode := range []string{"return", "panic"} {
		t.Run(mode, func(t *testing.T) {
			p, c := recordedPeer(t)
			release := make(chan struct{})
			if err := p.Provide(Lane, Service{"prompt": func(ctx context.Context, _ []json.RawMessage) (any, error) {
				Yield(ctx)
				select {
				case <-release:
				case <-ctx.Done():
					return nil, context.Cause(ctx)
				}
				if mode == "panic" {
					panic(errors.New("after await"))
				}
				return nil, errors.New("after await")
			}}); err != nil {
				t.Fatal(err)
			}
			receive(t, c.sent)
			p.OnEvent(func(string, json.RawMessage, *string) { close(release) })
			c.deliver(`{"kind":"call","id":1,"method":"lane.prompt","args":[]}`)
			c.deliver(`{"kind":"event","service":"lane","payload":null}`)
			if got := string(receive(t, c.sent)); got != `{"kind":"error","id":1,"error":"after await"}` {
				t.Fatal(got)
			}
		})
	}
}

// Pi calls forward without the incoming AbortSignal. Forwarding must still release dispatch when it awaits a reverse RPC on the same reader.
func TestRpcForwardNestedCallDoesNotInheritCancellation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c := newRecordedConnection()
		zero := float64(0)
		var p *RpcPeer
		p = CreatePeer(c, PeerOptions{DeadMs: &zero, Forward: func(ctx context.Context, method string, args []json.RawMessage) (any, error) {
			if ctx.Done() != nil || method != "away.prompt" {
				t.Error("forward inherited cancellation or lost its method")
			}
			return p.Call(ctx, "upstream.prompt", args[0])
		}})
		defer func() { _ = p.Close(); p.Wait() }()
		c.deliver(`{"kind":"call","id":42,"method":"away.prompt","args":["hello"]}`)
		if got := string(receive(t, c.sent)); got != `{"kind":"call","id":1,"method":"upstream.prompt","args":["hello"]}` {
			t.Fatal(got)
		}
		c.deliver(`{"kind":"cancel","id":42}`)
		synctest.Wait()
		if len(c.sent) != 0 {
			t.Fatalf("caller cancellation crossed the forward hop: %s", receive(t, c.sent))
		}
		c.deliver(`{"kind":"result","id":1,"result":"forwarded"}`)
		if got := string(receive(t, c.sent)); got != `{"kind":"result","id":42,"result":"forwarded"}` {
			t.Fatal(got)
		}
	})
}

func TestRpcTypedNestedCallYields(t *testing.T) {
	p, q := peerPair(t)
	remote, err := Use(q, Worker, CallOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := Provide(p, Worker, WorkerServiceApi{Describe: func(context.Context) (WorkerDescription, error) {
		return WorkerDescription{SessionID: "reverse"}, nil
	}}); err != nil {
		t.Fatal(err)
	}
	if err := Provide(q, Worker, WorkerServiceApi{Describe: remote.Describe}); err != nil {
		t.Fatal(err)
	}
	if got, err := p.Call(t.Context(), "worker.describe"); err != nil || string(got) != `{"sessionId":"reverse"}` {
		t.Fatalf("typed reverse result: %s %v", got, err)
	}
}
