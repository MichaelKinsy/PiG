//go:build !pig_strip_mcp

package coding

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/mcpext"
	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
	"github.com/MichaelKinsy/PiG/mcp"
)

// The wire of updateConfig and openUrl is the documented one: Pi's entry and patch objects as JSON, and the function's error is the error of the call.
func TestGraphMcpUpdateConfigAndOpenURLWire(t *testing.T) {
	t.Parallel()
	scope := &mcpFakeScope{serve: func(id string, _ []json.RawMessage) (any, error) {
		if id == "cb-update" {
			return nil, errors.New("read-only file")
		}
		return nil, nil
	}}
	var options mcpext.Options
	graphMcpFunctions{OpenURL: "cb-open", UpdateConfig: "cb-update"}.apply(scope, &options)
	options.OpenURL("https://auth.example/authorize?state=1")
	disabled := false
	err := options.UpdateConfig(mcpext.McpServerEntry{Name: "demo", Config: extension.McpServerConfig{Command: "srv"}, Source: "/cfg/mcp.json", Scope: "global", Override: "/proj/mcp.json"}, mcpext.McpServerConfigPatch{Enabled: &disabled, Exposure: extension.McpExposureDirect})
	if err == nil || err.Error() != "read-only file" {
		t.Fatalf("updateConfig error = %v, want the extension's", err)
	}
	want := []string{
		`cb-open ["https://auth.example/authorize?state=1"]`,
		`cb-update [{"name":"demo","config":{"command":"srv"},"source":"/cfg/mcp.json","scope":"global","override":"/proj/mcp.json"},{"enabled":false,"exposure":"direct"}]`,
	}
	if got := scope.sent(); !slices.Equal(got, want) {
		t.Fatalf("wire:\n got %q\nwant %q", got, want)
	}
}

// A loadConfig that fails is a throw out of the session_start handler, as Pi's loadConfig throwing is.
func TestGraphMcpLoadConfigErrorIsAThrow(t *testing.T) {
	t.Parallel()
	scope := &mcpFakeScope{serve: func(string, []json.RawMessage) (any, error) { return nil, errors.New("config unreadable") }}
	load := graphMcpLoadConfig(scope, "cb-load")
	defer func() {
		if recovered := recover(); recovered == nil || !strings.Contains(fmt.Sprint(recovered), "config unreadable") {
			t.Fatalf("loadConfig panicked with %v, want the extension's error", recovered)
		}
	}()
	load(context.Background())
}

// The credentials object's refresh lock: lock answers when the extension's function entered, the host's function runs while it holds, and unlock carries its outcome back.
func TestGraphMcpCredentialsRefreshLock(t *testing.T) {
	t.Parallel()
	var order []string
	scope := &mcpFakeScope{serve: func(_ string, args []json.RawMessage) (any, error) {
		var op string
		_ = json.Unmarshal(args[0], &op)
		order = append(order, op)
		switch op {
		case "forServer":
			return map[string]any{"store": "s1"}, nil
		case "lock":
			return map[string]any{"lock": "s1.2"}, nil
		case "load":
			return nil, nil
		}
		return nil, nil
	}}
	store, err := (&graphMcpCredentials{scope: scope, id: "cb-cred"}).ForServer("demo", "https://x.example/mcp")
	if err != nil {
		t.Fatal(err)
	}
	if state, err := store.Load(context.Background()); err != nil || state != nil {
		t.Fatalf("Load = %v, %v; want nil, nil for an absent state", state, err)
	}
	ran := false
	failure := errors.New("refresh failed")
	err = store.WithRefreshLock(context.Background(), func(context.Context) error {
		ran = true
		order = append(order, "fn")
		return failure
	})
	if !ran || !errors.Is(err, failure) {
		t.Fatalf("WithRefreshLock ran=%v err=%v, want the function's error", ran, err)
	}
	want := []string{"forServer", "load", "lock", "fn", "unlock"}
	if !slices.Equal(order, want) {
		t.Fatalf("order = %v, want %v", order, want)
	}
	if last := scope.sent()[len(scope.sent())-1]; last != `cb-cred ["unlock","s1","s1.2","refresh failed",true]` {
		t.Fatalf("unlock request = %s", last)
	}
}

// graphMcpExtensionSide is the extension's end of a transport: what the SDK shim does, in a few lines.
type graphMcpExtensionSide struct {
	mu     sync.Mutex
	cond   *sync.Cond
	events []map[string]any
	log    []string
	// placedSeen is closed by the host's placed hook; a send holds its answer until then, so the hook cannot be satisfied by the proxy's fallback after the answer.
	placedSeen chan struct{}
}

func newGraphMcpExtensionSide() *graphMcpExtensionSide {
	e := &graphMcpExtensionSide{placedSeen: make(chan struct{})}
	e.cond = sync.NewCond(&e.mu)
	return e
}

func (e *graphMcpExtensionSide) push(event map[string]any) {
	e.mu.Lock()
	e.events = append(e.events, event)
	e.mu.Unlock()
	e.cond.Broadcast()
}

