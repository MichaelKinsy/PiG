package subprocess

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// callOrderFixture runs handleIncoming over a real Conn and lets a test write
// raw call frames as the extension would.
type callOrderFixture struct {
	t      *testing.T
	ext    net.Conn
	writeM sync.Mutex
}

func newCallOrderFixture(t *testing.T, handle func(call *CallPayload) (*CallResultPayload, error)) *callOrderFixture {
	t.Helper()
	h := NewHostWithConfigRoot(t.TempDir(), t.TempDir())
	h.SetCallHandler(func(_ string, call *CallPayload) (*CallResultPayload, error) { return handle(call) })
	hostSide, extSide := net.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	conn := NewConn("order", hostSide)
	conn.Start(ctx)
	me := &managedExt{config: ExtConfig{Name: "order"}, host: h, conn: conn}
	me.shuttingDown.Store(true)
	go h.handleIncoming(me)
	go func() {
		buf := make([]byte, 1<<16)
		for {
			if _, err := extSide.Read(buf); err != nil {
				return
			}
		}
	}()
	t.Cleanup(func() {
		cancel()
		_ = extSide.Close()
		<-conn.Done()
	})
	return &callOrderFixture{t: t, ext: extSide}
}

func (f *callOrderFixture) call(id, method, parent string, args any) {
	f.t.Helper()
	raw, err := json.Marshal(args)
	if err != nil {
		f.t.Fatal(err)
	}
	data, err := json.Marshal(Envelope{Type: MsgCall, ID: id, Call: &CallPayload{Method: method, Args: raw, ParentRequestID: parent}})
	if err != nil {
		f.t.Fatal(err)
	}
	var length [4]byte
	binary.BigEndian.PutUint32(length[:], uint32(len(data)))
	f.writeM.Lock()
	defer f.writeM.Unlock()
	if _, err := f.ext.Write(length[:]); err != nil {
		f.t.Fatal(err)
	}
	if _, err := f.ext.Write(data); err != nil {
		f.t.Fatal(err)
	}
}

func waitSignal(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(testbudget.Wait(t)):
		t.Fatalf("timed out waiting for %s", what)
	}
}

// Upstream ui.setStatus, appendEntry, sendMessage, and setActiveTools are
// synchronous in-process calls, so a burst an extension does not await applies
// in program order and the last call sent is the last applied.
func TestExtensionCallsApplyInSendOrder(t *testing.T) {
	const n = 2000
	var mu sync.Mutex
	var got []int
	done := make(chan struct{})
	f := newCallOrderFixture(t, func(call *CallPayload) (*CallResultPayload, error) {
		var args struct{ Seq int }
		_ = json.Unmarshal(call.Args, &args)
		mu.Lock()
		got = append(got, args.Seq)
		if len(got) == n {
			close(done)
		}
		mu.Unlock()
		return &CallResultPayload{}, nil
	})
	for i := range n {
		f.call(fmt.Sprintf("c%d", i), "ui.setStatus", "", map[string]int{"Seq": i})
	}
	waitSignal(t, done, "all calls")
	mu.Lock()
	defer mu.Unlock()
	for i, seq := range got {
		if seq != i {
			t.Fatalf("call %d applied at position %d; want send order (first inversion)", seq, i)
		}
	}
}

// A call the host is still running may wait for a request it sent the same
// extension, whose handler makes nested calls. Those calls carry the request as
// their parent and must run while the earlier call waits.
func TestNestedCallsRunWhileAnEarlierCallWaits(t *testing.T) {
	nested := make(chan struct{})
	outerDone := make(chan struct{})
	f := newCallOrderFixture(t, func(call *CallPayload) (*CallResultPayload, error) {
		switch call.Method {
		case "setActiveTools":
			select {
			case <-nested:
			case <-time.After(testbudget.Wait(t)):
			}
			close(outerDone)
		case "ui.notify":
			close(nested)
		}
		return &CallResultPayload{}, nil
	})
	f.call("c1", "setActiveTools", "", map[string]any{})
	f.call("c2", "ui.notify", "r7", map[string]any{})
	waitSignal(t, nested, "nested call")
	waitSignal(t, outerDone, "outer call")
}

// A Promise-returning upstream API completes on its own, so a later
// synchronous call does not wait for a process or a user.
func TestAsyncCallsDoNotBlockLaterCalls(t *testing.T) {
	release := make(chan struct{})
	execStarted := make(chan struct{})
	statusApplied := make(chan struct{})
	h := NewHostWithConfigRoot(t.TempDir(), t.TempDir())
	bridge := NewUIBridge(func() {})
	bridge.SetUIContext(&testUIContext{UIContext: extension.NoopUIContext, onStatus: func(string, string) { close(statusApplied) }})
	bridge.SetActions(&HostCallbacks{ExecContext: func(ctx context.Context, _ string, _ []string, _ *extension.ExecOptions) (extension.ExecResult, error) {
		close(execStarted)
		extension.CallInitiated(ctx)
		<-release
		return extension.ExecResult{}, nil
	}})
	h.SetUIBridge(bridge)
	hostEnd, peer := net.Pipe()
	defer func() { _ = peer.Close() }()
	conn := NewConn("async", hostEnd)
	conn.Start(t.Context())
	defer func() { _ = conn.Close("test done") }()
	go func() {
		buf := make([]byte, 1<<16)
		for {
			if _, err := peer.Read(buf); err != nil {
				return
			}
		}
	}()
	defer close(release)
	me := &managedExt{config: ExtConfig{Name: "async"}, host: h, conn: conn}
	lanes := newCallLanes()
	h.queueCall(me, lanes, "c1", &CallPayload{Method: "exec", Args: json.RawMessage(`{"command":"x"}`)})
	h.queueCall(me, lanes, "c2", &CallPayload{Method: "ui.setStatus", Args: json.RawMessage(`{"key":"k","text":"t"}`)})
	waitSignal(t, execStarted, "exec start")
	waitSignal(t, statusApplied, "status after a pending exec")
}
