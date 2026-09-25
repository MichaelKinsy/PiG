package subprocess

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// terminalInputUI is a minimal UIContext that captures the handler the bridge
// registers, so a test can drive raw input through the real bridge path.
type terminalInputUI struct {
	extension.UIContext
	remote       extension.RemoteTerminalInputHandler
	unsubscribed atomic.Bool
}

func (u *terminalInputUI) OnRemoteTerminalInput(_ string, h extension.RemoteTerminalInputHandler) func() {
	u.remote = h
	return func() { u.unsubscribed.Store(true) }
}

// handler asks the registered listener with a live context, as the
// interactive host's input worker does.
func (u *terminalInputUI) handler(data string) extension.TerminalInputResult {
	return u.remote(context.Background(), data)
}

// TestTerminalInputConsumeRoundTrip pins that a subprocess extension can
// consume raw input, which upstream supports and pig previously refused.
func TestTerminalInputConsumeRoundTrip(t *testing.T) {
	ui := &terminalInputUI{UIContext: extension.NoopUIContext}
	bridge, conn, stop := newTerminalInputBridge(t, ui, func(data string) (bool, time.Duration) {
		return data == "\x1b", 0 // consume Escape only
	})
	defer stop()
	_ = bridge
	_ = conn

	if ui.remote == nil {
		t.Fatal("bridge did not register a terminal-input handler")
	}
	if got := ui.handler("\x1b"); !got.Consume {
		t.Error("Escape must be consumed by the subscribing extension")
	}
	if got := ui.handler("a"); got.Consume {
		t.Error("a key the extension declined must not be consumed")
	}
}

// TestTerminalInputWaitsForSlowVerdict pins upstream's synchronous listener:
// the input waits for the extension's verdict however long it takes, and a
// slow answer never costs the extension its subscription.
func TestTerminalInputWaitsForSlowVerdict(t *testing.T) {
	ui := &terminalInputUI{UIContext: extension.NoopUIContext}
	_, _, stop := newTerminalInputBridge(t, ui, func(string) (bool, time.Duration) {
		return true, 300 * time.Millisecond
	})
	defer stop()

	for range 6 {
		if got := ui.handler("\x1b"); !got.Consume {
			t.Fatal("a slow extension's consume verdict was dropped")
		}
	}
	if ui.unsubscribed.Load() {
		t.Fatal("slow answers unsubscribed the extension")
	}
}

// TestTerminalInputCarriesDataRewrite pins upstream's `data` result: the
// extension's replacement reaches the host, and a verdict without `data`
// leaves the input unchanged.
func TestTerminalInputCarriesDataRewrite(t *testing.T) {
	ui := &terminalInputUI{UIContext: extension.NoopUIContext}
	_, _, stop := newTerminalInputBridgeWithVerdict(t, ui, func(data string) (map[string]any, time.Duration) {
		if data == "j" {
			return map[string]any{"data": "\x1b"}, 0
		}
		return map[string]any{}, 0
	})
	defer stop()

	got := ui.handler("j")
	if got.Consume || got.Data == nil || *got.Data != "\x1b" {
		t.Fatalf("rewrite verdict = %+v, want data \\x1b", got)
	}
	if got := ui.handler("a"); got.Consume || got.Data != nil {
		t.Fatalf("plain verdict = %+v, want no change", got)
	}
}

