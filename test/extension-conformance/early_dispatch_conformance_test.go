package extensionconformance

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
	"github.com/MichaelKinsy/PiG/internal/toolchain"
	"github.com/MichaelKinsy/PiG/test/extension-conformance/testfixture/earlydispatch"
)

// A host can dispatch to a listener of an extension that is still loading: after the register frame and before ready. Every SDK answers each such events.dispatch
// exactly once, under the request's own ID, and its handler serves the request's own payload. The host here is a script on the wire. It reads up to the register
// frame, then writes two dispatches and ready in ONE write, so the extension has all three before it acts on the first, and nothing in the order depends on timing.
// Each listener echoes the payload it was dispatched as an events.emit call, which carries the ID of the request that made it (parent_request_id): a handler that
// served another request's payload, or a fallback, shows in the pairing. The SDKs that start a handler at once (Go, Python) and the ones that queue the dispatch
// until ready (Rust, Lisp, Node) must agree on the pairing, not on the order of the two answers.
//
// Placements: every SDK as its own process (isolated), and the Go SDK in this process over a pipe (fused). A packed cell serves each member through the same
// Run, on a socket of the member's own, so its extension reads this same loop.

type earlyDispatchTarget struct {
	name  string
	start func(t *testing.T) net.Conn
	// node is the Node runtime: its bus carries a payload as {"$x":"json","v":payload} under "data", not under "json", and its listener echoes the payload as a JSON string.
	node bool
}

func TestEarlyDispatchBeforeReadyAnswersEachRequestOnceUnderItsOwnID(t *testing.T) {
	if testing.Short() {
		t.Skip("starts SDK subprocesses")
	}
	root := findModuleRoot(t)
	data := filepath.Join(root, "test", "extension-conformance", "testdata")
	targets := []earlyDispatchTarget{
		{name: "go", start: func(t *testing.T) net.Conn {
			bin := buildEarlyDispatchGo(t)
			return startLivenessSubprocess(t, func(string) *exec.Cmd { return exec.Command(bin) }, false)
		}},
		{name: "fused-go", start: func(t *testing.T) net.Conn {
			extensionConn, hostConn := net.Pipe()
			done := make(chan struct{})
			go func() {
				defer close(done)
				_ = earlydispatch.Extension().RunWithConn(extensionConn)
			}()
			t.Cleanup(func() {
				_ = hostConn.Close()
				<-done
			})
			return hostConn
		}},
		{name: "rust", start: func(t *testing.T) net.Conn {
			bin, err := rustEarlyDispatchBinary.Do(t, buildRustEarlyDispatchBinary)
			if err != nil {
				t.Fatal(err)
			}
			return startLivenessSubprocess(t, func(string) *exec.Cmd { return exec.Command(bin) }, false)
		}},
		{name: "python", start: func(t *testing.T) net.Conn {
			script := filepath.Join(data, "early-dispatch-python", "early.py")
			return startLivenessSubprocess(t, func(string) *exec.Cmd { return exec.Command(testPythonExecutable(), script) }, false)
		}},
		{name: "lisp", start: func(t *testing.T) net.Conn {
			requireSBCL(t)
			sbcl := toolchain.SBCLExecutable("", exec.LookPath)
			script := filepath.Join(data, "early-dispatch-lisp", "early.lisp")
			return startLivenessSubprocess(t, func(string) *exec.Cmd {
				return exec.Command(sbcl, "--noinform", "--non-interactive", "--no-sysinit", "--no-userinit", "--disable-debugger", "--load", script)
			}, false)
		}},
		{name: "node", start: func(t *testing.T) net.Conn {
			node := requireConformanceTool(t, "node")
			entry := filepath.Join(data, "early-dispatch-node", "early.mjs")
			cli := filepath.Join(root, "coding", "extension", "host", "subprocess", "runtime-node", "cli.mjs")
			return startLivenessSubprocess(t, func(string) *exec.Cmd {
				cmd := exec.Command(node, cli, entry)
				// A Node realm keeps pi.events in its own heap unless the Host routes the bus, as it does once a second realm joins.
				cmd.Env = []string{"PIG_EVENT_BUS=routed"}
				return cmd
			}, true)
		}, node: true},
	}
	for _, target := range targets {
		t.Run(target.name, func(t *testing.T) {
			peer := target.start(t)
			if err := peer.SetDeadline(time.Now().Add(30 * time.Second)); err != nil {
				t.Fatal(err)
			}
			runEarlyDispatchScript(t, peer, target.node)
		})
	}
}

