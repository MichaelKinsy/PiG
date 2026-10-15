package subprocess

import (
	"net"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// A loop turn is the sending process's own: its loop already ran the callbacks its members queued. Packed members have no OS process handle of their own, so the host names their process by processIdentity. A copy on a packed sibling's connection could arrive after the sender's handler answered and end the wait that answer began, letting a queued timer run with a live ctx where Pi's runs after invalidate.
func TestLoopTurnedReachesOnlyOtherNodeProcesses(t *testing.T) {
	h := NewHostWithConfigRoot(t.TempDir(), t.TempDir())
	peers := map[string]net.Conn{}
	attach := func(me *managedExt) *managedExt {
		hostSide, extSide := net.Pipe()
		conn := NewConn(me.config.Name, hostSide)
		conn.Start(t.Context())
		t.Cleanup(func() {
			_ = conn.Close("test done")
			_ = extSide.Close()
		})
		peers[me.config.Name] = extSide
		return withConn(me, conn)
	}
	cell := &packedProcessState{node: true, key: "cell"}
	owner := attach(&managedExt{config: ExtConfig{Name: "isolated"}, host: h, nodeRuntime: true})
	exts := []*managedExt{
		attach(&managedExt{config: ExtConfig{Name: "packed-a"}, host: h, packedProcess: cell}),
		attach(&managedExt{config: ExtConfig{Name: "packed-b"}, host: h, packedProcess: cell}),
		owner,
		attach(&managedExt{config: ExtConfig{Name: "adopted"}, host: h, nodeRuntime: true, procOwner: owner}),
		attach(&managedExt{config: ExtConfig{Name: "go"}, host: h}),
	}
	h.mu.Lock()
	h.exts = map[string]*managedExt{}
	for _, me := range exts {
		h.exts[me.config.Name] = me
	}
	h.mu.Unlock()

	marker := encodedEnvelope(`{"type":"notify","notify":{"method":"marker"}}`)
	for _, tc := range []struct {
		from string
		want map[string]bool
	}{
		{"packed-a", map[string]bool{"isolated": true, "adopted": true}},
		{"isolated", map[string]bool{"packed-a": true, "packed-b": true}},
		{"adopted", map[string]bool{"packed-a": true, "packed-b": true}},
	} {
		h.forwardLoopTurned(h.exts[tc.from].connection(), json.RawMessage(`{"turn":1}`))
		// Every connection gets a marker after the forward; the writer sends in queue order, so a connection that got no turn reads the marker first.
		for _, me := range exts {
			if me.config.Name == tc.from {
				continue
			}
			if err := me.connection().sendEncoded(marker); err != nil {
				t.Fatal(err)
			}
			got := readLivenessEnvelope(t, peers[me.config.Name])
			turned := got.Type == MsgNotify && got.Notify != nil && got.Notify.Method == NotifyLoopTurned
			if turned != tc.want[me.config.Name] {
				t.Fatalf("turn from %s: %s read %+v first, want a turn: %v", tc.from, me.config.Name, got.Notify, tc.want[me.config.Name])
			}
			if turned {
				if next := readLivenessEnvelope(t, peers[me.config.Name]); next.Notify == nil || next.Notify.Method != "marker" {
					t.Fatalf("turn from %s: %s read %+v after the turn, want the marker", tc.from, me.config.Name, next.Notify)
				}
			}
		}
	}
}

// A turn's sender resumes its handler only once every connection the host forwarded the turn to took it or closed, so the callbacks a stalled recipient queued before the turn run before that handler's continuation, as on Pi's one loop. The host answers the sender with the sender's own number, and at once when no other Node process exists.
func TestLoopTurnIsTakenOnlyOnceEveryRecipientTookIt(t *testing.T) {
	h := NewHostWithConfigRoot(t.TempDir(), t.TempDir())
	peers := map[string]net.Conn{}
	attach := func(me *managedExt) *managedExt {
		hostSide, extSide := net.Pipe()
		conn := NewConn(me.config.Name, hostSide)
		h.observeXref(conn)
		conn.Start(t.Context())
		t.Cleanup(func() {
			_ = conn.Close("test done")
			_ = extSide.Close()
		})
		peers[me.config.Name] = extSide
		return withConn(me, conn)
	}
	setExts := func(exts ...*managedExt) {
		h.mu.Lock()
		defer h.mu.Unlock()
		h.exts = map[string]*managedExt{}
		for _, me := range exts {
			h.exts[me.config.Name] = me
		}
	}
	turn := func(name string, method string, number uint64) {
		t.Helper()
		writeLivenessEnvelope(t, peers[name], Envelope{Type: MsgNotify, Notify: &NotifyPayload{Method: method, Args: json.RawMessage(loopTurnFrameArgs(number))}})
	}
	readTurn := func(name, method string) uint64 {
		t.Helper()
		got := readLivenessEnvelope(t, peers[name])
		var args LoopTurnArgs
		if got.Type != MsgNotify || got.Notify == nil || got.Notify.Method != method || json.Unmarshal(got.Notify.Args, &args) != nil {
			t.Fatalf("%s read %+v, want %s", name, got.Notify, method)
		}
		return args.Turn
	}

	cell := &packedProcessState{node: true, key: "cell"}
	setExts(
		attach(&managedExt{config: ExtConfig{Name: "sender"}, host: h, nodeRuntime: true}),
		attach(&managedExt{config: ExtConfig{Name: "isolated"}, host: h, nodeRuntime: true}),
		attach(&managedExt{config: ExtConfig{Name: "packed-a"}, host: h, packedProcess: cell}),
		attach(&managedExt{config: ExtConfig{Name: "packed-b"}, host: h, packedProcess: cell}),
		attach(&managedExt{config: ExtConfig{Name: "go"}, host: h}),
	)
	turn("sender", NotifyLoopTurned, 7)
	forwarded := readTurn("isolated", NotifyLoopTurned)
	for _, name := range []string{"packed-a", "packed-b"} {
		if got := readTurn(name, NotifyLoopTurned); got != forwarded {
			t.Fatalf("%s read turn %d, want the forwarded %d", name, got, forwarded)
		}
	}
	turn("isolated", NotifyLoopTurnTaken, forwarded)
	turn("packed-a", NotifyLoopTurnTaken, forwarded)
	// packed-b has not taken the turn: the sender reads nothing before a marker the host sends once the other two were read.
	deadline := time.Now().Add(10 * time.Second)
	for {
		h.loopTurns.mu.Lock()
		pending := len(h.loopTurns.turns[forwarded].pending)
		h.loopTurns.mu.Unlock()
		if pending == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("turn %d has %d recipients left, want packed-b's alone", forwarded, pending)
		}
		time.Sleep(time.Millisecond)
	}
	if err := h.exts["sender"].connection().sendEncoded(encodedEnvelope(`{"type":"notify","notify":{"method":"marker"}}`)); err != nil {
		t.Fatal(err)
	}
	if got := readLivenessEnvelope(t, peers["sender"]); got.Notify == nil || got.Notify.Method != "marker" {
		t.Fatalf("sender read %+v before packed-b took the turn, want the marker", got.Notify)
	}
	// A recipient whose connection closes can take no turn.
	_ = peers["packed-b"].Close()
	if got := readTurn("sender", NotifyLoopTurnTaken); got != 7 {
		t.Fatalf("sender read turn %d taken, want its own 7", got)
	}

	// With no other Node process, the turn is taken at once.
	setExts(h.exts["sender"], h.exts["go"])
	turn("sender", NotifyLoopTurned, 8)
	if got := readTurn("sender", NotifyLoopTurnTaken); got != 8 {
		t.Fatalf("sender read turn %d taken, want its own 8", got)
	}
}

// loopTurnFrameArgs encodes LoopTurnArgs numbered turn.
func loopTurnFrameArgs(turn uint64) string {
	args, err := json.Marshal(LoopTurnArgs{Turn: turn})
	if err != nil {
		panic(err)
	}
	return string(args)
}
