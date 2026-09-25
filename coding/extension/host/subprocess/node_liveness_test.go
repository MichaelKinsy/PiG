package subprocess

import (
	"bytes"
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestNodeRuntimeHeartbeatAndRequestStatesBypassHandlers(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skipf("node unavailable: %v", err)
	}
	parent := "/tmp"
	if runtime.GOOS == "windows" {
		parent = os.TempDir()
	}
	root, err := os.MkdirTemp(parent, "pig-node-liveness-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	entry := filepath.Join(root, "liveness.mjs")
	if err := os.WriteFile(entry, []byte(`export default function (pi) {
  pi.registerCommand("done", { description: "complete immediately", handler: async () => {} });
  pi.registerCommand("ask", { description: "wait for input", handler: async (_args, ctx) => { await ctx.ui.input("Question", "Answer"); } });
  pi.registerCommand("title", { description: "set title", handler: async (_args, ctx) => { ctx.ui.setTitle("Node title"); } });
  pi.registerCommand("wait", { description: "wait for cancellation", handler: async (_args, ctx) => {
    await new Promise((resolve) => ctx.signal.addEventListener("abort", resolve, { once: true }));
  } });
}`), 0o600); err != nil {
		t.Fatal(err)
	}
	listener, address, err := ListenExtension(filepath.Join(root, "runtime.sock"), true)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	runtimePath := filepath.Join("runtime-node", "cli.mjs")
	cmd := exec.CommandContext(t.Context(), node, runtimePath, entry)
	cmd.Env = append(os.Environ(), "PIG_EXT_SOCKET="+address, "PIG_EXT_NAME=node-liveness")
	peer := startAndAccept(t, cmd, listener)
	defer func() { _ = peer.Close() }()
	if register := readLivenessEnvelope(t, peer); register.Type != MsgRegister {
		t.Fatalf("register = %+v", register)
	}
	writeLivenessEnvelope(t, peer, Envelope{Type: MsgReady, Ready: &ReadyPayload{Cwd: root, Width: 80}})
	writeLivenessEnvelope(t, peer, Envelope{Type: MsgPing, Ping: &PingPayload{Nonce: "heartbeat-1"}})
	if pong := readLivenessEnvelope(t, peer); pong.Type != MsgPong || pong.Pong == nil || pong.Pong.Nonce != "heartbeat-1" {
		t.Fatalf("pong = %+v", pong)
	}
	writeLivenessEnvelope(t, peer, Envelope{Type: MsgRequest, ID: "req-state", Request: &RequestPayload{Method: "command", Tool: "done"}})
	started := readLivenessEnvelope(t, peer)
	completed := readLivenessEnvelope(t, peer)
	response := readLivenessEnvelope(t, peer)
	if started.RequestState == nil || started.RequestState.State != "started" || started.RequestState.RequestID != "req-state" {
		t.Fatalf("started state = %+v", started)
	}
	if completed.RequestState == nil || completed.RequestState.State != "completed" || completed.RequestState.RequestID != "req-state" {
		t.Fatalf("completed state = %+v", completed)
	}
	if response.Type != MsgResponse || response.ID != "req-state" {
		t.Fatalf("response = %+v", response)
	}

	writeLivenessEnvelope(t, peer, Envelope{Type: MsgRequest, ID: "req-user", Request: &RequestPayload{Method: "command", Tool: "ask"}})
	if state := readLivenessEnvelope(t, peer); state.RequestState == nil || state.RequestState.State != "started" {
		t.Fatalf("started state = %+v", state)
	}
	blocked := readLivenessEnvelope(t, peer)
	if blocked.RequestState == nil || blocked.RequestState.State != "blocked" || blocked.RequestState.Reason != "user" {
		t.Fatalf("blocked state = %+v", blocked)
	}
	call := readLivenessEnvelope(t, peer)
	if call.Call == nil || call.Call.Method != "ui.input" || call.Call.ParentRequestID != "req-user" {
		t.Fatalf("parented host call = %+v", call)
	}
	writeLivenessEnvelope(t, peer, Envelope{Type: MsgPing, Ping: &PingPayload{Nonce: "while-blocked"}})
	if pong := readLivenessEnvelope(t, peer); pong.Type != MsgPong || pong.Pong == nil || pong.Pong.Nonce != "while-blocked" {
		t.Fatalf("blocked-handler pong = %+v", pong)
	}
	writeLivenessEnvelope(t, peer, Envelope{Type: MsgCallResult, ID: call.ID, CallResult: &CallResultPayload{Result: json.RawMessage(`{"text":"ok","ok":true}`)}})
	if state := readLivenessEnvelope(t, peer); state.RequestState == nil || state.RequestState.State != "progress" {
		t.Fatalf("progress state = %+v", state)
	}
	if state := readLivenessEnvelope(t, peer); state.RequestState == nil || state.RequestState.State != "completed" {
		t.Fatalf("completed state = %+v", state)
	}
	if response := readLivenessEnvelope(t, peer); response.Type != MsgResponse || response.ID != "req-user" {
		t.Fatalf("response = %+v", response)
	}

	writeLivenessEnvelope(t, peer, Envelope{Type: MsgRequest, ID: "req-title", Request: &RequestPayload{Method: "command", Tool: "title"}})
	if state := readLivenessEnvelope(t, peer); state.RequestState == nil || state.RequestState.State != "started" {
		t.Fatalf("title start = %+v", state)
	}
	blocked = readLivenessEnvelope(t, peer)
	if blocked.RequestState == nil || blocked.RequestState.State != "blocked" || blocked.RequestState.Reason != "host_call" {
		t.Fatalf("title blocked state = %+v", blocked)
	}
	call = readLivenessEnvelope(t, peer)
	if call.Call == nil || call.Call.Method != "ui.setTitle" || call.Call.ParentRequestID != "req-title" {
		t.Fatalf("title parented host call = %+v", call)
	}
	writeLivenessEnvelope(t, peer, Envelope{Type: MsgCallResult, ID: call.ID, CallResult: &CallResultPayload{}})
	if state := readLivenessEnvelope(t, peer); state.RequestState == nil || state.RequestState.State != "progress" {
		t.Fatalf("title progress state = %+v", state)
	}
	if state := readLivenessEnvelope(t, peer); state.RequestState == nil || state.RequestState.State != "completed" {
		t.Fatalf("title completed state = %+v", state)
	}
	if response := readLivenessEnvelope(t, peer); response.Type != MsgResponse || response.ID != "req-title" {
		t.Fatalf("title response = %+v", response)
	}

	writeLivenessEnvelope(t, peer, Envelope{Type: MsgRequest, ID: "req-cancel", Request: &RequestPayload{Method: "command", Tool: "wait"}})
	if state := readLivenessEnvelope(t, peer); state.RequestState == nil || state.RequestState.State != "started" {
		t.Fatalf("cancelled request start = %+v", state)
	}
	writeLivenessEnvelope(t, peer, Envelope{Type: MsgCancel, ID: "req-cancel", Cancel: &CancelPayload{RequestID: "req-cancel", Reason: "user cancelled"}})
	if state := readLivenessEnvelope(t, peer); state.RequestState == nil || state.RequestState.State != "completed" {
		t.Fatalf("cancelled request completion = %+v", state)
	}
	if response := readLivenessEnvelope(t, peer); response.Type != MsgResponse || response.ID != "req-cancel" {
		t.Fatalf("cancelled response = %+v", response)
	}
	writeLivenessEnvelope(t, peer, Envelope{Type: MsgShutdown, Shutdown: &ShutdownPayload{Reason: "done"}})
}

// startAndAccept starts cmd and returns its connection to listener. A fixture
// that exits before connecting fails the test with its output instead of
// leaving Accept blocked; the process is killed and reaped at cleanup.
func startAndAccept(t *testing.T, cmd *exec.Cmd, listener net.Listener) net.Conn {
	t.Helper()
	var output bytes.Buffer
	cmd.Stdout, cmd.Stderr = &output, &output
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		<-exited
	})
	type accepted struct {
		conn net.Conn
		err  error
	}
	connected := make(chan accepted, 1)
	go func() {
		conn, err := listener.Accept()
		connected <- accepted{conn, err}
	}()
	select {
	case a := <-connected:
		if a.err != nil {
			t.Fatal(a.err)
		}
		return a.conn
	case err := <-exited:
		exited <- err
		t.Fatalf("fixture exited before connecting: %v\n%s", err, output.String())
	case <-time.After(30 * time.Second):
		// exec copies the child's output into output until Wait returns, so
		// read it only after the killed child has been reaped.
		_ = cmd.Process.Kill()
		err := <-exited
		exited <- err
		t.Fatalf("fixture did not connect within 30s (%v)\n%s", err, output.String())
	}
	return nil
}