// earlyDispatchPayloads is what each dispatch carries: distinct per request, so no fallback or default can stand for it.
var earlyDispatchPayloads = map[string]map[string]any{
	"d1": {"seq": float64(1), "tag": "first"},
	"d2": {"seq": float64(2), "tag": "second"},
}

func runEarlyDispatchScript(t *testing.T, peer net.Conn, node bool) {
	t.Helper()
	// The factory's own host calls come first: the listener's events.on is made before the register frame. The script answers them and remembers the handler.
	handlerID := ""
	for {
		env := readSDKEnvelope(t, peer)
		if env.Type == subprocess.MsgRegister {
			break
		}
		if node && env.Type == subprocess.MsgCall && env.Call != nil && env.Call.Method == "xref.hello" {
			writeSDKEnvelope(t, peer, subprocess.Envelope{Type: subprocess.MsgCallResult, ID: env.ID, CallResult: &subprocess.CallResultPayload{Result: json.RawMessage(`{}`)}})
			continue
		}
		if env.Type != subprocess.MsgCall || env.Call == nil || env.Call.Method != "events.on" {
			t.Fatalf("before register: frame %s %+v, want events.on", env.Type, env.Call)
		}
		var on struct {
			Channel   string `json:"channel"`
			HandlerID string `json:"handlerId"`
		}
		if err := json.Unmarshal(env.Call.Args, &on); err != nil {
			t.Fatal(err)
		}
		if on.Channel != earlydispatch.Channel || on.HandlerID == "" {
			t.Fatalf("events.on = %+v, want channel %s and a handler ID", on, earlydispatch.Channel)
		}
		handlerID = on.HandlerID
		writeSDKEnvelope(t, peer, subprocess.Envelope{Type: subprocess.MsgCallResult, ID: env.ID, CallResult: &subprocess.CallResultPayload{Result: json.RawMessage(`{}`)}})
	}
	if handlerID == "" {
		t.Fatal("the extension registered without subscribing its listener")
	}

	var burst bytes.Buffer
	frames := []subprocess.Envelope{}
	for _, id := range []string{"d1", "d2"} {
		wire := map[string]any{"handlerId": handlerID, "channel": earlydispatch.Channel, "json": earlyDispatchPayloads[id]}
		if node {
			wire = map[string]any{"handlerId": handlerID, "channel": earlydispatch.Channel, "data": map[string]any{"$x": "json", "v": earlyDispatchPayloads[id]}}
		}
		args, err := json.Marshal(wire)
		if err != nil {
			t.Fatal(err)
		}
		frames = append(frames, subprocess.Envelope{Type: subprocess.MsgRequest, ID: id, Request: &subprocess.RequestPayload{Method: "events.dispatch", Args: args}})
	}
	frames = append(frames, subprocess.Envelope{Type: subprocess.MsgReady, Ready: &subprocess.ReadyPayload{Cwd: "/fixture", Width: 80, Height: 24, State: &subprocess.StatePayload{HasUI: true}}})
	for _, frame := range frames {
		body, err := json.Marshal(frame)
		if err != nil {
			t.Fatal(err)
		}
		var header [4]byte
		binary.BigEndian.PutUint32(header[:], uint32(len(body)))
		burst.Write(header[:])
		burst.Write(body)
	}
	if _, err := peer.Write(burst.Bytes()); err != nil {
		t.Fatal(err)
	}

	responses := map[string]int{}
	echoed := map[string][]any{}
	note := func(env subprocess.Envelope) {
		switch env.Type {
		case subprocess.MsgResponse:
			if env.Response == nil || env.Response.Error != nil {
				t.Errorf("response %q = %+v, want success", env.ID, env.Response)
			}
			responses[env.ID]++
		case subprocess.MsgCall:
			if env.Call == nil || env.Call.Method != "events.emit" {
				t.Errorf("host call %+v, want events.emit", env.Call)
				return
			}
			var emit struct {
				Channel string `json:"channel"`
				JSON    any    `json:"json"`
				Data    any    `json:"data"`
			}
			if err := json.Unmarshal(env.Call.Args, &emit); err != nil {
				t.Fatal(err)
			}
			if node {
				text, _ := emit.Data.(string)
				if err := json.Unmarshal([]byte(text), &emit.JSON); err != nil {
					t.Errorf("the Node listener echoed %v, want a JSON string: %v", emit.Data, err)
				}
			}
			if emit.Channel != earlydispatch.EchoChannel {
				t.Errorf("emit on %q, want %q", emit.Channel, earlydispatch.EchoChannel)
			}
			echoed[env.Call.ParentRequestID] = append(echoed[env.Call.ParentRequestID], emit.JSON)
			writeSDKEnvelope(t, peer, subprocess.Envelope{Type: subprocess.MsgCallResult, ID: env.ID, CallResult: &subprocess.CallResultPayload{Result: json.RawMessage(`{}`)}})
		}
	}
	for len(responses) < 2 || len(echoed) < 2 {
		note(readSDKEnvelope(t, peer))
	}

	// Shutdown ends the connection: anything the extension still sends before then, a second answer included, is read and counted.
	writeSDKEnvelope(t, peer, subprocess.Envelope{Type: subprocess.MsgShutdown, Shutdown: &subprocess.ShutdownPayload{Reason: "done"}})
	for {
		env, err := tryReadSDKEnvelope(peer)
		if err != nil {
			break
		}
		note(env)
	}

	for _, id := range []string{"d1", "d2"} {
		if responses[id] != 1 {
			t.Errorf("answers under %s = %d, want exactly 1", id, responses[id])
		}
		if got := echoed[id]; len(got) != 1 || !reflect.DeepEqual(got[0], any(earlyDispatchPayloads[id])) {
			t.Errorf("the handler of %s echoed %v, want [%v]", id, got, earlyDispatchPayloads[id])
		}
	}
	for id := range responses {
		if _, ok := earlyDispatchPayloads[id]; !ok {
			t.Errorf("an answer under %q, which no request carried", id)
		}
	}
	for id := range echoed {
		if _, ok := earlyDispatchPayloads[id]; !ok {
			t.Errorf("an echo from request %q, which no request carried", id)
		}
	}
}

