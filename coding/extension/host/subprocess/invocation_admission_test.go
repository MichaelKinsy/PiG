package subprocess

import (
	"context"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension/host/invocation"
)

// Pi's rpc prompt awaits command.handler (agent-session.ts:1766-1780) and writes the prompt response from preflightResult (rpc-mode.ts:394-413). A handler that settles without awaiting I/O has written that response before the next input event, stdin end included (rpc-mode.ts:804-807). The Node runtime therefore reports an RPC command as blocked only while its Promise is still pending after the synchronous prefix; a settled handler reports completion with its response.
func TestNodeRuntimeRPCCommandReportsBlockedOnlyWhileSuspended(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatalf("node is required for the Node runtime: %v", err)
	}
	// Unix socket paths have a short length limit, so keep the fixture under a short root as the liveness test does.
	parent := "/tmp"
	if runtime.GOOS == "windows" {
		parent = os.TempDir()
	}
	root, err := os.MkdirTemp(parent, "pig-rpc-admission-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	entry := filepath.Join(root, "admission.mjs")
	if err := os.WriteFile(entry, []byte(`export default function (pi) {
  pi.registerCommand("settled", { description: "settle in the synchronous prefix", handler: async () => {} });
  pi.registerCommand("plain", { description: "return without a Promise", handler: () => {} });
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
	cmd := exec.CommandContext(t.Context(), node, filepath.Join("runtime-node", "cli.mjs"), entry)
	cmd.Env = append(os.Environ(), "PIG_EXT_SOCKET="+address, "PIG_EXT_NAME=rpc-admission")
	peer := startAndAccept(t, cmd, listener)
	defer func() { _ = peer.Close() }()
	if register := readLivenessEnvelope(t, peer); register.Type != MsgRegister {
		t.Fatalf("register = %+v", register)
	}
	writeLivenessEnvelope(t, peer, Envelope{Type: MsgReady, Ready: &ReadyPayload{Cwd: root, Mode: "rpc", Width: 80, State: &StatePayload{HasUI: true}}})
	state := func(id string) string {
		t.Helper()
		env := readLivenessEnvelope(t, peer)
		if env.Type == MsgResponse && env.ID == id {
			return "response"
		}
		if env.RequestState == nil || env.RequestState.RequestID != id {
			t.Fatalf("%s: frame = %+v", id, env)
		}
		if env.RequestState.Reason != "" {
			return env.RequestState.State + ":" + env.RequestState.Reason
		}
		return env.RequestState.State
	}
	for _, command := range []string{"settled", "plain"} {
		writeLivenessEnvelope(t, peer, Envelope{Type: MsgRequest, ID: command, Request: &RequestPayload{Method: "command", Tool: command}})
		for _, want := range []string{"started", "completed", "response"} {
			if got := state(command); got != want {
				t.Fatalf("%s: frame %q, want %q: a settled handler never entered an awaited operation", command, got, want)
			}
		}
	}
	writeLivenessEnvelope(t, peer, Envelope{Type: MsgRequest, ID: "wait", Request: &RequestPayload{Method: "command", Tool: "wait"}})
	for _, want := range []string{"started", "blocked:external_io"} {
		if got := state("wait"); got != want {
			t.Fatalf("wait: frame %q, want %q", got, want)
		}
	}
	writeLivenessEnvelope(t, peer, Envelope{Type: MsgCancel, ID: "wait", Cancel: &CancelPayload{RequestID: "wait", Reason: "test done"}})
	for _, want := range []string{"completed", "response"} {
		got := state("wait")
		if got == "suspended" {
			// The runtime reports the command's event-loop window closing once, whether before or after the cancellation.
			got = state("wait")
		}
		if got != want {
			t.Fatalf("wait after cancellation: frame %q, want %q", got, want)
		}
	}
	writeLivenessEnvelope(t, peer, Envelope{Type: MsgShutdown, Shutdown: &ShutdownPayload{Reason: "done"}})
}

// A blocked report admits the caller while the handler is suspended. A completion report admits a caller that treats completion as a suspension boundary, such as the UI prompt drain, but not one that publishes the handler's result itself first: RPC admission must not release later input, including stdin end, before a completed extension command's prompt continuation is queued.
func TestRequestAcknowledgesSuspensionAndLeavesCompletionToItsBinder(t *testing.T) {
	clock := newManualLivenessClock()
	host, peer := net.Pipe()
	defer func() { _ = peer.Close() }()
	conn := newConnWithOptions("admission", host, connOptions{Clock: clock, HeartbeatInterval: 2 * time.Second, HeartbeatTimeout: time.Second})
	conn.Start(t.Context())
	request := func(ctx context.Context) <-chan error {
		result := make(chan error, 1)
		go func() {
			_, err := conn.Request(ctx, &Envelope{Type: MsgRequest, Request: &RequestPayload{Method: "command", Tool: "done"}})
			result <- err
		}()
		return result
	}
	for _, tc := range []struct {
		name           string
		suspensionOnly bool
		wantAcked      bool
	}{
		{"completion leaves a suspension binder waiting", true, false},
		{"completion releases an ordinary binder", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			acked := make(chan struct{})
			bind := invocation.WithAcknowledgment
			if tc.suspensionOnly {
				bind = invocation.WithSuspensionAcknowledgment
			}
			result := request(bind(t.Context(), func() { close(acked) }))
			sent := readLivenessEnvelope(t, peer)
			writeLivenessEnvelope(t, peer, Envelope{Type: MsgRequestState, RequestState: &RequestStatePayload{RequestID: sent.ID, State: "completed"}})
			writeLivenessEnvelope(t, peer, Envelope{Type: MsgResponse, ID: sent.ID, Response: &ResponsePayload{}})
			if err := <-result; err != nil {
				t.Fatal(err)
			}
			select {
			case <-acked:
				if !tc.wantAcked {
					t.Fatal("a completion report released a suspension-only acknowledgment")
				}
			default:
				if tc.wantAcked {
					t.Fatal("a completion report did not release an ordinary acknowledgment")
				}
			}
		})
	}
	t.Run("blocked releases a suspension binder before the response", func(t *testing.T) {
		acked := make(chan struct{})
		result := request(invocation.WithSuspensionAcknowledgment(t.Context(), func() { close(acked) }))
		sent := readLivenessEnvelope(t, peer)
		writeLivenessEnvelope(t, peer, Envelope{Type: MsgRequestState, RequestState: &RequestStatePayload{RequestID: sent.ID, State: "blocked", Reason: "external_io"}})
		select {
		case <-acked:
		case err := <-result:
			t.Fatalf("request ended before its suspension was acknowledged: %v", err)
		}
		writeLivenessEnvelope(t, peer, Envelope{Type: MsgResponse, ID: sent.ID, Response: &ResponsePayload{}})
		if err := <-result; err != nil {
			t.Fatal(err)
		}
	})
}
