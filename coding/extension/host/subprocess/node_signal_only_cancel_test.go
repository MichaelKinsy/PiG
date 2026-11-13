package subprocess

import (
	"encoding/binary"
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

const (
	signalOnlyCancelledCall = "host call cancelled with its parent request"
	signalOnlyFixture       = `export default function (pi) {
  pi.registerCommand("probe", { handler: async (_args, ctx) => {
    const report = {};
    report.idle1 = ctx.isIdle();
    try { await ctx.ui.input("after signal"); report.afterSignal = "answered"; } catch (e) { report.afterSignal = e.message; }
    try { ctx.isIdle(); report.idle2 = "answered"; } catch (e) { report.idle2 = e.message; }
    try { await ctx.ui.input("after ordinary"); report.afterOrdinary = "answered"; } catch (e) { report.afterOrdinary = e.message; }
    throw new Error(JSON.stringify(report));
  } });
  pi.registerCommand("park", { handler: async (_args, ctx) => { ctx.isIdle(); } });
  pi.registerCommand("late", { handler: async (_args, ctx) => {
    await new Promise((resolve) => setTimeout(resolve, 50));
    let outcome = "answered";
    try { await ctx.ui.input("late"); } catch (e) { outcome = e.message; }
    throw new Error(outcome);
  } });
}`
)

// startSignalOnlyRuntime starts the Node runtime on signalOnlyFixture and completes its register/ready handshake.
func startSignalOnlyRuntime(t *testing.T) net.Conn {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skipf("node unavailable: %v", err)
	}
	root := testenv.ShortTempDir(t, "pig-node-signal-only-")
	entry := filepath.Join(root, "signal_only.mjs")
	if err := os.WriteFile(entry, []byte(signalOnlyFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	listener, address, err := ListenExtension(filepath.Join(root, "runtime.sock"), true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	cmd := exec.CommandContext(t.Context(), node, filepath.Join("runtime-node", "cli.mjs"), entry)
	cmd.Env = append(os.Environ(), "PIG_EXT_SOCKET="+address, "PIG_EXT_NAME=node-signal-only")
	peer := startAndAccept(t, cmd, listener)
	t.Cleanup(func() { _ = peer.Close() })
	if err := peer.SetReadDeadline(time.Now().Add(testbudget.Wait(t))); err != nil {
		t.Fatal(err)
	}
	if register := readLivenessEnvelope(t, peer); register.Type != MsgRegister {
		t.Fatalf("register = %+v", register)
	}
	writeLivenessEnvelope(t, peer, Envelope{Type: MsgReady, Ready: &ReadyPayload{Cwd: root, Width: 80, State: &StatePayload{HasUI: true}}})
	return peer
}

// readSignalOnlyFrame returns the next frame that is not a request-state report.
func readSignalOnlyFrame(t *testing.T, peer net.Conn) Envelope {
	t.Helper()
	for {
		if env := readLivenessEnvelope(t, peer); env.Type != MsgRequestState {
			return env
		}
	}
}

// writeSignalOnlyFrames sends the frames in one write, so the runtime reads them back to back.
func writeSignalOnlyFrames(t *testing.T, peer net.Conn, envs ...Envelope) {
	t.Helper()
	var wire []byte
	for _, env := range envs {
		body, err := json.Marshal(env)
		if err != nil {
			t.Fatal(err)
		}
		wire = binary.BigEndian.AppendUint32(wire, uint32(len(body)))
		wire = append(wire, body...)
	}
	if _, err := peer.Write(wire); err != nil {
		t.Fatal(err)
	}
}

// wantSignalOnlyCall fails unless env is a host call of method made for request.
func wantSignalOnlyCall(t *testing.T, env Envelope, method, request string) {
	t.Helper()
	if env.Type != MsgCall || env.Call == nil || env.Call.Method != method || env.Call.ParentRequestID != request {
		t.Fatalf("frame = %+v, want %s call of %s", env, method, request)
	}
}

// A signal-only cancel leaves the request's host calls running: a call made after it is sent and answered. An ordinary cancel that follows it clears the mark: the handler's pending synchronous call fails and its later calls are refused. The Connection reader applies the cancel while the handler waits in a synchronous call, before the serve loop reads it.
func TestNodeOrdinaryCancelAfterSignalOnlyCancelEndsHostCalls(t *testing.T) {
	peer := startSignalOnlyRuntime(t)
	writeLivenessEnvelope(t, peer, Envelope{Type: MsgRequest, ID: "req-probe", Request: &RequestPayload{Method: "command", Tool: "probe"}})

	idle := readSignalOnlyFrame(t, peer)
	wantSignalOnlyCall(t, idle, "isIdle", "req-probe")
	writeSignalOnlyFrames(t, peer,
		Envelope{Type: MsgCancel, ID: "req-probe", Cancel: &CancelPayload{RequestID: "req-probe", Reason: "signal ended", SignalOnly: true}},
		Envelope{Type: MsgCallResult, ID: idle.ID, CallResult: &CallResultPayload{Result: json.RawMessage(`{"idle":true}`)}},
	)

	input := readSignalOnlyFrame(t, peer)
	wantSignalOnlyCall(t, input, "ui.input", "req-probe")
	writeLivenessEnvelope(t, peer, Envelope{Type: MsgCallResult, ID: input.ID, CallResult: &CallResultPayload{Result: json.RawMessage(`{"text":"ok","ok":true}`)}})

	idle = readSignalOnlyFrame(t, peer)
	wantSignalOnlyCall(t, idle, "isIdle", "req-probe")
	writeLivenessEnvelope(t, peer, Envelope{Type: MsgCancel, ID: "req-probe", Cancel: &CancelPayload{RequestID: "req-probe", Reason: "request ended"}})

	response := readSignalOnlyFrame(t, peer)
	if response.Type != MsgResponse || response.ID != "req-probe" || response.Response == nil || response.Response.Error == nil {
		t.Fatalf("frame after the ordinary cancel = %+v, want the handler's failure response", response)
	}
	message := response.Response.Error.Message
	start, end := strings.Index(message, "{"), strings.LastIndex(message, "}")
	if start < 0 || end < start {
		t.Fatalf("handler failure %q carries no report", message)
	}
	var report struct {
		Idle1         bool   `json:"idle1"`
		AfterSignal   string `json:"afterSignal"`
		Idle2         string `json:"idle2"`
		AfterOrdinary string `json:"afterOrdinary"`
	}
	if err := json.Unmarshal([]byte(message[start:end+1]), &report); err != nil {
		t.Fatalf("handler report %q: %v", message, err)
	}
	if !report.Idle1 || report.AfterSignal != "answered" {
		t.Errorf("after the signal-only cancel: idle1=%v afterSignal=%q, want true and answered", report.Idle1, report.AfterSignal)
	}
	if report.Idle2 != "host call cancelled with parent request req-probe" {
		t.Errorf("pending synchronous call after the ordinary cancel = %q", report.Idle2)
	}
	if report.AfterOrdinary != signalOnlyCancelledCall {
		t.Errorf("call after the ordinary cancel = %q, want %q", report.AfterOrdinary, signalOnlyCancelledCall)
	}
}

// Both cancels reach the runtime before the serve loop starts the request (it is parked behind another handler's synchronous call), so the Connection reader finds no controller and the serve loop applies them in order: the ordinary one clears the signal-only mark, and the handler's host call is refused.
func TestNodeServeLoopOrdinaryCancelAfterSignalOnlyCancelEndsHostCalls(t *testing.T) {
	peer := startSignalOnlyRuntime(t)
	writeLivenessEnvelope(t, peer, Envelope{Type: MsgRequest, ID: "req-park", Request: &RequestPayload{Method: "command", Tool: "park"}})
	idle := readSignalOnlyFrame(t, peer)
	wantSignalOnlyCall(t, idle, "isIdle", "req-park")
	writeSignalOnlyFrames(t, peer,
		Envelope{Type: MsgRequest, ID: "req-late", Request: &RequestPayload{Method: "command", Tool: "late"}},
		Envelope{Type: MsgCancel, ID: "req-late", Cancel: &CancelPayload{RequestID: "req-late", Reason: "signal ended", SignalOnly: true}},
		Envelope{Type: MsgCancel, ID: "req-late", Cancel: &CancelPayload{RequestID: "req-late", Reason: "request ended"}},
		Envelope{Type: MsgCallResult, ID: idle.ID, CallResult: &CallResultPayload{Result: json.RawMessage(`{"idle":true}`)}},
	)

	for {
		env := readSignalOnlyFrame(t, peer)
		if env.Type == MsgCall {
			t.Fatalf("handler of the cancelled request made a host call after both cancels: %+v", env)
		}
		if env.Type != MsgResponse || env.ID != "req-late" {
			continue
		}
		if env.Response == nil || env.Response.Error == nil || !strings.Contains(env.Response.Error.Message, signalOnlyCancelledCall) {
			t.Fatalf("response = %+v, want the failure %q", env.Response, signalOnlyCancelledCall)
		}
		return
	}
}
