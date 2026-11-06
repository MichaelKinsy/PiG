package subprocess

import (
	"encoding/json"
	"net"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/typebox/engine"
)

// fakeEngineSession records what the host does with the session of a connection.
type fakeEngineSession struct {
	served int
	closed atomic.Bool
}

func (s *fakeEngineSession) Serve(got []byte) []byte {
	s.served++
	return append([]byte(`{"value":`), append(got, '}')...)
}

func (s *fakeEngineSession) Close() { s.closed.Store(true) }

// The host answers CallTypeEngine with the registered engine's reply, and with an error naming the missing engine where none is registered (a program that links the
// host without coding/extension/pigsdk).
func TestTypeEngineCallUsesTheRegisteredEngine(t *testing.T) {
	previous := typeEngine.Load()
	t.Cleanup(func() { typeEngine.Store(previous) })
	h := &Host{}
	request := json.RawMessage(`{"member":"Pick","args":[]}`)

	typeEngine.Store(nil)
	if _, err := h.handleTypeEngine(nil, &CallPayload{Method: CallTypeEngine, Args: request}); err == nil || !strings.Contains(err.Error(), "type engine") {
		t.Fatalf("handleTypeEngine without an engine = %v, want an error naming the type engine", err)
	}

	SetTypeEngine(func() TypeEngineSession { return &fakeEngineSession{} })
	result, err := h.handleTypeEngine(nil, &CallPayload{Method: CallTypeEngine, Args: request})
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"value":` + string(request) + `}`; string(result.Result) != want {
		t.Fatalf("handleTypeEngine = %s, want %s", result.Result, want)
	}
}

// TestTypeEngineSessionBelongsToItsConnection: each connection has a session of its own, made with its first call and closed when the connection closes, or the host shuts down. What
// the engine parks for a connection (a Value walk waiting for a refinement's answer) so never outlives the extension that started it.
func TestTypeEngineSessionBelongsToItsConnection(t *testing.T) {
	previous := typeEngine.Load()
	t.Cleanup(func() { typeEngine.Store(previous) })
	var sessions []*fakeEngineSession
	SetTypeEngine(func() TypeEngineSession {
		session := &fakeEngineSession{}
		sessions = append(sessions, session)
		return session
	})
	h := &Host{}
	call := &CallPayload{Method: CallTypeEngine, Args: json.RawMessage(`{}`)}
	newConn := func() (*Conn, net.Conn) {
		a, b := net.Pipe()
		conn := NewConn("ext", a)
		conn.Start(t.Context())
		return conn, b
	}
	first, firstPeer := newConn()
	second, secondPeer := newConn()
	t.Cleanup(func() { _ = secondPeer.Close(); _ = second.Close("test done") })
	for range 3 {
		if _, err := h.handleTypeEngine(first, call); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := h.handleTypeEngine(second, call); err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 2 || sessions[0].served != 3 || sessions[1].served != 1 {
		t.Fatalf("%d sessions serving %d and %d calls, want 2 serving 3 and 1", len(sessions), sessions[0].served, sessions[1].served)
	}
	_ = firstPeer.Close()
	_ = first.Close("the extension went away")
	deadline := time.Now().Add(5 * time.Second)
	for !sessions[0].closed.Load() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if !sessions[0].closed.Load() {
		t.Fatal("the session of a closed connection stayed open")
	}
	if sessions[1].closed.Load() {
		t.Fatal("closing one connection closed another's session")
	}
	// A call on a connection that has closed gets a closed session, not a parked one.
	if _, err := h.handleTypeEngine(first, call); err != nil || !sessions[2].closed.Load() {
		t.Fatalf("a call on a closed connection: %v, session closed %v", err, sessions[2].closed.Load())
	}
	h.typeEngines.closeAll()
	if !sessions[1].closed.Load() {
		t.Fatal("Host shutdown left a session open")
	}
}

// TestAbandonedValueWalksEndWithTheirConnection: an extension that disconnects (a crash, a reload) with Value walks parked at a refinement leaves no goroutine behind, and the walks of
// the connections that live on stay answerable. It runs the production engine, not a fake.
func TestAbandonedValueWalksEndWithTheirConnection(t *testing.T) {
	previous := typeEngine.Load()
	t.Cleanup(func() { typeEngine.Store(previous) })
	SetTypeEngine(func() TypeEngineSession { return engine.NewSession() })
	h := &Host{}
	t.Cleanup(h.typeEngines.closeAll)
	refinement := `{"\u0000":"o","h":{"~kind":"Number","~refine":[{"\u0000":"f","i":0}]},"e":{"type":"number"}}`
	start := json.RawMessage(`{"member":"Value.Check","args":[` + refinement + `,1]}`)
	parked := func(conn *Conn) string {
		result, err := h.handleTypeEngine(conn, &CallPayload{Method: CallTypeEngine, Args: start})
		if err != nil {
			t.Fatal(err)
		}
		var reply struct {
			Call struct{ Token string } `json:"call"`
		}
		if err := json.Unmarshal(result.Result, &reply); err != nil || reply.Call.Token == "" {
			t.Fatalf("reply %s (%v), want a parked walk", result.Result, err)
		}
		return reply.Call.Token
	}
	a, aPeer := net.Pipe()
	gone := NewConn("gone", a)
	gone.Start(t.Context())
	b, bPeer := net.Pipe()
	stays := NewConn("stays", b)
	stays.Start(t.Context())
	t.Cleanup(func() { _ = bPeer.Close(); _ = stays.Close("test done") })
	// Both connections have their sessions before the count, so it holds only the walks.
	for _, conn := range []*Conn{gone, stays} {
		if _, err := h.handleTypeEngine(conn, &CallPayload{Method: CallTypeEngine, Args: json.RawMessage(`{"member":"Value.Cancel","args":["none"]}`)}); err != nil {
			t.Fatal(err)
		}
	}
	before := runtime.NumGoroutine()
	for range 200 {
		parked(gone)
	}
	token := parked(stays)
	_ = aPeer.Close()
	_ = gone.Close("the extension went away")
	deadline := time.Now().Add(10 * time.Second)
	for runtime.NumGoroutine() > before+1 && time.Now().Before(deadline) { // the live connection's one parked walk
		time.Sleep(10 * time.Millisecond)
	}
	if got := runtime.NumGoroutine(); got > before+1 {
		t.Fatalf("%d goroutines after the connection closed, %d before the walks: the abandoned walks are still parked", got, before)
	}
	answer := json.RawMessage(`{"member":"Value.Answer","args":["` + token + `",true]}`)
	result, err := h.handleTypeEngine(stays, &CallPayload{Method: CallTypeEngine, Args: answer})
	if err != nil || string(result.Result) != `{"value":true}` {
		t.Fatalf("the surviving connection's walk: %s (%v)", result.Result, err)
	}
}
