package subprocess

import (
	"encoding/json"
	"net"
	"testing"
)

// A reference an extension transmits in a call is held until the Host has delivered it. A call the Host drops before it runs (its parent request already ended) delivers nothing, so its hold must end with it; otherwise the lease and the owner's exported object are never released.
func TestXrefDroppedCallReleasesItsHold(t *testing.T) {
	hostSide, peer := net.Pipe()
	defer func() { _ = hostSide.Close() }()
	defer func() { _ = peer.Close() }()
	h := NewHostWithConfigRoot(t.TempDir(), t.TempDir())
	conn := NewConn("emitter", hostSide)
	me := withConn(&managedExt{config: ExtConfig{Name: "emitter"}, host: h}, conn)
	if err := h.xref.bind(conn, me, "n-emitter"); err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{callEventsEmit, callXrefOp} {
		args := json.RawMessage(`{"channel":"ch","data":{"$x":"ref","realm":"n-other","id":"7","kind":"object"}}`)
		if method == callXrefOp {
			args = json.RawMessage(`{"ref":{"realm":"n-other","id":"1"},"op":"set","args":["k",{"$x":"ref","realm":"n-other","id":"7","kind":"object"}]}`)
		}
		call := &CallPayload{Method: method, Args: args, ParentRequestID: "finished-request"}
		env := &Envelope{Type: MsgCall, ID: "c-" + method, Call: call}
		h.xref.inbound(conn, env)
		if stats := h.xref.stats(); stats.Holds != 1 || stats.Leases != 1 {
			t.Fatalf("%s: inbound hold = %+v", method, stats)
		}
		h.runCall(me, me.connection(), env.ID, call, nil)
		if stats := h.xref.stats(); stats.Holds != 0 || stats.Leases != 0 {
			t.Fatalf("%s: dropped call kept %+v", method, stats)
		}
	}
}

// A connection that closes while a call it sent still holds a reference ends that hold with the connection: the call may never reach a handler, and nothing else would release the lease that keeps the owner's export alive.
func TestXrefClosedConnectionReleasesItsHolds(t *testing.T) {
	h := NewHostWithConfigRoot(t.TempDir(), t.TempDir())
	newConn := func(name string) (*Conn, *managedExt) {
		hostSide, peer := net.Pipe()
		t.Cleanup(func() { _ = hostSide.Close(); _ = peer.Close() })
		conn := NewConn(name, hostSide)
		return conn, withConn(&managedExt{config: ExtConfig{Name: name}, host: h}, conn)
	}
	ownerConn, ownerExt := newConn("owner")
	importerConn, importerExt := newConn("importer")
	if err := h.xref.bind(ownerConn, ownerExt, "n-owner"); err != nil {
		t.Fatal(err)
	}
	if err := h.xref.bind(importerConn, importerExt, "n-importer"); err != nil {
		t.Fatal(err)
	}
	args := json.RawMessage(`{"ref":{"realm":"n-owner","id":"1"},"op":"get","args":[{"$x":"ref","realm":"n-owner","id":"7","kind":"object"}]}`)
	h.xref.inbound(importerConn, &Envelope{Type: MsgCall, ID: "held", Call: &CallPayload{Method: callXrefOp, Args: args}})
	if stats := h.xref.stats(); stats.Holds != 1 || stats.Leases != 1 {
		t.Fatalf("inbound hold = %+v", stats)
	}
	h.xref.closed(importerConn)
	if stats := h.xref.stats(); stats.Holds != 0 || stats.Leases != 0 {
		t.Fatalf("closed connection kept %+v", stats)
	}
}

// A retained process serves successive generations through different managed extensions and process states, and its one realm keeps its identity across them. Every generation's connection binds to that realm; a different process claiming it is still rejected.
func TestXrefRealmSurvivesRetainedGenerations(t *testing.T) {
	h := NewHostWithConfigRoot(t.TempDir(), t.TempDir())
	bind := func(name string, me *managedExt, realm string) error {
		hostSide, peer := net.Pipe()
		t.Cleanup(func() { _ = hostSide.Close(); _ = peer.Close() })
		conn := NewConn(name, hostSide)
		return h.xref.bind(conn, withConn(me, conn), realm)
	}
	origin := &managedExt{config: ExtConfig{Name: "isolated"}, host: h}
	if err := bind("first", origin, "n-retained"); err != nil {
		t.Fatal(err)
	}
	next := &managedExt{config: ExtConfig{Name: "isolated"}, host: h, procOwner: origin}
	if err := bind("next", next, "n-retained"); err != nil {
		t.Fatalf("a retained generation was refused its process's realm: %v", err)
	}
	spawner := &packedProcessState{}
	if err := bind("packed", &managedExt{config: ExtConfig{Name: "a"}, host: h, packedProcess: spawner}, "n-packed"); err != nil {
		t.Fatal(err)
	}
	adopted := &packedProcessState{}
	adopted.adopt(spawner)
	if err := bind("adopted", &managedExt{config: ExtConfig{Name: "a"}, host: h, packedProcess: adopted}, "n-packed"); err != nil {
		t.Fatalf("an adopting state was refused its process's realm: %v", err)
	}
	if err := bind("other", &managedExt{config: ExtConfig{Name: "other"}, host: h}, "n-packed"); err == nil {
		t.Fatal("a different process claimed an existing realm")
	}
}
