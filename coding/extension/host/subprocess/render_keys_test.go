package subprocess

import (
	"encoding/json"
	"io"
	"net"
	"runtime"
	"testing"
	"time"
	"weak"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// A message outlives the extension that draws it: a reload replaces the extension, and the transcript renders the same identity
// through the new connection. The key the replaced connection had for the message must not keep that connection: a session would
// hold every generation that ever drew a message for as long as the transcript shows it. Nothing can use such a key again: a
// notification through the ended connection is refused by the connection with its error and reaches no process, and a renderer call
// through it gets no key. When the transcript drops the message, the extension that draws it now is told to drop the state under its key.
func TestRenderKeysDoNotKeepAnEndedConnection(t *testing.T) {
	identity := extension.NewRenderIdentity()

	hostEnd, replacedPeer := net.Pipe()
	t.Cleanup(func() { _ = replacedPeer.Close() })
	// The replaced extension's process reads until the host closes.
	go func() { _, _ = io.Copy(io.Discard, replacedPeer) }()
	replaced := NewConn("ext", hostEnd)
	replaced.Start(t.Context())
	oldKey := renderKeys.key(replaced, identity)
	if oldKey == "" || renderKeys.key(replaced, identity) != oldKey {
		t.Fatalf("keys of one identity on one connection = %q, then %q; want one key", oldKey, renderKeys.key(replaced, identity))
	}
	if err := replaced.Close("reload"); err != nil {
		t.Fatalf("close of the replaced connection: %v", err)
	}
	<-replaced.Done()
	if err := replaced.Send(&Envelope{Type: MsgNotify, Notify: &NotifyPayload{Method: NotifyRenderRelease}}); err == nil || err.Error() != "connection closed" {
		t.Fatalf("a notification through the ended connection returned %v, want the connection's error %q", err, "connection closed")
	}

	hostEnd, peer := net.Pipe()
	t.Cleanup(func() { _ = peer.Close() })
	current := NewConn("ext", hostEnd)
	current.Start(t.Context())
	t.Cleanup(func() {
		go func() { _, _ = io.Copy(io.Discard, peer) }()
		_ = current.Close("test done")
	})
	newKey := renderKeys.key(current, identity)
	if newKey == "" || newKey == oldKey {
		t.Fatalf("key through the new connection = %q, want a new one (the old was %q): the message is new to the new process", newKey, oldKey)
	}

	// The identity is alive: the transcript shows the message. The ended connection is not held by its key.
	held := weak.Make(replaced)
	replaced = nil
	for deadline := time.Now().Add(testbudget.Wait(t)); held.Value() != nil; time.Sleep(time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("the ended connection is still held while its message is shown (wait budget %s)", testbudget.Env)
		}
		runtime.GC()
	}
	runtime.KeepAlive(identity)

	// The transcript drops the message: nothing refers to its identity after the KeepAlive above.
	runtime.GC()
	for {
		frame := readLivenessEnvelope(t, peer)
		if frame.Notify == nil || frame.Notify.Method != NotifyRenderRelease {
			continue
		}
		var release RenderReleasePayload
		if err := json.Unmarshal(frame.Notify.Args, &release); err != nil || release.Key != newKey {
			t.Fatalf("release = %s (%v), want the key %q of the connection that draws the message", frame.Notify.Args, err, newKey)
		}
		return
	}
}

// TestRenderKeyOfAnEndedConnectionIsEmpty: a renderer call through a connection that has ended is for no process, so it gets no key, and
// none is held for it.
func TestRenderKeyOfAnEndedConnectionIsEmpty(t *testing.T) {
	hostEnd, peer := net.Pipe()
	t.Cleanup(func() { _ = peer.Close() })
	go func() { _, _ = io.Copy(io.Discard, peer) }()
	conn := NewConn("ext", hostEnd)
	conn.Start(t.Context())
	_ = conn.Close("reload")
	<-conn.Done()
	identity := extension.NewRenderIdentity()
	if key := renderKeys.key(conn, identity); key != "" {
		t.Fatalf("key on an ended connection = %q, want none", key)
	}
	runtime.KeepAlive(identity)
}