// tryReadSDKEnvelope reads one frame, or reports that the connection ended.
func tryReadSDKEnvelope(conn net.Conn) (subprocess.Envelope, error) {
	var env subprocess.Envelope
	var header [4]byte
	if _, err := io.ReadFull(conn, header[:]); err != nil {
		return env, err
	}
	body := make([]byte, binary.BigEndian.Uint32(header[:]))
	if _, err := io.ReadFull(conn, body); err != nil {
		return env, err
	}
	return env, json.Unmarshal(body, &env)
}

// buildEarlyDispatchGo builds the Go extension of the row the way buildSDKFixtureBinary builds the canonical one.
func buildEarlyDispatchGo(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), testExecutableName("early-dispatch"))
	cmd := exec.CommandContext(t.Context(), "go", "build", "-o", bin, ".")
	cmd.Dir = filepath.Join(fixtureSourceRoot, "test", "extension-conformance", "testfixture", "earlydispatch", "cmd")
	flags := strings.Join(slices.DeleteFunc(strings.Fields(os.Getenv("GOFLAGS")), func(f string) bool { return f == "-mod=mod" }), " ")
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOWORK="+filepath.Join(fixtureSourceRoot, "go.work"), "GOFLAGS="+flags)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build early-dispatch: %v\n%s", err, out)
	}
	return bin
}

var rustEarlyDispatchBinary testbudget.Once[string]

func buildRustEarlyDispatchBinary(ctx context.Context) (string, error) {
	if _, err := exec.LookPath("cargo"); err != nil {
		return "", fmt.Errorf("cargo is required for SDK conformance: %w", err)
	}
	src := filepath.Join(fixtureSourceRoot, "test", "extension-conformance", "testdata", "rust-sdk-fixture")
	target := strings.TrimSpace(os.Getenv("CARGO_TARGET_DIR"))
	if target == "" {
		target = filepath.Join(src, "target")
	} else if !filepath.IsAbs(target) {
		target = filepath.Join(src, target)
	}
	return buildFixtureCopy(ctx, target, "early_dispatch", filepath.Join(fixtureRoot, testExecutableName("early-dispatch-rust")), func(ctx context.Context) error {
		cmd := exec.CommandContext(ctx, "cargo", "build", "--release", "--quiet", "--bin", "early_dispatch")
		cmd.Dir = src
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("build early_dispatch: %w\n%s", err, out)
		}
		return nil
	})
}
