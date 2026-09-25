package extensionconformance

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
	"github.com/MichaelKinsy/PiG/tests/extension-conformance/testfixture"
)

type livenessRecording struct {
	PingWhileBlocked       bool
	AwaitedHostCall        []string
	InteractiveHostCall    []string
	NoResultHostCall       []string
	ParentCancellation     []string
	LateResponseWasIgnored bool
}

type livenessFixture struct {
	name    string
	command func(string) *exec.Cmd
	serve   func(net.Conn) error
}

func TestLivenessConformanceSDKsMatch(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the Go and Rust SDK fixtures")
	}
	modRoot := findModuleRoot(t)
	goFixture := buildSDKFixture(t)
	rustFixture := buildRustSDKFixture(t)
	pythonFixture := buildPythonSDKFixture(t)
	node := requireConformanceTool(t, "node")
	nodeEntry := filepath.Join(t.TempDir(), "liveness.mjs")
	if err := os.WriteFile(nodeEntry, []byte(`export default function (pi) {
  pi.registerCommand("liveness_host_call", { handler: async (_args, ctx) => { await ctx.waitForIdle(); } });
  pi.registerCommand("liveness_user_call", { handler: async (_args, ctx) => { await ctx.ui.input("Question", "Answer"); } });
  pi.registerCommand("liveness_fire_call", { handler: async (_args, ctx) => { ctx.ui.setTitle("Conformance title"); } });
}`), 0o600); err != nil {
		t.Fatal(err)
	}

	fixtures := []livenessFixture{
		{name: "go", command: func(string) *exec.Cmd { return exec.Command(goFixture) }},
		{name: "fused-go", serve: testfixture.Extension().RunWithConn},
		{name: "rust", command: func(string) *exec.Cmd { return exec.Command(rustFixture) }},
		{name: "python", command: func(string) *exec.Cmd {
			return exec.Command(testPythonExecutable(), pythonFixture)
		}},
		{name: "node", command: func(string) *exec.Cmd {
			return exec.Command(node, filepath.Join(modRoot, "coding", "extension", "host", "subprocess", "runtime-node", "cli.mjs"), nodeEntry)
		}},
	}

	var baseline livenessRecording
	for index, fixture := range fixtures {
		t.Run(fixture.name, func(t *testing.T) {
			recording := recordSDKLiveness(t, fixture)
			if index == 0 {
				baseline = recording
				return
			}
			if !reflect.DeepEqual(recording, baseline) {
				t.Fatalf("liveness recording differs from Go:\n got: %#v\nwant: %#v", recording, baseline)
			}
		})
	}
}

func recordSDKLiveness(t *testing.T, fixture livenessFixture) livenessRecording {
	t.Helper()
	var peer net.Conn
	if fixture.serve != nil {
		extensionConn, hostConn := net.Pipe()
		peer = hostConn
		done := make(chan struct{})
		go func() {
			defer close(done)
			_ = fixture.serve(extensionConn)
		}()
		t.Cleanup(func() {
			_ = peer.Close()
			<-done
		})
	} else {
		peer = startLivenessSubprocess(t, fixture.command, fixture.name == "node")
	}
	if err := peer.SetDeadline(time.Now().Add(20 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if register := readSDKEnvelope(t, peer); register.Type != subprocess.MsgRegister {
		t.Fatalf("first frame = %+v", register)
	}
	writeSDKEnvelope(t, peer, subprocess.Envelope{Type: subprocess.MsgReady, Ready: &subprocess.ReadyPayload{Cwd: "/fixture", Width: 80, Height: 24}})

	recording := livenessRecording{}
	recording.AwaitedHostCall, _ = recordHostCall(t, peer, "awaited", "liveness_host_call", "waitForIdle", nil, false)
	recording.InteractiveHostCall, recording.PingWhileBlocked = recordHostCall(t, peer, "interactive", "liveness_user_call", "ui.input", json.RawMessage(`{"text":"ok","ok":true}`), true)
	recording.NoResultHostCall, _ = recordHostCall(t, peer, "no-result", "liveness_fire_call", "ui.setTitle", nil, false)

	writeSDKEnvelope(t, peer, subprocess.Envelope{Type: subprocess.MsgRequest, ID: "cancelled", Request: &subprocess.RequestPayload{Method: "command", Tool: "liveness_user_call"}})
	started := readSDKEnvelope(t, peer)
	blocked := readSDKEnvelope(t, peer)
	call := readSDKEnvelope(t, peer)
	recording.ParentCancellation = append(recording.ParentCancellation,
		normalizeState(started), normalizeState(blocked), normalizeCall(call, "cancelled"))
	writeSDKEnvelope(t, peer, subprocess.Envelope{Type: subprocess.MsgCancel, ID: "cancelled", Cancel: &subprocess.CancelPayload{RequestID: "cancelled", Reason: "test cancellation"}})
	progress := readSDKEnvelope(t, peer)
	completed := readSDKEnvelope(t, peer)
	response := readSDKEnvelope(t, peer)
	recording.ParentCancellation = append(recording.ParentCancellation,
		normalizeState(progress), normalizeState(completed), normalizeResponse(response, "cancelled"))

	writeSDKEnvelope(t, peer, subprocess.Envelope{Type: subprocess.MsgCallResult, ID: call.ID, CallResult: &subprocess.CallResultPayload{Result: json.RawMessage(`{"text":"late","ok":true}`)}})
	writeSDKEnvelope(t, peer, subprocess.Envelope{Type: subprocess.MsgPing, Ping: &subprocess.PingPayload{Nonce: "after-late"}})
	pong := readSDKEnvelope(t, peer)
	recording.LateResponseWasIgnored = pong.Type == subprocess.MsgPong && pong.Pong != nil && pong.Pong.Nonce == "after-late"

	writeSDKEnvelope(t, peer, subprocess.Envelope{Type: subprocess.MsgShutdown, Shutdown: &subprocess.ShutdownPayload{Reason: "done"}})
	return recording
}

// startLivenessSubprocess listens the way the extension host does, so the
// fixture connects over the transport production gives it (a named pipe for
// Node on Windows), and fails at once if the fixture exits before connecting.
func startLivenessSubprocess(t *testing.T, command func(string) *exec.Cmd, node bool) net.Conn {
	t.Helper()
	parent := "/tmp"
	if runtime.GOOS == "windows" {
		parent = os.TempDir()
	}
	socketDir, err := os.MkdirTemp(parent, "pig-sdk-live-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(socketDir) })
	listener, address, err := subprocess.ListenExtension(filepath.Join(socketDir, "extension.sock"), node)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	cmd := command(address)
	cmd.Env = append(os.Environ(), "PIG_EXT_SOCKET="+address, "PIG_EXT_NAME=liveness-conformance")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		<-exited
	})
	accepted := make(chan net.Conn, 1)
	acceptErr := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			acceptErr <- err
			return
		}
		accepted <- conn
	}()
	select {
	case peer := <-accepted:
		t.Cleanup(func() { _ = peer.Close() })
		return peer
	case err := <-acceptErr:
		t.Fatal(err)
	case err := <-exited:
		exited <- err
		t.Fatalf("SDK fixture exited before connecting: %v", err)
	case <-time.After(testbudget.Wait(t)):
		t.Fatalf("SDK fixture did not connect within %s", testbudget.Wait(t))
	}
	return nil
}

