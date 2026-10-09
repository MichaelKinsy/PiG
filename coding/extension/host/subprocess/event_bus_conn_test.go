package subprocess

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
)

// startedPipeConn returns a started Conn and the extension side of its pipe.
func startedPipeConn(t *testing.T, name string) (*Conn, net.Conn) {
	t.Helper()
	hostSide, extSide := net.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	conn := NewConn(name, hostSide)
	conn.Start(ctx)
	t.Cleanup(func() {
		cancel()
		_ = extSide.Close()
		<-conn.Done()
	})
	return conn, extSide
}

// An adoption replaces managedExt.conn while the connection's own read loop, the close hook and event-bus migration read it. Every writer stores under Host.mu and every reader loads it atomically; the race detector reports any plain access.
func TestManagedConnAdoptionRacesItsReaders(t *testing.T) {
	h := NewHostWithConfigRoot(t.TempDir(), t.TempDir())
	newClosedConn := func(name string) *Conn {
		hostSide, peer := net.Pipe()
		_ = peer.Close()
		t.Cleanup(func() { _ = hostSide.Close() })
		return NewConn(name, hostSide)
	}
	first, second := newClosedConn("first"), newClosedConn("second")
	me := withConn(&managedExt{config: ExtConfig{Name: "member"}, host: h, nodeRealm: true}, first)
	h.mu.Lock()
	h.exts["member"] = me
	h.mu.Unlock()
	h.eventBus.mu.Lock()
	h.eventBus.initLocked()
	h.eventBus.local[processIdentity(me)] = me
	h.eventBus.mu.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var wg sync.WaitGroup
	wg.Go(func() {
		for i := range 200 {
			next := first
			if i%2 == 0 {
				next = second
			}
			h.mu.Lock()
			me.setConnLocked(next)
			h.mu.Unlock()
		}
	})
	wg.Go(func() {
		for range 200 {
			_ = h.migrateEventBus(ctx, me)
		}
	})
	wg.Go(func() {
		for range 200 {
			h.forgetLocalRealm(second)
		}
	})
	wg.Go(func() {
		for range 200 {
			if conn := me.connection(); conn != first && conn != second {
				t.Errorf("connection() = %p, want first or second", conn)
			}
		}
	})
	wg.Wait()
}

// A host call is answered on the connection that delivered it, and an event-bus listener it registers belongs to that connection, even after an adoption has made another connection current.
func TestHostCallAnswersOnDeliveringConn(t *testing.T) {
	h := NewHostWithConfigRoot(t.TempDir(), t.TempDir())
	delivering, deliveringPeer := startedPipeConn(t, "delivering")
	replacement, replacementPeer := startedPipeConn(t, "replacement")
	me := withConn(&managedExt{config: ExtConfig{Name: "caller"}, host: h, nodeRealm: true}, delivering)

	h.runCall(me, delivering, "hello", &CallPayload{Method: callXrefHello, Args: []byte(`{"realm":"realm-a"}`)}, nil)
	if env := readFramed(t, deliveringPeer); env.Type != MsgCallResult || env.ID != "hello" || env.CallResult == nil || env.CallResult.Error != nil {
		t.Fatalf("xref.hello reply = %+v", env)
	}

	h.mu.Lock()
	me.setConnLocked(replacement)
	h.mu.Unlock()

	h.runCall(me, delivering, "on", &CallPayload{Method: callEventsOn, Args: []byte(`{"channel":"c","handlerId":"h"}`)}, nil)
	env := readFramed(t, deliveringPeer)
	if env.Type != MsgCallResult || env.ID != "on" || env.CallResult == nil || env.CallResult.Error != nil {
		t.Fatalf("events.on reply on the delivering connection = %+v", env)
	}
	h.eventBus.mu.Lock()
	listener := h.eventBus.handlers[busHandlerKey{"realm-a", "h"}]
	h.eventBus.mu.Unlock()
	if listener == nil || listener.conn != delivering {
		t.Fatalf("listener = %+v, want it bound to the delivering connection", listener)
	}

	// The replacement's first frame is this sentinel, so it received no reply.
	if err := replacement.Send(&Envelope{Type: MsgNotify, Notify: &NotifyPayload{Method: "sentinel"}}); err != nil {
		t.Fatal(err)
	}
	if env := readFramed(t, replacementPeer); env.Type != MsgNotify || env.Notify == nil || env.Notify.Method != "sentinel" {
		t.Fatalf("replacement connection received %+v before the sentinel", env)
	}
}

// setConnLocked is the only store of managedExt.conn, and each call holds Host.mu, so a Host.mu holder observes a stable connection.
func TestManagedConnStoresHoldHostLock(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	calls := 0
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		file, err := parser.ParseFile(fset, name, src, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				switch sel.Sel.Name {
				case "Store":
					if inner, ok := sel.X.(*ast.SelectorExpr); ok && inner.Sel.Name == "conn" && fn.Name.Name != "setConnLocked" {
						t.Errorf("%s: %s stores managedExt.conn outside setConnLocked", fset.Position(call.Pos()), fn.Name.Name)
					}
				case "setConnLocked":
					calls++
					if !hostLockHeldAt(fn.Body, call.Pos()) {
						t.Errorf("%s: %s calls setConnLocked without holding Host.mu", fset.Position(call.Pos()), fn.Name.Name)
					}
				}
				return true
			})
		}
	}
	if calls == 0 {
		t.Fatal("no setConnLocked call found")
	}
}

