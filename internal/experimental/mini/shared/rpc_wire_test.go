package shared

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sync"
	"testing"
)

type recordedConnection struct {
	mu       sync.Mutex
	sent     chan json.RawMessage
	handlers []func(json.RawMessage)
	closers  []func()
	closed   bool
}

func newRecordedConnection() *recordedConnection {
	return &recordedConnection{sent: make(chan json.RawMessage, 32)}
}
func (c *recordedConnection) Send(value any) error {
	line, err := json.Marshal(value)
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.closed {
		c.sent <- line
	}
	return nil
}
func (c *recordedConnection) OnMessage(handler func(json.RawMessage)) {
	c.mu.Lock()
	c.handlers = append(c.handlers, handler)
	c.mu.Unlock()
}
func (c *recordedConnection) OnClose(handler func()) {
	c.mu.Lock()
	c.closers = append(c.closers, handler)
	c.mu.Unlock()
}
func (c *recordedConnection) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	handlers := append([]func(){}, c.closers...)
	c.mu.Unlock()
	for _, handler := range handlers {
		handler()
	}
	return nil
}
func (c *recordedConnection) deliver(wire string) {
	c.mu.Lock()
	handlers := append([]func(json.RawMessage){}, c.handlers...)
	c.mu.Unlock()
	for _, handler := range handlers {
		handler(json.RawMessage(wire))
	}
}

func recordedPeer(t *testing.T) (*RpcPeer, *recordedConnection) {
	t.Helper()
	c := newRecordedConnection()
	zero := float64(0)
	p := CreatePeer(c, PeerOptions{DeadMs: &zero})
	t.Cleanup(func() { _ = p.Close(); p.Wait() })
	return p, c
}

func TestRpcCallAfterCloseStillUsesCallOptions(t *testing.T) {
	p, c := recordedPeer(t)
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	timeout := float64(1)
	if _, err := p.CallWith(t.Context(), CallOptions{TimeoutMs: &timeout}, "worker.describe"); err == nil || err.Error() != "worker.describe timed out after 1ms" {
		t.Fatalf("closed send must remain a no-op until the call's timeout: %v", err)
	}
	if len(c.sent) != 0 {
		t.Fatal("closed connection sent a frame")
	}
}

func TestRpcWireCancellationAndIDs(t *testing.T) {
	p, c := recordedPeer(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := p.Call(ctx, "lane.prompt", "cancelled before send"); err == nil {
		t.Fatal("pre-cancelled call succeeded")
	}
	if len(c.sent) != 0 {
		t.Fatal("pre-cancelled call reached wire")
	}
	ctx, cancel = context.WithCancel(t.Context())
	defer cancel()
	finished := make(chan error, 1)
	go func() { _, err := p.Call(ctx, "lane.prompt", "hello"); finished <- err }()
	if got := string(receive(t, c.sent)); got != `{"kind":"call","id":2,"method":"lane.prompt","args":["hello"]}` {
		t.Fatal(got)
	}
	cancel()
	if got := string(receive(t, c.sent)); got != `{"kind":"cancel","id":2}` {
		t.Fatal(got)
	}
	if err := receive(t, finished); err == nil || err.Error() != "Call cancelled" {
		t.Fatal(err)
	}
	c.deliver(`{"kind":"result","id":2,"result":"late"}`)
	c.deliver(`{"kind":"error","id":999,"error":"unknown"}`)
	p.mu.Lock()
	retained := len(p.pending)
	p.mu.Unlock()
	if retained != 0 {
		t.Fatal("late response recreated pending call")
	}
}

func TestRpcWireAnnouncementsAndEventOrder(t *testing.T) {
	p, c := recordedPeer(t)
	for _, token := range []Token{Lane, Worker, Lane} {
		if err := p.Provide(token, Service{}); err != nil {
			t.Fatal(err)
		}
	}
	for _, want := range []string{`{"kind":"announce","services":["lane"]}`, `{"kind":"announce","services":["lane","worker"]}`, `{"kind":"announce","services":["lane","worker"]}`} {
		if got := string(receive(t, c.sent)); got != want {
			t.Fatalf("%s != %s", got, want)
		}
	}
	c.deliver(`{"kind":"announce","services":["first","second","first"]}`)
	if got := p.Announced(); !reflect.DeepEqual(got, []string{"first", "second"}) {
		t.Fatal(got)
	}
	c.deliver(`{"kind":"announce","services":[]}`)
	if len(p.Announced()) != 0 {
		t.Fatal("old announcement retained")
	}
	var order []string
	p.OnEvent(func(string, json.RawMessage, *string) { order = append(order, "router") })
	p.On(Lane, func(json.RawMessage) { order = append(order, "lane") })
	p.On(Models, func(json.RawMessage) { order = append(order, "wrong") })
	c.deliver(`{"kind":"event","service":"lane","payload":null}`)
	if !reflect.DeepEqual(order, []string{"router", "lane"}) {
		t.Fatal(order)
	}
	empty := ""
	for _, to := range []*string{nil, &empty} {
		if err := p.EmitRaw("lane", nil, to); err != nil {
			t.Fatal(err)
		}
	}
	for _, want := range []string{`{"kind":"event","service":"lane","payload":null}`, `{"kind":"event","service":"lane","payload":null,"to":""}`} {
		if got := string(receive(t, c.sent)); got != want {
			t.Fatalf("%s != %s", got, want)
		}
	}
}

func TestRpcHandlerPanicIsRemoteError(t *testing.T) {
	p, q := peerPair(t)
	if err := q.Provide(Worker, Service{"describe": func(context.Context, []json.RawMessage) (any, error) { panic(errors.New("handler threw")) }}); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Call(t.Context(), "worker.describe"); err == nil || err.Error() != "handler threw" {
		t.Fatalf("remote error: %v", err)
	}
}

func TestRpcUnknownFrameTerminatesPeer(t *testing.T) {
	p, c := recordedPeer(t)
	finished := make(chan error, 1)
	go func() { _, err := p.Call(t.Context(), "worker.describe"); finished <- err }()
	receive(t, c.sent)
	c.deliver(`{"kind":"unexpected","extra":true}`)
	if err := receive(t, finished); err == nil || err.Error() != "Connection closed" {
		t.Fatal(err)
	}
	if err := p.Err(); err == nil || err.Error() != `Unknown frame: {"kind":"unexpected","extra":true}` {
		t.Fatal(err)
	}
}