func recordHostCall(t *testing.T, peer net.Conn, id, command, method string, result json.RawMessage, ping bool) ([]string, bool) {
	t.Helper()
	writeSDKEnvelope(t, peer, subprocess.Envelope{Type: subprocess.MsgRequest, ID: id, Request: &subprocess.RequestPayload{Method: "command", Tool: command}})
	started := readSDKEnvelope(t, peer)
	blocked := readSDKEnvelope(t, peer)
	call := readSDKEnvelope(t, peer)
	frames := []string{normalizeState(started), normalizeState(blocked), normalizeCall(call, id)}
	pongOK := false
	if ping {
		writeSDKEnvelope(t, peer, subprocess.Envelope{Type: subprocess.MsgPing, Ping: &subprocess.PingPayload{Nonce: "while-blocked"}})
		pong := readSDKEnvelope(t, peer)
		pongOK = pong.Type == subprocess.MsgPong && pong.Pong != nil && pong.Pong.Nonce == "while-blocked"
	}
	if call.Call == nil || call.Call.Method != method {
		t.Fatalf("%s call = %+v, want %s", command, call, method)
	}
	writeSDKEnvelope(t, peer, subprocess.Envelope{Type: subprocess.MsgCallResult, ID: call.ID, CallResult: &subprocess.CallResultPayload{Result: result}})
	frames = append(frames,
		normalizeState(readSDKEnvelope(t, peer)),
		normalizeState(readSDKEnvelope(t, peer)),
		normalizeResponse(readSDKEnvelope(t, peer), id),
	)
	return frames, pongOK
}

func normalizeState(env subprocess.Envelope) string {
	if env.Type != subprocess.MsgRequestState || env.RequestState == nil {
		return "unexpected:" + env.Type
	}
	return fmt.Sprintf("state:%s:%s", env.RequestState.State, env.RequestState.Reason)
}

func normalizeCall(env subprocess.Envelope, parent string) string {
	if env.Type != subprocess.MsgCall || env.Call == nil {
		return "unexpected:" + env.Type
	}
	return fmt.Sprintf("call:%s:parent=%t", env.Call.Method, env.Call.ParentRequestID == parent)
}

func normalizeResponse(env subprocess.Envelope, id string) string {
	if env.Type != subprocess.MsgResponse || env.ID != id || env.Response == nil {
		return "unexpected:" + env.Type
	}
	return fmt.Sprintf("response:error=%t", env.Response.Error != nil)
}

func readSDKEnvelope(t *testing.T, conn net.Conn) subprocess.Envelope {
	t.Helper()
	var header [4]byte
	if _, err := io.ReadFull(conn, header[:]); err != nil {
		t.Fatal(err)
	}
	body := make([]byte, binary.BigEndian.Uint32(header[:]))
	if _, err := io.ReadFull(conn, body); err != nil {
		t.Fatal(err)
	}
	var env subprocess.Envelope
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatal(err)
	}
	return env
}

func writeSDKEnvelope(t *testing.T, conn net.Conn, env subprocess.Envelope) {
	t.Helper()
	body, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], uint32(len(body)))
	if _, err := conn.Write(append(header[:], body...)); err != nil {
		t.Fatal(err)
	}
}
