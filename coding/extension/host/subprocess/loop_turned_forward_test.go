package subprocess

import (
	"net"
	"testing"
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
		h.forwardLoopTurned(h.exts[tc.from].connection())
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
