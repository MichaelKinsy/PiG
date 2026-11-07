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

// A component whose HandleInput panics: the host waits for ui.editor.inputDone after every key and has no deadline, so the SDK sends it however the call
// ended, and the keys after it still reach the component.
type panickingInput struct {
	EditorComponent
	calls int
}

func (c *panickingInput) HandleInput(data string) error {
	c.calls++
	if data == "boom" {
		panic("component boom")
	}
	return nil
}
func (*panickingInput) Render(int) ([]string, error) { return []string{"row"}, nil }

// readFrames hands the method of every notify frame the extension writes to a channel.
func readFrames(t *testing.T, peer net.Conn) <-chan string {
	t.Helper()
	out := make(chan string, 32)
	go func() {
		defer close(out)
		for {
			header := make([]byte, 4)
			if _, err := io.ReadFull(peer, header); err != nil {
				return
			}
			body := make([]byte, binary.BigEndian.Uint32(header))
			if _, err := io.ReadFull(peer, body); err != nil {
				return
			}
			var env struct {
				Notify struct {
					Method string          `json:"method"`
					Args   json.RawMessage `json:"args"`
				} `json:"notify"`
			}
			if json.Unmarshal(body, &env) == nil && env.Notify.Method != "" {
				out <- env.Notify.Method + " " + string(env.Notify.Args)
			}
		}
	}()
	return out
}

func TestEditorHandleInputPanicStillSendsInputDone(t *testing.T) {
	host, peer := net.Pipe()
	defer func() { _ = host.Close(); _ = peer.Close() }()
	frames := readFrames(t, peer)
	component := &panickingInput{}
	ext := &Extension{conn: &conn{nc: host}}
	s := &editorSession{ext: ext, key: "editor-1", component: component, wake: make(chan struct{}, 1), done: make(chan struct{})}
	ext.editor = s
	go s.run()
	defer s.close()
	input := func(data string) {
		args, _ := json.Marshal(map[string]any{"key": "editor-1", "data": data})
		if !ext.handleEditorNotify("ui.editor.input", args) {
			t.Fatal("the editor notify was not routed")
		}
	}
	awaitInputDone := func() (all []string) {
		for {
			select {
			case frame := <-frames:
				all = append(all, frame)
				if strings.HasPrefix(frame, "ui.editor.inputDone ") {
					return all
				}
			case <-time.After(10 * time.Second):
				t.Fatalf("no ui.editor.inputDone after the key; frames %q", all)
			}
		}
	}
	input("boom")
	got := awaitInputDone()
	failed := false
	for _, frame := range got {
		if strings.HasPrefix(frame, "ui.notify ") && strings.Contains(frame, "editor handleInput failed: component boom") {
			failed = true
		}
	}
	if !failed {
		t.Fatalf("frames %q hold no \"editor handleInput failed: component boom\" notice", got)
	}
	input("x")
	awaitInputDone()
	if component.calls != 2 {
		t.Fatalf("HandleInput ran %d times, want 2: a later key did not reach the component", component.calls)
	}
}