// hostLockHeldAt reports whether the last h.mu Lock or Unlock call in body before pos is a Lock.
func hostLockHeldAt(body *ast.BlockStmt, pos token.Pos) bool {
	held := false
	var last token.Pos
	ast.Inspect(body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || call.Pos() >= pos || call.Pos() < last {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || (sel.Sel.Name != "Lock" && sel.Sel.Name != "Unlock") {
			return true
		}
		mu, ok := sel.X.(*ast.SelectorExpr)
		if !ok || mu.Sel.Name != "mu" {
			return true
		}
		if host, ok := mu.X.(*ast.Ident); !ok || host.Name != "h" {
			return true
		}
		last = call.Pos()
		held = sel.Sel.Name == "Lock"
		return true
	})
	return held
}

// A packed Node member is listed as an in-heap realm before its socket is accepted, so another connection's close reads its managedExt.conn (forgetLocalRealm, under Host.mu) while acceptPackedExt adopts the member's connection. The adoption must write under the same lock: the race detector reports the unlocked write against the locked read.
func TestPackedAcceptWritesConnUnderHostLock(t *testing.T) {
	h := NewHostWithConfigRoot(t.TempDir(), t.TempDir())
	process := &packedProcessState{node: true, waitDone: make(chan struct{})}
	process.waitOnce.Do(func() {})
	me := &managedExt{config: ExtConfig{Name: "member"}, host: h, packedProcess: process, nodeRealm: true}
	h.noteNodeRealm(me, false)
	other := func() *Conn {
		hostSide, peer := net.Pipe()
		_ = peer.Close()
		t.Cleanup(func() { _ = hostSide.Close() })
		return NewConn("other", hostSide)
	}()
	for range 50 {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		accepted := make(chan struct{})
		go func() {
			defer close(accepted)
			_, _ = h.acceptPackedExt(context.Background(), me, ln)
		}()
		client, err := net.Dial("tcp", ln.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		// The member closes before it registers, so the accept ends after adopting the connection.
		_ = client.Close()
		done := false
		for !done {
			h.forgetLocalRealm(other)
			select {
			case <-accepted:
				done = true
			default:
			}
		}
		_ = ln.Close()
		h.noteNodeRealm(me, false)
	}
}

// The cross-process reference call handler binds, holds and releases on the connection that delivered the call while an adoption replaces the current one.
func TestXrefCallUsesItsDeliveringConn(t *testing.T) {
	h := NewHostWithConfigRoot(t.TempDir(), t.TempDir())
	newClosedConn := func(name string) *Conn {
		hostSide, peer := net.Pipe()
		_ = peer.Close()
		t.Cleanup(func() { _ = hostSide.Close() })
		return NewConn(name, hostSide)
	}
	delivering, replacement := newClosedConn("delivering"), newClosedConn("replacement")
	me := withConn(&managedExt{config: ExtConfig{Name: "caller"}, host: h, nodeRealm: true}, delivering)
	var wg sync.WaitGroup
	wg.Go(func() {
		for i := range 200 {
			next := delivering
			if i%2 == 0 {
				next = replacement
			}
			h.mu.Lock()
			me.setConnLocked(next)
			h.mu.Unlock()
		}
	})
	wg.Go(func() {
		for range 200 {
			_, _ = h.handleXrefCall(context.Background(), me, delivering, "call", &CallPayload{Method: callXrefOp, Args: []byte(`{"ref":{},"args":[]}`)})
			_, _ = h.handleXrefCall(context.Background(), me, delivering, "", &CallPayload{Method: callXrefHello, Args: []byte(`{"realm":""}`)})
		}
	})
	wg.Wait()
	if _, err := h.handleXrefCall(context.Background(), me, delivering, "", &CallPayload{Method: callXrefHello, Args: []byte(`{"realm":"realm-a"}`)}); err != nil {
		t.Fatal(err)
	}
	if got, other := h.xref.realmOf(delivering), h.xref.realmOf(replacement); got != "realm-a" || other != "" {
		t.Fatalf("realmOf(delivering) = %q, realmOf(replacement) = %q; want realm-a and none", got, other)
	}
}

// A host call keeps answering on the connection that delivered it when an adoption makes another connection current while its handler runs (transplanted from xproc-race-a's TestRunCallAnswersOnTheConnectionThatDeliveredIt).
func TestHostCallAnswersOnDeliveringConnDuringAdoption(t *testing.T) {
	h := NewHostWithConfigRoot(t.TempDir(), t.TempDir())
	delivering, deliveringPeer := startedPipeConn(t, "delivering")
	replacement, replacementPeer := startedPipeConn(t, "replacement")
	me := withConn(&managedExt{config: ExtConfig{Name: "member"}, host: h}, delivering)
	h.SetCallHandler(func(string, *CallPayload) (*CallResultPayload, error) {
		h.mu.Lock()
		me.setConnLocked(replacement)
		h.mu.Unlock()
		return &CallResultPayload{Result: []byte(`"answer"`)}, nil
	})
	h.runCall(me, delivering, "call-1", &CallPayload{Method: "custom"}, nil)
	if env := readFramed(t, deliveringPeer); env.Type != MsgCallResult || env.ID != "call-1" || env.CallResult == nil || string(env.CallResult.Result) != `"answer"` {
		t.Fatalf("delivering connection received %+v, want the call result", env)
	}
	// The replacement's first frame is this sentinel, so it received no reply.
	if err := replacement.Send(&Envelope{Type: MsgNotify, Notify: &NotifyPayload{Method: "sentinel"}}); err != nil {
		t.Fatal(err)
	}
	if env := readFramed(t, replacementPeer); env.Type != MsgNotify || env.Notify == nil || env.Notify.Method != "sentinel" {
		t.Fatalf("replacement connection received %+v before the sentinel", env)
	}
}

// Pi loader.ts:201-210,198-199: an extension's events.on subscription is retained by the runtime and is unsubscribed when the runtime is invalidated; events.off unsubscribes it once.
func TestHostEventBusListenerIsRetiredByRuntimeInvalidate(t *testing.T) {
	h := NewHostWithConfigRoot(t.TempDir(), t.TempDir())
	conn, peer := startedPipeConn(t, "listener")
	me := withConn(&managedExt{config: ExtConfig{Name: "listener"}, host: h, nodeRealm: true}, conn)
	h.runCall(me, conn, "hello", &CallPayload{Method: callXrefHello, Args: []byte(`{"realm":"realm-a"}`)}, nil)
	readFramed(t, peer)
	for _, id := range []string{"kept", "dropped"} {
		h.runCall(me, conn, id, &CallPayload{Method: callEventsOn, Args: []byte(`{"channel":"c","handlerId":"` + id + `"}`)}, nil)
		if env := readFramed(t, peer); env.CallResult == nil || env.CallResult.Error != nil {
			t.Fatalf("events.on %s = %+v", id, env)
		}
	}
	h.runCall(me, conn, "off", &CallPayload{Method: callEventsOff, Args: []byte(`{"handlerId":"dropped"}`)}, nil)
	readFramed(t, peer)
	if h.eventBus.lookup(busHandlerKey{"realm-a", "dropped"}) != nil || h.eventBus.lookup(busHandlerKey{"realm-a", "kept"}) == nil {
		t.Fatal("events.off must remove only its own listener")
	}
	h.Runtime().Invalidate("reload")
	if h.eventBus.lookup(busHandlerKey{"realm-a", "kept"}) != nil {
		t.Fatal("runtime invalidate left the retained event-bus listener registered")
	}
}

// A closed connection's listeners leave the runtime's retained unsubscribes too (loader.ts:201-210 retains a subscription only until it is unsubscribed), so a host that outlives many reload generations does not keep each generation's listeners and connections.
func TestHostEventBusClosedConnectionForgetsRetainedSubscriptions(t *testing.T) {
	h := NewHostWithConfigRoot(t.TempDir(), t.TempDir())
	conn, peer := startedPipeConn(t, "listener")
	me := withConn(&managedExt{config: ExtConfig{Name: "listener"}, host: h, nodeRealm: true}, conn)
	h.runCall(me, conn, "hello", &CallPayload{Method: callXrefHello, Args: []byte(`{"realm":"realm-a"}`)}, nil)
	readFramed(t, peer)
	h.runCall(me, conn, "on", &CallPayload{Method: callEventsOn, Args: []byte(`{"channel":"c","handlerId":"h1"}`)}, nil)
	if env := readFramed(t, peer); env.CallResult == nil || env.CallResult.Error != nil {
		t.Fatalf("events.on = %+v", env)
	}
	retained := func() int { return reflect.ValueOf(h.Runtime()).Elem().FieldByName("eventBusUnsubscribers").Len() }
	if retained() != 1 {
		t.Fatalf("retained after events.on = %d, want 1", retained())
	}
	h.eventBus.closed(conn)
	if retained() != 0 {
		t.Fatalf("retained after the connection closed = %d, want 0", retained())
	}
}

// A retained unsubscribe removes only its own listener: once its connection closed, a later listener under the same realm and handler id stays registered.
func TestHostEventBusStaleUnsubscribeKeepsTheListenerThatReusedItsKey(t *testing.T) {
	h := NewHostWithConfigRoot(t.TempDir(), t.TempDir())
	conn, _ := startedPipeConn(t, "listener")
	stale := &busListener{conn: conn, realm: "realm-a", handlerID: "h1", channel: "c"}
	h.eventBus.add(stale)
	h.eventBus.closed(conn)
	current := &busListener{realm: "realm-a", handlerID: "h1", channel: "c"}
	h.eventBus.add(current)
	h.removeBusListener(context.Background(), stale)
	if h.eventBus.lookup(busHandlerKey{"realm-a", "h1"}) != current {
		t.Fatal("the stale listener's unsubscribe removed the listener that reused its key")
	}
}
