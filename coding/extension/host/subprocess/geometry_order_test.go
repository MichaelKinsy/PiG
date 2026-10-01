package subprocess

import (
	"encoding/json"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// A reload's geometry resynchronization runs on the reload goroutine while the
// renderer's resize notification runs on its own. Each records what an
// extension has seen and then enqueues the notification; if a later resize
// records and enqueues between the resynchronization's record and its
// enqueue, the extension receives the newer height first and the older one
// last while the host records the newer one, and no later resize to that
// height repairs it. The first extension's queue is full so the
// resynchronization blocks there with the second extension's notification
// still unsent; the resize must not overtake it.
func TestGeometryResyncAndResizeReachAConnectionInRecordedOrder(t *testing.T) {
	for attempt := range 8 {
		t.Run(fmt.Sprint(attempt), func(t *testing.T) {
			h := NewHostWithConfigRoot(t.TempDir(), t.TempDir())
			var height atomic.Int64
			height.Store(60)
			h.SetHeightFunc(func() int { return int(height.Load()) })
			blocked, other := geometryOrderExt(t, h, "blocked"), geometryOrderExt(t, h, "other")
			for range cap(blocked.connection().outCh) {
				if err := blocked.connection().Send(&Envelope{Type: MsgNotify, Notify: &NotifyPayload{Method: "filler"}}); err != nil {
					t.Fatal(err)
				}
			}

			var wg sync.WaitGroup
			wg.Go(func() { h.syncGeometry([]*managedExt{blocked, other}) })
			waitSeenHeight(t, h, other, 60)
			// The terminal grows again; the renderer notifies its new height.
			height.Store(80)
			wg.Go(func() { h.NotifyHeight(80) })
			// Give an unordered resize the chance to reach the unblocked connection first.
			deadline := time.Now().Add(100 * time.Millisecond)
			for time.Now().Before(deadline) && len(other.connection().outCh) < 2 {
				time.Sleep(time.Millisecond)
			}
			drained := make(chan struct{})
			go func() {
				defer close(drained)
				for range cap(blocked.connection().outCh) + 2 {
					<-blocked.connection().outCh
				}
			}()
			wg.Wait()
			<-drained

			got := heightsSent(t, other.connection())
			if len(got) != 2 || got[1] != 80 {
				t.Fatalf("other extension received heights %v, want [60 80]: the last height it sees must be the one the host recorded", got)
			}
		})
	}
}

func geometryOrderExt(t *testing.T, h *Host, name string) *managedExt {
	t.Helper()
	hostSide, peer := net.Pipe()
	t.Cleanup(func() { _ = hostSide.Close(); _ = peer.Close() })
	me := withConn(&managedExt{config: ExtConfig{Name: name}, host: h, seenHeight: 40}, NewConn(name, hostSide))
	h.mu.Lock()
	h.exts[name] = me
	h.mu.Unlock()
	return me
}

func waitSeenHeight(t *testing.T, h *Host, me *managedExt, want int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		h.mu.Lock()
		seen := me.seenHeight
		h.mu.Unlock()
		if seen == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("%s never recorded height %d", me.config.Name, want)
}

// heightsSent returns the heights of the height_change notifications queued on conn, in order.
func heightsSent(t *testing.T, conn *Conn) []int {
	t.Helper()
	var heights []int
	for len(conn.outCh) > 0 {
		var env Envelope
		if err := json.Unmarshal((<-conn.outCh).data, &env); err != nil {
			t.Fatal(err)
		}
		var args struct{ Height int }
		if env.Notify == nil || env.Notify.Method != "height_change" || json.Unmarshal(env.Notify.Args, &args) != nil {
			t.Fatalf("unexpected frame %+v", env)
		}
		heights = append(heights, args.Height)
	}
	return heights
}