// TestTerminalInputRequestEndsWithItsContext pins the owner of a pending
// verdict: the interactive host asks on a background task and cancels it at
// shutdown. The request ends with its context, returns the unchanged verdict,
// and tells the extension to stop, instead of waiting on an extension that
// never answers.
func TestTerminalInputRequestEndsWithItsContext(t *testing.T) {
	ui := &terminalInputUI{UIContext: extension.NoopUIContext}
	hostEnd, extEnd := net.Pipe()
	conn := NewConn("test-ext", hostEnd)
	connCtx, stopConn := context.WithCancel(t.Context())
	conn.Start(connCtx)
	defer func() { stopConn(); _ = extEnd.Close(); _ = hostEnd.Close() }()

	asked := make(chan string, 1)
	cancelled := make(chan string, 1)
	go func() {
		for {
			env, err := readEnvelopeFrom(extEnd)
			if err != nil {
				return
			}
			switch {
			case env.Type == MsgPing && env.Ping != nil:
				_ = writeEnvelopeTo(extEnd, &Envelope{Type: MsgPong, Pong: &PongPayload{Nonce: env.Ping.Nonce}})
			case env.Request != nil && env.Request.Method == "terminal_input":
				asked <- env.ID
			case env.Type == MsgCancel && env.Cancel != nil:
				cancelled <- env.Cancel.RequestID
			}
		}
	}()
	bridge := NewUIBridge(func() {})
	bridge.SetUIContext(ui)
	bridge.RegisterExtConn("test-ext", conn)
	if _, err := bridge.handleOnTerminalInput("test-ext", nil); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	verdict := make(chan extension.TerminalInputResult, 1)
	go func() { verdict <- ui.remote(ctx, "x") }()
	id := awaitTerminalInputSignal(t, asked, "the extension was never asked")
	cancel()
	if got := awaitTerminalInputSignal(t, verdict, "the request outlived its context"); got.Consume || got.Data != nil {
		t.Fatalf("cancelled verdict = %+v, want the input unchanged", got)
	}
	if got := awaitTerminalInputSignal(t, cancelled, "the extension was not told to stop"); got != id {
		t.Fatalf("cancelled request %q, want %q", got, id)
	}
}

// A restarted extension subscribes again on its new connection. Upstream
// keeps one listener per live subscription, so the old connection's listener
// must leave the input path instead of lingering beside the new one.
func TestTerminalInputResubscribeRetiresTheEarlierListener(t *testing.T) {
	ui := &terminalInputUI{UIContext: extension.NoopUIContext}
	bridge, _, stop := newTerminalInputBridge(t, ui, func(string) (bool, time.Duration) { return false, 0 })
	defer stop()
	first := ui.remote

	restarted, _ := net.Pipe()
	defer func() { _ = restarted.Close() }()
	bridge.RegisterExtConn("test-ext", NewConn("test-ext", restarted))
	if _, err := bridge.handleOnTerminalInput("test-ext", nil); err != nil {
		t.Fatal(err)
	}
	if !ui.unsubscribed.Load() {
		t.Fatal("the earlier connection's terminal-input listener stayed registered")
	}
	if first == nil {
		t.Fatal("no listener was registered before the restart")
	}
}

func awaitTerminalInputSignal[T any](t *testing.T, ch <-chan T, failure string) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(testbudget.Wait(t)):
		t.Fatal(failure)
		var zero T
		return zero
	}
}

// newTerminalInputBridge wires a UIBridge to a fake extension connection whose
// responder decides consume and how long it takes to answer.
func newTerminalInputBridge(t *testing.T, ui extension.UIContext, responder func(data string) (bool, time.Duration)) (*UIBridge, *Conn, func()) {
	t.Helper()
	return newTerminalInputBridgeWithVerdict(t, ui, func(data string) (map[string]any, time.Duration) {
		consume, delay := responder(data)
		return map[string]any{"consume": consume}, delay
	})
}