func (e *graphMcpExtensionSide) serve(_ string, args []json.RawMessage) (any, error) {
	var op string
	_ = json.Unmarshal(args[0], &op)
	e.mu.Lock()
	e.log = append(e.log, op)
	e.mu.Unlock()
	switch op {
	case "receive":
		e.mu.Lock()
		defer e.mu.Unlock()
		for len(e.events) == 0 {
			e.cond.Wait()
		}
		events := e.events
		e.events = nil
		return map[string]any{"events": events}, nil
	case "send":
		var seq uint64
		var ordered bool
		_ = json.Unmarshal(args[1], &seq)
		_ = json.Unmarshal(args[3], &ordered)
		if ordered {
			e.push(map[string]any{"type": "placed", "seq": seq})
			if e.placedSeen != nil {
				select {
				case <-e.placedSeen:
				case <-time.After(5 * time.Second):
					return nil, errors.New("the placed event never reached the host's hook")
				}
			}
		}
		e.push(map[string]any{"type": "message", "message": json.RawMessage(`{"jsonrpc":"2.0","id":1,"result":{}}`)})
	case "close":
		e.push(map[string]any{"type": "close"})
	}
	return nil, nil
}

// The transport proxy delivers the extension's events in order on its own goroutine, calls an ordered send's placed hook when the extension reports it, drops the auth provider with the transport, and closes once.
func TestGraphMcpTransportProxy(t *testing.T) {
	t.Parallel()
	side := newGraphMcpExtensionSide()
	scope := &mcpFakeScope{serve: func(id string, args []json.RawMessage) (any, error) {
		if id == "cb-factory" {
			return map[string]any{"transport": "cb-transport"}, nil
		}
		return side.serve(id, args)
	}}
	transport, err := graphMcpTransportFactory(scope, "cb-factory")(mcpext.McpServerEntry{Name: "demo"}, "/cwd", tokenProvider("tok"))
	if err != nil {
		t.Fatal(err)
	}
	if len(scope.objects) != 1 {
		t.Fatalf("auth provider objects = %d, want 1", len(scope.objects))
	}
	received := make(chan mcp.JSONRPCMessage, 2)
	closed := make(chan struct{})
	transport.OnMessage(func(message mcp.JSONRPCMessage) { received <- message })
	transport.OnClose(func() { close(closed) })
	if err := transport.Start(); err != nil {
		t.Fatal(err)
	}
	ordered, ok := transport.(mcp.OrderedSender)
	if !ok {
		t.Fatal("the proxy is not an OrderedSender")
	}
	if err := ordered.SendOrdered(context.Background(), mcp.NewRequest(mcp.NumberID(1), "ping", nil), func() { close(side.placedSeen) }); err != nil {
		t.Fatal(err)
	}
	if message := <-received; !message.IsResponse() {
		t.Fatalf("delivered %+v, want the extension's response", message)
	}
	if err := transport.Close(); err != nil {
		t.Fatal(err)
	}
	<-closed
	if err := transport.Close(); err != nil {
		t.Fatalf("second Close = %v", err)
	}
	if len(scope.removed) != 1 || len(scope.objects) != 0 {
		t.Fatalf("auth provider removed %v, objects left %d", scope.removed, len(scope.objects))
	}
	side.mu.Lock()
	defer side.mu.Unlock()
	if side.log[0] != "start" || !slices.Contains(side.log, "send") || side.log[len(side.log)-1] == "send" {
		t.Fatalf("operations = %v", side.log)
	}
}

type tokenProvider string

func (p tokenProvider) Token(context.Context) (string, error) { return string(p), nil }

// BenchmarkGraphMcpTransportSend is what the transport proxy adds in front of a send and the poll that brings the answer back, with the extension in process (no socket): one ordered send, one placed event, one message event.
func BenchmarkGraphMcpTransportSend(b *testing.B) {
	side := newGraphMcpExtensionSide()
	scope := &mcpFakeScope{serve: func(id string, args []json.RawMessage) (any, error) {
		if id == "cb-factory" {
			return map[string]any{"transport": "cb-transport"}, nil
		}
		return side.serve(id, args)
	}}
	transport, err := graphMcpTransportFactory(scope, "cb-factory")(mcpext.McpServerEntry{Name: "demo"}, "/cwd", nil)
	if err != nil {
		b.Fatal(err)
	}
	delivered := make(chan struct{}, 1)
	transport.OnMessage(func(mcp.JSONRPCMessage) { delivered <- struct{}{} })
	if err := transport.Start(); err != nil {
		b.Fatal(err)
	}
	ordered := transport.(mcp.OrderedSender)
	message := mcp.NewRequest(mcp.NumberID(1), "ping", nil)
	side.placedSeen = nil
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		// The side's send holds for the hook only when placedSeen is set; the benchmark measures the wire shape, so the hook is plain.
		if err := ordered.SendOrdered(context.Background(), message, func() {}); err != nil {
			b.Fatal(err)
		}
		<-delivered
	}
	b.StopTimer()
	_ = transport.Close()
}
