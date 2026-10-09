package subprocess

import (
	"context"
	"encoding/json"
	"net"
	"runtime"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

func userBashOperationsRig(t *testing.T) (*Conn, net.Conn) {
	t.Helper()
	host, peer := net.Pipe()
	conn := NewConn("user-bash-operations", host)
	conn.Start(t.Context())
	t.Cleanup(func() {
		_ = conn.Close("test done")
		_ = peer.Close()
	})
	return conn, peer
}

// A user_bash reply that names an operations handle becomes the object the Runner accepts, and the extension's table entry is released
// when the host drops that object (BashOperationsRelease), so a command's object does not outlive its consumer.
func TestUserBashOperationsReleaseTheExtensionHandleWhenTheHostDropsThem(t *testing.T) {
	conn, peer := userBashOperationsRig(t)
	typed, ok := userBashOperationsResult(conn, json.RawMessage(`{"operations":{"handle":"bash-7"}}`))
	if !ok || typed == nil || typed.Operations == nil || typed.Result != nil {
		t.Fatalf("a reply naming a handle = %+v, %v, want an operations result", typed, ok)
	}
	released := make(chan string, 1)
	go func() {
		for {
			env, err := tryReadFramed(peer)
			if err != nil {
				return
			}
			if env.Type == MsgNotify && env.Notify != nil && env.Notify.Method == NotifyBashOperationsRelease {
				var release BashOperationsRelease
				_ = json.Unmarshal(env.Notify.Args, &release)
				released <- release.Handle
				return
			}
		}
	}()
	select {
	case handle := <-released:
		t.Fatalf("handle %q released while the host still holds the object", handle)
	case <-time.After(50 * time.Millisecond):
	}
	typed = nil
	deadline := time.After(10 * time.Second)
	for {
		runtime.GC()
		select {
		case handle := <-released:
			if handle != "bash-7" {
				t.Fatalf("released handle %q, want bash-7", handle)
			}
			return
		case <-deadline:
			t.Fatal("dropping the operations never released the extension's handle")
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// Pi's isUserBashEventResult accepts exactly one of operations and result. A reply with both is invalid, so the host releases the handle at
// once and leaves the raw reply for the Runner to reject; a reply without a handle names no object.
func TestUserBashOperationsReplyWithResultOrWithoutHandleIsLeftRaw(t *testing.T) {
	conn, peer := userBashOperationsRig(t)
	for _, reply := range []string{`{"operations":{}}`, `{"operations":null}`, `{"result":{"output":"x"}}`, `null`} {
		if typed, ok := userBashOperationsResult(conn, json.RawMessage(reply)); ok || typed != nil {
			t.Fatalf("reply %s = %+v, %v, want it left raw", reply, typed, ok)
		}
	}
	if typed, ok := userBashOperationsResult(conn, json.RawMessage(`{"operations":{"handle":"bash-1"},"result":{"output":"x"}}`)); ok || typed != nil {
		t.Fatalf("a reply with operations and result = %+v, %v, want it left raw", typed, ok)
	}
	env, err := tryReadFramed(peer)
	if err != nil || env.Notify == nil || env.Notify.Method != NotifyBashOperationsRelease {
		t.Fatalf("first frame after the invalid reply = %+v, %v, want the release of its handle", env, err)
	}
	var release BashOperationsRelease
	if err := json.Unmarshal(env.Notify.Args, &release); err != nil || release.Handle != "bash-1" {
		t.Fatalf("released %s (%v), want bash-1", env.Notify.Args, err)
	}
}

// BashOperations.Exec tells the extension to cancel the exec (its signal) when its context is cancelled, keeps delivering the exec's
// output and returns the extension's own settlement: Pi's executeBashWithOperations awaits operations.exec after its signal aborts
// (bash-executor.ts:120-128).
func TestUserBashOperationsExecAbortsWithTheContext(t *testing.T) {
	conn, peer := userBashOperationsRig(t)
	typed, ok := userBashOperationsResult(conn, json.RawMessage(`{"operations":{"handle":"bash-3"}}`))
	if !ok {
		t.Fatal("no operations result")
	}
	ctx, cancel := context.WithCancel(t.Context())
	failed := make(chan error, 1)
	var chunks []string
	go func() {
		_, err := typed.Operations.Exec(ctx, "sleep", "/work", extension.BashOperationsExecOptions{OnData: func(data []byte) { chunks = append(chunks, string(data)) }})
		failed <- err
	}()
	request, err := tryReadFramed(peer)
	if err != nil || request.Request == nil || request.Request.Method != RequestUserBashExec || request.Request.Tool != "bash-3" {
		t.Fatalf("request = %+v, %v, want user_bash_exec for bash-3", request, err)
	}
	var args UserBashExecArgs
	if err := json.Unmarshal(request.Request.Args, &args); err != nil || args.Command != "sleep" || args.Cwd != "/work" || args.Timeout != nil || args.Env != nil {
		t.Fatalf("args = %+v, %v", args, err)
	}
	cancel()
	cancelFrame, err := tryReadFramed(peer)
	if err != nil || cancelFrame.Type != MsgCancel || cancelFrame.Cancel == nil || cancelFrame.Cancel.RequestID != request.ID {
		t.Fatalf("frame after the cancellation = %+v, %v, want a cancel of the exec request", cancelFrame, err)
	}
	select {
	case err := <-failed:
		t.Fatalf("Exec returned %v before the extension's exec settled", err)
	case <-time.After(50 * time.Millisecond):
	}
	update, err := json.Marshal(ToolUpdatePayload{RequestID: request.ID, Result: json.RawMessage(`{"data":"c3RvcHBlZA=="}`)})
	if err != nil {
		t.Fatal(err)
	}
	writeFramed(t, peer, Envelope{Type: MsgNotify, Notify: &NotifyPayload{Method: NotifyToolUpdate, Args: update}})
	writeFramed(t, peer, Envelope{Type: MsgResponse, ID: request.ID, Response: &ResponsePayload{Error: &ErrorInfo{Message: "remote shell stopped"}}})
	if err := <-failed; err == nil || err.Error() != "remote shell stopped" {
		t.Fatalf("Exec error = %v, want the extension's rejection", err)
	}
	if len(chunks) != 1 || chunks[0] != "stopped" {
		t.Fatalf("chunks after the abort = %q, want the exec's output until it settled", chunks)
	}
}