// newTerminalInputBridgeWithVerdict is newTerminalInputBridge with the
// extension's full verdict object.
func newTerminalInputBridgeWithVerdict(t *testing.T, ui extension.UIContext, responder func(data string) (map[string]any, time.Duration)) (*UIBridge, *Conn, func()) {
	t.Helper()
	hostEnd, extEnd := net.Pipe()
	conn := NewConn("test-ext", hostEnd)
	ctx, cancel := context.WithCancel(t.Context())
	conn.Start(ctx)

	// Stand in for the extension process: read request frames, answer them
	// through the responder.
	go func() {
		for {
			env, err := readEnvelope(extEnd)
			if err != nil {
				return
			}
			if env.Request == nil || env.Request.Method != "terminal_input" {
				continue
			}
			var p struct {
				Data string `json:"data"`
			}
			_ = json.Unmarshal(env.Request.Args, &p)
			verdict, delay := responder(p.Data)
			if delay > 0 {
				time.Sleep(delay)
			}
			result, _ := json.Marshal(verdict)
			_ = writeEnvelope(extEnd, &Envelope{Type: MsgResponse, ID: env.ID, Response: &ResponsePayload{Result: result}})
		}
	}()

	bridge := NewUIBridge(func() {})
	bridge.SetUIContext(ui)
	bridge.RegisterExtConn("test-ext", conn)
	if _, err := bridge.handleOnTerminalInput("test-ext", nil); err != nil {
		t.Fatalf("handleOnTerminalInput: %v", err)
	}
	return bridge, conn, func() { cancel(); _ = extEnd.Close(); _ = hostEnd.Close() }
}

// readEnvelope reads one length-prefixed frame.
func readEnvelope(c net.Conn) (*Envelope, error) {
	var lenBuf [4]byte
	if _, err := io.ReadFull(c, lenBuf[:]); err != nil {
		return nil, err
	}
	payload := make([]byte, binary.BigEndian.Uint32(lenBuf[:]))
	if _, err := io.ReadFull(c, payload); err != nil {
		return nil, err
	}
	var env Envelope
	if err := json.Unmarshal(payload, &env); err != nil {
		return nil, err
	}
	return &env, nil
}

// writeEnvelope writes one length-prefixed frame.
func writeEnvelope(c net.Conn, env *Envelope) error {
	payload, err := json.Marshal(env)
	if err != nil {
		return err
	}
	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], uint32(len(payload)))
	if _, err := c.Write(lenBuf[:]); err != nil {
		return err
	}
	_, err = c.Write(payload)
	return err
}

// TestTerminalInputNodeShimEndToEnd drives a pi-shaped TypeScript extension
// through the real node runtime: subscribe, consume a sentinel, pass an
// ordinary key, then unsubscribe. The node shim previously threw on
// onTerminalInput, so every pi extension reading raw input was dead under pig.
func TestTerminalInputNodeShimEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	shortSockDir(t)

	h := newTestHost(t)
	bridge := NewUIBridge(func() {})
	fakeUI := newTestUIContext()
	bridge.SetUIContext(fakeUI)
	h.SetUIBridge(bridge)
	defer h.Shutdown("test done")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ext, err := h.Load(ctx, ExtConfig{
		Name:    "terminal-input",
		Source:  filepath.Join("testdata", "terminal-input.mjs"),
		Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, ok := fakeUI.sendTerminalInput("\x1b[99~"); ok {
		t.Fatal("host forwarded input before the extension subscribed")
	}
	if err := ext.Commands["term_subscribe"].Handler(context.Background(), ""); err != nil {
		t.Fatal(err)
	}

	// Subscription crosses the wire: the command returns once the extension has
	// sent ui.onTerminalInput, which the host may not have processed yet. Poll
	// for registration, then assert the verdict on that same delivery.
	var consumed bool
	pollUntil(t, 5*time.Second, "extension subscribed but the host registered no input handler", func() bool {
		c, ok := fakeUI.sendTerminalInput("\x1b[99~")
		if !ok {
			return false
		}
		consumed = c
		return true
	})
	if !consumed {
		t.Error("sentinel chunk was not consumed by the node extension")
	}
	if consumed, _ := fakeUI.sendTerminalInput("a"); consumed {
		t.Error("ordinary keystroke was consumed; the editor would never see it")
	}

	if err := ext.Commands["term_unsubscribe"].Handler(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	pollUntil(t, 5*time.Second, "host kept forwarding input after unsubscribe", func() bool {
		_, ok := fakeUI.sendTerminalInput("\x1b[99~")
		return !ok
	})
}
