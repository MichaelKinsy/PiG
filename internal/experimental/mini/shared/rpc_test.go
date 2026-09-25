package shared

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func peerPair(t *testing.T) (*RpcPeer, *RpcPeer) {
	t.Helper()
	a, b := net.Pipe()
	left, right := JsonConnection(a, a, nil), JsonConnection(b, b, nil)
	zero := float64(0)
	p, q := CreatePeer(left, PeerOptions{DeadMs: &zero}), CreatePeer(right, PeerOptions{DeadMs: &zero})
	t.Cleanup(func() {
		_ = p.Close()
		_ = q.Close()
		p.Wait()
		q.Wait()
		left.Wait()
		right.Wait()
	})
	return p, q
}

// mini/shared/rpc.ts dispatches calls without awaiting previous calls; replies correlate by ID, not completion order.
func TestRpcConcurrentCallsEventsAndNestedCall(t *testing.T) {
	p, q := peerPair(t)
	echo := DefineService[struct{}, json.RawMessage]("echo")
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	if err := p.Provide(echo, Service{"reply": func(_ context.Context, args []json.RawMessage) (any, error) { return args[0], nil }}); err != nil {
		t.Fatal(err)
	}
	if err := q.Provide(echo, Service{
		"slow": func(ctx context.Context, args []json.RawMessage) (any, error) {
			entered <- struct{}{}
			Yield(ctx)
			select {
			case <-release:
			case <-ctx.Done():
				return nil, context.Cause(ctx)
			}
			return args[0], nil
		},
		"fast": func(ctx context.Context, args []json.RawMessage) (any, error) {
			return q.Call(ctx, "echo.reply", args[0])
		},
	}); err != nil {
		t.Fatal(err)
	}
	event := make(chan string, 1)
	p.OnEvent(func(service string, payload json.RawMessage, to *string) {
		event <- fmt.Sprintf("%s:%s:%s", service, payload, *to)
	})
	slow := make(chan json.RawMessage, 1)
	go func() {
		result, err := p.Call(t.Context(), "echo.slow", 1)
		if err != nil {
			t.Error(err)
		}
		slow <- result
	}()
	receive(t, entered)
	if err := q.EmitTo(echo, map[string]int{"n": 3}, "view"); err != nil {
		t.Fatal(err)
	}
	if got := receive(t, event); got != `echo:{"n":3}:view` {
		t.Fatal(got)
	}
	fast, err := p.Call(t.Context(), "echo.fast", 2)
	if err != nil || string(fast) != "2" {
		t.Fatalf("fast=%s err=%v", fast, err)
	}
	close(release)
	if got := string(receive(t, slow)); got != "1" {
		t.Fatal(got)
	}
}

func TestRpcCancellationAndClose(t *testing.T) {
	for _, mode := range []string{"cancel", "timeout", "close"} {
		t.Run(mode, func(t *testing.T) {
			p, q := peerPair(t)
			entered := make(chan struct{})
			cause := make(chan string, 1)
			service := DefineService[struct{}, struct{}]("work")
			if err := q.Provide(service, Service{"wait": func(ctx context.Context, _ []json.RawMessage) (any, error) {
				close(entered)
				Yield(ctx)
				<-ctx.Done()
				cause <- context.Cause(ctx).Error()
				return nil, context.Cause(ctx)
			}}); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			result := make(chan error, 1)
			timeout := float64(100)
			options := CallOptions{}
			if mode == "timeout" {
				options.TimeoutMs = &timeout
			}
			go func() { _, err := p.CallWith(ctx, options, "work.wait"); result <- err }()
			receive(t, entered)
			want, remote := "Call cancelled", "Cancelled by caller"
			switch mode {
			case "cancel":
				cancel()
			case "timeout":
				want = "work.wait timed out after 100ms"
			case "close":
				want, remote = "Connection closed", "Connection closed"
				if err := p.Close(); err != nil {
					t.Fatal(err)
				}
			}
			if err := receive(t, result); err == nil || err.Error() != want {
				t.Fatalf("error=%v want=%q", err, want)
			}
			if got := receive(t, cause); got != remote {
				t.Fatalf("remote cause=%q want=%q", got, remote)
			}
			p.mu.Lock()
			pending := len(p.pending)
			p.mu.Unlock()
			if pending != 0 {
				t.Fatal("abandoned waiter retained")
			}
		})
	}
}

