package subprocess

import (
	"errors"
	"net"
	"testing"
)

func TestOldNodeGenerationCannotPublishFramesOrStartHostCalls(t *testing.T) {
	a, b := net.Pipe()
	defer func() { _ = a.Close(); _ = b.Close() }()
	h := NewHost(t.TempDir())
	defer h.Shutdown("test complete")
	h.uiBridge = NewUIBridge(nil)
	conn := NewConn("peer", a)
	old := withConn(&managedExt{config: ExtConfig{Name: "peer"}, packedProcess: &packedProcessState{key: "node", generation: 1, node: true}}, conn)
	h.packedCellGeneration = map[string]int{"node": 2}
	calls := 0
	h.onCall = func(string, *CallPayload) (*CallResultPayload, error) { calls++; return &CallResultPayload{}, nil }
	h.runCall(old, old.connection(), "", &CallPayload{Method: "probe"}, nil)
	if calls != 0 {
		t.Error("stale generation started a host callback")
	}
	old.shuttingDown.Store(true)
	conn.inCh <- &Envelope{Type: MsgWidgetPush, WidgetPush: &WidgetPushPayload{Key: "stale", Lines: []string{"old generation"}}}
	close(conn.inCh)
	h.handleIncoming(old, old.connection())
	if len(h.uiBridge.AllWidgets()) != 0 {
		t.Error("stale generation published a widget frame")
	}
}

// Timer-owned host calls have no parent request, but still belong to the connection generation. Recovery must cancel them and must not admit queued work after disconnect.
func TestUnparentedHostCallEndsWithConnectionGeneration(t *testing.T) {
	a, b := net.Pipe()
	defer func() { _ = b.Close() }()
	conn := NewConn("generation", a)
	first, releaseFirst := conn.hostCallContext("", "first")
	defer releaseFirst()
	second, releaseSecond := conn.hostCallContext("", "second")
	defer releaseSecond()
	conn.fail(errors.New("process exited"))
	if first.Err() == nil || second.Err() == nil {
		t.Fatal("old-generation timer calls survived disconnect")
	}
	late, releaseLate := conn.hostCallContext("", "late")
	defer releaseLate()
	if late.Err() == nil {
		t.Fatal("queued old-generation host call admitted after disconnect")
	}
}
