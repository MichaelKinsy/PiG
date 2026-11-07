package sdk

import (
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

// A panic in a callback of the extension's own code that the SDK runs on a goroutine it started ends the process, and in a packed cell the process serves other
// extensions. Each such callback turns the panic into the error of the call it serves.

func TestCallbackErrorTurnsAPanicIntoTheCallsError(t *testing.T) {
	err := callbackError("a store", func() error { panic("boom") })
	if err == nil || err.Error() != "a store panicked: boom" {
		t.Fatalf("error = %v, want %q", err, "a store panicked: boom")
	}
	if got := callbackError("a store", func() error { return nil }); got != nil {
		t.Fatalf("a callback that returned nil failed: %v", got)
	}
}

type panickingSettingsStorage struct{ afterFn bool }

func (s panickingSettingsStorage) WithLock(_ string, fn func(*string) (*string, error)) error {
	if s.afterFn {
		if _, err := fn(nil); err != nil {
			return err
		}
	}
	panic("storage boom")
}

func TestSettingsStorageLockPanicIsTheCallsError(t *testing.T) {
	begin := []json.RawMessage{json.RawMessage(`"begin"`), json.RawMessage(`"global"`)}
	// The storage panics before it calls fn: the begin call fails.
	if _, err := storageCallback(panickingSettingsStorage{})(begin); err == nil || !strings.Contains(err.Error(), "panicked: storage boom") {
		t.Fatalf("begin error = %v, want the storage's panic", err)
	}
	// The storage panics after fn: begin answers, and the finish call carries the panic.
	callback := storageCallback(panickingSettingsStorage{afterFn: true})
	if _, err := callback(begin); err != nil {
		t.Fatalf("begin: %v", err)
	}
	finish := []json.RawMessage{json.RawMessage(`"finish"`), json.RawMessage(`{"write":false}`)}
	if _, err := callback(finish); err == nil || !strings.Contains(err.Error(), "panicked: storage boom") {
		t.Fatalf("finish error = %v, want the storage's panic", err)
	}
}

type panickingMcpStore struct{ McpOAuthServerStore }

func (panickingMcpStore) WithRefreshLock(func() error) error { panic("lock boom") }

func TestMcpRefreshLockPanicIsTheCallsError(t *testing.T) {
	shim := &mcpCredentialsShim{}
	if _, err := shim.lock("store", panickingMcpStore{}); err == nil || !strings.Contains(err.Error(), "panicked: lock boom") {
		t.Fatalf("lock error = %v, want the store's panic", err)
	}
}

func TestEditorComponentPanicIsReportedAndTheSessionGoesOn(t *testing.T) {
	host, peer := net.Pipe()
	defer func() { _ = host.Close(); _ = peer.Close() }()
	s := &editorSession{ext: &Extension{conn: &conn{nc: host}}, key: "editor-1", wake: make(chan struct{}, 1), done: make(chan struct{})}
	go s.run()
	defer close(s.done)
	s.post(func() { panic("component boom") })
	alive := make(chan struct{})
	s.post(func() { close(alive) })

	_ = peer.SetReadDeadline(time.Now().Add(10 * time.Second))
	header := make([]byte, 4)
	if _, err := io.ReadFull(peer, header); err != nil {
		t.Fatal(err)
	}
	body := make([]byte, binary.BigEndian.Uint32(header))
	if _, err := io.ReadFull(peer, body); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "editor call failed: panicked: component boom") {
		t.Fatalf("notification = %s, want the component's panic as an error notice", body)
	}
	select {
	case <-alive:
	case <-time.After(10 * time.Second):
		t.Fatal("the session stopped running calls after one panicked")
	}
}
