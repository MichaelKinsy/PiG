package subprocess

import (
	"context"
	"encoding/json"
	"net"
	"reflect"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

type terminalStateProbeUI struct {
	terminalInputUI
	reads atomic.Int32
}

func (u *terminalStateProbeUI) GetEditorText() string {
	u.reads.Add(1)
	return "worker must not read editor"
}

func (u *terminalStateProbeUI) GetToolsExpanded() bool {
	u.reads.Add(1)
	return false
}

func TestTerminalInputUsesOwnerSnapshotWithoutReadingMutableUI(t *testing.T) {
	ui := &terminalStateProbeUI{terminalInputUI: terminalInputUI{UIContext: extension.NoopUIContext}}
	hostEnd, extEnd := net.Pipe()
	conn := NewConn("state-probe", hostEnd)
	ctx, cancel := context.WithCancel(t.Context())
	conn.Start(ctx)
	peerDone := make(chan struct{})
	go func() {
		defer close(peerDone)
		for {
			env, err := readEnvelopeFrom(extEnd)
			if err != nil {
				return
			}
			if env.Type == MsgPing {
				_ = writeEnvelopeTo(extEnd, &Envelope{Type: MsgPong, Pong: &PongPayload{Nonce: env.Ping.Nonce}})
			} else if env.Request != nil {
				result, _ := json.Marshal(map[string]string{"data": string(env.Request.Args)})
				_ = writeEnvelopeTo(extEnd, &Envelope{Type: MsgResponse, ID: env.ID, Response: &ResponsePayload{Result: result}})
			}
		}
	}()
	t.Cleanup(func() { cancel(); _ = extEnd.Close(); _ = hostEnd.Close(); <-peerDone })
	bridge := NewUIBridge(func() {})
	bridge.SetUIContext(ui)
	bridge.RegisterExtConn("state-probe", conn)
	if _, err := bridge.handleOnTerminalInput("state-probe", nil); err != nil {
		t.Fatal(err)
	}
	for _, state := range []struct {
		text     string
		expanded bool
	}{{"captured draft", true}, {"", false}} {
		got := ui.remote(WithTerminalInputState(ctx, state.text, state.expanded), "x")
		if got.Data == nil {
			t.Fatalf("no snapshot echo: %+v", got)
		}
		var wire map[string]any
		if err := json.Unmarshal([]byte(*got.Data), &wire); err != nil {
			t.Fatal(err)
		}
		want := map[string]any{"data": "x", "editorText": state.text, "toolsExpanded": state.expanded}
		if !reflect.DeepEqual(wire, want) {
			t.Errorf("wire=%v, want %v", wire, want)
		}
	}
	if got := ui.reads.Load(); got != 0 {
		t.Fatalf("worker read mutable UI %d times", got)
	}
}
