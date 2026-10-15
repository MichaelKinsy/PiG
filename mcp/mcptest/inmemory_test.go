package mcptest_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/mcp"
	"github.com/MichaelKinsy/PiG/mcp/mcptest"
)

// PiG-only: packages/mcp/test has no test of InMemoryTransport itself; it is only the double under the client tests.
// These tests pin the contract of packages/mcp/src/transports/in-memory.ts.
func TestInMemoryTransportPairDeliversInOrderAndCloseNotifiesBothEnds(t *testing.T) {
	client, server := mcptest.NewInMemoryTransportPair()
	var mu sync.Mutex
	var got []string
	closes := map[string]int{}
	server.OnMessage(func(m mcp.JSONRPCMessage) { mu.Lock(); got = append(got, m.Method); mu.Unlock() })
	client.OnClose(func() { mu.Lock(); closes["client"]++; mu.Unlock() })
	server.OnClose(func() { mu.Lock(); closes["server"]++; mu.Unlock() })
	if err := client.Start(); err != nil {
		t.Fatal(err)
	}
	if err := server.Start(); err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{"a", "b", "c"} {
		if err := client.Send(context.Background(), mcp.NewNotification(method, nil)); err != nil {
			t.Fatal(err)
		}
	}
	mu.Lock()
	if len(got) != 3 || got[0] != "a" || got[1] != "b" || got[2] != "c" {
		t.Fatalf("delivered %v, want a b c in order", got)
	}
	mu.Unlock()
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	if err := client.Close(); err != nil {
		t.Fatalf("a second Close must be a no-op: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if closes["client"] != 1 || closes["server"] != 1 {
		t.Fatalf("close notifications = %v, want one each", closes)
	}
	if err := client.Send(context.Background(), mcp.NewNotification("late", nil)); !errors.As(err, new(*mcp.McpConnectionClosedError)) {
		t.Fatalf("send after close err = %v", err)
	}
}

// mutation-checked: zeroing the results of InMemoryTransport.ConnectPeer fails it
// Pi: packages/mcp/src/transports/in-memory.ts:9 (connectPeer)
// packages/mcp/src/transports/in-memory.ts:9: connectPeer(peer) pairs two in-memory transports.
func TestInMemoryTransportRefusesASecondPeerAndAnUnconnectedSend(t *testing.T) {
	a, b := mcptest.NewInMemoryTransportPair()
	if err := a.ConnectPeer(b); err == nil || err.Error() != "In-memory MCP transport already has a peer" {
		t.Fatalf("second peer err = %v", err)
	}
	if err := a.Send(context.Background(), mcp.NewNotification("early", nil)); !errors.As(err, new(*mcp.McpConnectionClosedError)) {
		t.Fatalf("send before start err = %v", err)
	}
	if err := a.Start(); err != nil {
		t.Fatal(err)
	}
	err := a.Send(context.Background(), mcp.NewNotification("peer-not-started", nil))
	var closed *mcp.McpConnectionClosedError
	if !errors.As(err, &closed) || closed.Message != "In-memory MCP peer is not connected" {
		t.Fatalf("peer not started err = %v", err)
	}
}

// packages/mcp/src/transports/in-memory.ts:35-36: emitError(error) reports a transport error to the listeners.
func TestInMemoryTransportListenersCanBeDisposedAndErrorsAreEmitted(t *testing.T) {
	var client *mcptest.InMemoryTransport
	client, _ = mcptest.NewInMemoryTransportPair()
	var count int
	var seen error
	dispose := client.OnError(func(err error) { count++; seen = err })
	boom := errors.New("boom")
	client.EmitError(boom)
	dispose()
	client.EmitError(errors.New("after dispose"))
	if count != 1 || !errors.Is(seen, boom) {
		t.Fatalf("count=%d seen=%v", count, seen)
	}
}

// packages/mcp/src/transports/in-memory.ts:26-32 send queues the delivery on the peer and :41-44 deliver drops it when the peer closed meanwhile: a message that
// is queued behind a slow listener is not delivered once the receiving end closes. SendOrdered (the Go ordering hook) calls placed once the message has its
// place in the peer's queue.
func TestInMemoryTransportDropsQueuedMessagesOfAClosedEndAndReportsPlacement(t *testing.T) {
	client, server := mcptest.NewInMemoryTransportPair()
	var mu sync.Mutex
	var delivered []string
	entered, release := make(chan struct{}), make(chan struct{})
	server.OnMessage(func(m mcp.JSONRPCMessage) {
		mu.Lock()
		delivered = append(delivered, m.Method)
		mu.Unlock()
		if m.Method == "first" {
			close(entered)
			<-release
		}
	})
	if err := client.Start(); err != nil {
		t.Fatal(err)
	}
	if err := server.Start(); err != nil {
		t.Fatal(err)
	}
	firstSent := make(chan error, 1)
	go func() { firstSent <- client.Send(context.Background(), mcp.NewNotification("first", nil)) }()
	<-entered
	placed := make(chan struct{})
	secondSent := make(chan error, 1)
	go func() {
		secondSent <- client.SendOrdered(context.Background(), mcp.NewNotification("second", nil), func() { close(placed) })
	}()
	<-placed
	closeDone := make(chan error, 1)
	go func() { closeDone <- server.Close() }()
	// Close marks the server closed before it notifies anything; the queued "second" is dropped when the slow listener lets the loop go on.
	closedBy := time.Now().Add(10 * time.Second)
	for {
		if err := server.Send(context.Background(), mcp.NewNotification("probe", nil)); err != nil {
			break
		}
		if time.Now().After(closedBy) {
			t.Fatal("the server never reported itself closed")
		}
		time.Sleep(time.Millisecond)
	}
	close(release)
	if err := <-firstSent; err != nil {
		t.Fatal(err)
	}
	if err := <-secondSent; err != nil {
		t.Fatal(err)
	}
	if err := <-closeDone; err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(delivered) != 1 || delivered[0] != "first" {
		t.Fatalf("delivered %v, want only the first message: the one queued behind it is dropped by the closed end", delivered)
	}
}
