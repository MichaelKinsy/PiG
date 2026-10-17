package mcp_test

// Pins packages/mcp/src/transports/transport.ts (TransportEvents) and packages/mcp/src/transports/in-memory.ts, which
// packages/mcp/test has no file for: listener bookkeeping with disposal and close-once (transport.ts:21-54), and the
// in-memory pair's start/peer/close rules and structuredClone delivery (in-memory.ts:5-51).

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/mcp"
	"github.com/MichaelKinsy/PiG/mcp/mcptest"
)

func TestTransportEventsDeliverInRegistrationOrderUntilDisposed(t *testing.T) {
	var events mcp.TransportEvents
	var order []string
	var first, second mcp.MessageListener = func(mcp.JSONRPCMessage) { order = append(order, "first") }, func(mcp.JSONRPCMessage) { order = append(order, "second") }
	var error1 mcp.ErrorListener = func(err error) { order = append(order, "error1:"+err.Error()) }
	disposeFirst := events.OnMessage(first)
	events.OnMessage(second)
	disposeError1 := events.OnError(error1)
	events.OnError(func(err error) { order = append(order, "error2:"+err.Error()) })
	events.EmitMessage(mcp.NewNotification("x", nil))
	events.EmitError(errors.New("boom"))
	if want := []string{"first", "second", "error1:boom", "error2:boom"}; !slices.Equal(order, want) {
		t.Fatalf("order = %v, want %v", order, want)
	}
	disposeFirst()
	order = nil
	events.EmitMessage(mcp.NewNotification("x", nil))
	if want := []string{"second"}; !slices.Equal(order, want) {
		t.Fatalf("after dispose order = %v, want %v", order, want)
	}
	disposeError1()
	order = nil
	events.EmitError(errors.New("again"))
	if want := []string{"error2:again"}; !slices.Equal(order, want) {
		t.Fatalf("after disposing an error listener order = %v, want %v", order, want)
	}
}

func TestTransportEventsEmitCloseOnceAndHonorDisposal(t *testing.T) {
	var events mcp.TransportEvents
	calls := 0
	var counting mcp.CloseListener = func() { calls++ }
	events.OnClose(counting)
	disposed := false
	dispose := events.OnClose(func() { disposed = true })
	dispose()
	events.EmitClose()
	events.EmitClose()
	if calls != 1 || disposed {
		t.Fatalf("calls = %d disposed listener ran = %v, want 1 and false", calls, disposed)
	}
	late := false
	events.OnClose(func() { late = true })
	events.EmitClose()
	if late {
		t.Fatal("a listener added after the close was notified by a second EmitClose")
	}
}

func TestInMemoryTransportPairDeliversCopiesInOrderAndClosesBothEnds(t *testing.T) {
	client, server := mcptest.NewInMemoryTransportPair()
	var mu sync.Mutex
	var got []mcp.JSONRPCMessage
	server.OnMessage(func(message mcp.JSONRPCMessage) {
		mu.Lock()
		defer mu.Unlock()
		got = append(got, message)
	})
	closed := map[string]int{}
	client.OnClose(func() { closed["client"]++ })
	server.OnClose(func() { closed["server"]++ })
	if err := client.Start(); err != nil {
		t.Fatal(err)
	}
	if err := server.Start(); err != nil {
		t.Fatal(err)
	}
	params := json.RawMessage(`{"n":1}`)
	first := mcp.NewRequest(mcp.NumberID(1), "first", params)
	if err := client.Send(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	params[5] = '9' // the delivered message is a copy: mutating the sender's bytes does not change it
	if err := client.Send(context.Background(), mcp.NewNotification("second", nil)); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	if len(got) != 2 || got[0].Method != "first" || got[1].Method != "second" || string(got[0].Params) != `{"n":1}` {
		t.Fatalf("delivered = %+v, want first then second with the original params", got)
	}
	mu.Unlock()

	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	if closed["client"] != 1 || closed["server"] != 1 {
		t.Fatalf("close notifications = %v, want one on each end", closed)
	}
	if err := client.Close(); err != nil || closed["client"] != 1 {
		t.Fatalf("second Close = %v, notifications = %v; want a no-op", err, closed)
	}
	var closedErr *mcp.McpConnectionClosedError
	if err := server.Send(context.Background(), mcp.NewNotification("late", nil)); !errors.As(err, &closedErr) {
		t.Fatalf("send on a closed transport = %v, want McpConnectionClosedError", err)
	}
	if err := client.Start(); !errors.As(err, &closedErr) {
		t.Fatalf("Start after Close = %v, want McpConnectionClosedError", err)
	}
}

// packages/mcp/src/transports/in-memory.ts:9: connectPeer(peer) pairs two in-memory transports.
func TestInMemoryTransportRejectsSendsBeforeStartAndToAnUnstartedOrMissingPeer(t *testing.T) {
	client, server := mcptest.NewInMemoryTransportPair()
	var closedErr *mcp.McpConnectionClosedError
	if err := client.Send(context.Background(), mcp.NewNotification("x", nil)); !errors.As(err, &closedErr) || closedErr.Error() != "MCP connection closed" {
		t.Fatalf("send before Start = %v, want the default closed message", err)
	}
	if err := client.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	if err := client.Send(context.Background(), mcp.NewNotification("x", nil)); !errors.As(err, &closedErr) || closedErr.Error() != "In-memory MCP peer is not connected" {
		t.Fatalf("send to an unstarted peer = %v", err)
	}
	_ = server

	orphan, _ := mcptest.NewInMemoryTransportPair()
	lonely := &mcptest.InMemoryTransport{}
	if err := lonely.ConnectPeer(orphan); err != nil {
		t.Fatal(err)
	}
	if err := lonely.ConnectPeer(orphan); err == nil || err.Error() != "In-memory MCP transport already has a peer" {
		t.Fatalf("second ConnectPeer = %v", err)
	}
}

// TestProtocolVersionsAreTheOnesMcpTypesDeclare pins protocol/types.ts's LATEST_PROTOCOL_VERSION and
// SUPPORTED_PROTOCOL_VERSIONS by value. packages/mcp/test compares a client request with the exported constant, so a
// changed literal passes it while every server negotiates a different version.
func TestProtocolVersionsAreTheOnesMcpTypesDeclare(t *testing.T) {
	if mcp.LatestProtocolVersion != "2025-11-25" {
		t.Fatalf("LatestProtocolVersion = %q", mcp.LatestProtocolVersion)
	}
	if want := []mcp.SupportedProtocolVersion{"2025-11-25", "2025-06-18", "2025-03-26", "2024-11-05"}; !slices.Equal(mcp.SupportedProtocolVersions, want) {
		t.Fatalf("SupportedProtocolVersions = %v, want %v", mcp.SupportedProtocolVersions, want)
	}
}