func TestRpcPrecancelledErrorsAndNull(t *testing.T) {
	p, q := peerPair(t)
	local := DefineService[struct{}, struct{}]("local")
	if err := q.Provide(local, Service{"void": func(context.Context, []json.RawMessage) (any, error) { return nil, nil }, "fail": func(context.Context, []json.RawMessage) (any, error) { return nil, errors.New("broken") }}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := p.Call(ctx, "local.void"); err == nil || err.Error() != "Call cancelled" {
		t.Fatal(err)
	}
	for _, tt := range []struct{ method, want string }{{"local.absent", "Unknown method: local.absent"}, {"absent.call", "No service provides absent.call"}, {"bare", "No service provides bare"}, {"local.fail", "broken"}} {
		if _, err := p.Call(t.Context(), tt.method); err == nil || err.Error() != tt.want {
			t.Fatalf("%s: %v", tt.method, err)
		}
	}
	if got, err := p.Call(t.Context(), "local.void"); err != nil || string(got) != "null" {
		t.Fatalf("void=%s err=%v", got, err)
	}
}

func TestRpcForwardAnnounceAndReplacement(t *testing.T) {
	a, b := net.Pipe()
	left, right := JsonConnection(a, a, nil), JsonConnection(b, b, nil)
	zero := float64(0)
	p := CreatePeer(left, PeerOptions{DeadMs: &zero})
	q := CreatePeer(right, PeerOptions{DeadMs: &zero, Forward: func(_ context.Context, method string, args []json.RawMessage) (any, error) {
		return map[string]any{"method": method, "args": args}, nil
	}})
	t.Cleanup(func() {
		_ = p.Close()
		_ = q.Close()
		p.Wait()
		q.Wait()
		left.Wait()
		right.Wait()
	})
	one, two := DefineService[struct{}, struct{}]("one"), DefineService[struct{}, struct{}]("two")
	for _, token := range []Token{one, two, one} {
		if err := q.Provide(token, Service{"call": func(context.Context, []json.RawMessage) (any, error) { return "replacement", nil }}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := p.Call(t.Context(), "one.call"); err != nil {
		t.Fatal(err)
	}
	if got := p.Announced(); !reflect.DeepEqual(got, []string{"one", "two"}) {
		t.Fatal(got)
	}
	if got := q.Provided(); !reflect.DeepEqual(got, []string{"one", "two"}) {
		t.Fatal(got)
	}
	if got, err := p.Call(t.Context(), "away.call", true); err != nil || string(got) != `{"args":[true],"method":"away.call"}` {
		t.Fatalf("%s %v", got, err)
	}
	if _, err := p.Call(t.Context(), "one.missing"); err == nil || err.Error() != "Unknown method: one.missing" {
		t.Fatal(err)
	}
}

func TestRpcTypedServices(t *testing.T) {
	p, q := peerPair(t)
	if err := Provide(q, Worker, WorkerServiceApi{Describe: func(context.Context) (WorkerDescription, error) { return WorkerDescription{SessionID: "session"}, nil }}); err != nil {
		t.Fatal(err)
	}
	remote, err := Use(p, Worker, CallOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := remote.Describe(t.Context()); err != nil || got.SessionID != "session" {
		t.Fatalf("%+v %v", got, err)
	}
	reply := make(chan *string, 1)
	if err := Provide(q, Models, ModelsServiceApi{AuthReply: func(_ context.Context, _ string, answer *string) error { reply <- answer; return nil }}); err != nil {
		t.Fatal(err)
	}
	models, err := Use(p, Models, CallOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := models.AuthReply(t.Context(), "prompt", nil); err != nil {
		t.Fatal(err)
	}
	if receive(t, reply) != nil {
		t.Fatal("null answer lost")
	}
	events := make(chan ModelsEvent, 1)
	On(p, Models, func(event ModelsEvent) { events <- event })
	if err := q.Emit(Models, ModelsEvent{Type: "state", State: &ModelsState{Models: []ModelSummary{}, Accounts: []ProviderAccount{}, Refreshing: true}}); err != nil {
		t.Fatal(err)
	}
	if got := receive(t, events); got.State == nil || !got.State.Refreshing {
		t.Fatalf("%+v", got)
	}
}

func TestRpcManyCalls(t *testing.T) {
	p, q := peerPair(t)
	token := DefineService[struct{}, struct{}]("echo")
	if err := q.Provide(token, Service{"value": func(_ context.Context, args []json.RawMessage) (any, error) { return args[0], nil }}); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := range 200 {
		wg.Go(func() {
			got, err := p.Call(t.Context(), "echo.value", i)
			if err != nil || string(got) != fmt.Sprint(i) {
				t.Errorf("id %d: %s %v", i, got, err)
			}
		})
	}
	wg.Wait()
}

func TestRpcLiveness(t *testing.T) {
	a, b := net.Pipe()
	left, right := JsonConnection(a, a, nil), JsonConnection(b, b, nil)
	dead := float64(90)
	p := CreatePeer(left, PeerOptions{DeadMs: &dead})
	closed := make(chan struct{})
	p.OnClose(func() { close(closed) })
	pings := make(chan struct{}, 10)
	right.OnMessage(func(m json.RawMessage) {
		if string(m) == `{"kind":"ping"}` {
			pings <- struct{}{}
		}
	})
	t.Cleanup(func() { _ = p.Close(); _ = right.Close(); p.Wait(); left.Wait(); right.Wait() })
	receive(t, pings)
	receive(t, closed)
}

func TestRpcCloseDrainsConcurrentHandlers(t *testing.T) {
	p, q := peerPair(t)
	const calls = 80
	entered := make(chan struct{}, calls)
	answers := make(chan error, calls)
	if err := q.Provide(Worker, Service{"wait": func(ctx context.Context, _ []json.RawMessage) (any, error) {
		entered <- struct{}{}
		Yield(ctx)
		<-ctx.Done()
		return nil, context.Cause(ctx)
	}}); err != nil {
		t.Fatal(err)
	}
	for range calls {
		go func() { _, err := p.Call(t.Context(), "worker.wait"); answers <- err }()
	}
	for range calls {
		receive(t, entered)
	}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	for range calls {
		if err := receive(t, answers); err == nil || err.Error() != "Connection closed" {
			t.Fatal(err)
		}
	}
	q.Wait()
	p.mu.Lock()
	pending := len(p.pending)
	p.mu.Unlock()
	q.mu.Lock()
	inflight := len(q.inflight)
	q.mu.Unlock()
	if pending != 0 || inflight != 0 {
		t.Fatalf("retained pending=%d inflight=%d", pending, inflight)
	}
}

func TestRpcLargeResultAndServiceReplacement(t *testing.T) {
	p, q := peerPair(t)
	for _, want := range []string{"old", strings.Repeat("é", 1024*1024), "new"} {
		if err := q.Provide(Worker, Service{"describe": func(context.Context, []json.RawMessage) (any, error) { return want, nil }}); err != nil {
			t.Fatal(err)
		}
		got, err := p.Use(Worker, CallOptions{}).Call(t.Context(), "describe")
		if err != nil {
			t.Fatal(err)
		}
		var value string
		if err := json.Unmarshal(got, &value); err != nil {
			t.Fatal(err)
		}
		if value != want {
			t.Fatal("replacement or large result lost")
		}
	}
}

func BenchmarkRpcRoundTrip(b *testing.B) {
	a, z := net.Pipe()
	left, right := JsonConnection(a, a, nil), JsonConnection(z, z, nil)
	zero := float64(0)
	p, q := CreatePeer(left, PeerOptions{DeadMs: &zero}), CreatePeer(right, PeerOptions{DeadMs: &zero})
	defer func() {
		_ = p.Close()
		_ = q.Close()
		p.Wait()
		q.Wait()
		left.Wait()
		right.Wait()
	}()
	if err := q.Provide(Worker, WorkerServiceApi{Describe: func(context.Context) (WorkerDescription, error) { return WorkerDescription{SessionID: "session"}, nil }}); err != nil {
		b.Fatal(err)
	}
	remote, err := Use(p, Worker, CallOptions{})
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := remote.Describe(b.Context()); err != nil {
			b.Fatal(err)
		}
	}
}
