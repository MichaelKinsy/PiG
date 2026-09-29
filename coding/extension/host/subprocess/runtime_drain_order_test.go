package subprocess

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"net"
	"slices"
	"sync"
	"testing"
	"testing/synctest"
)

// A Node runtime reports runtime_drained after the calls it sent earlier. Pi
// writes an RPC dialog request synchronously before its event loop can drain
// (rpc-mode.ts createDialogPromise), so the host must apply those calls'
// synchronous parts before the owner's drain handler exits the process.
func TestRuntimeDrainWaitsForEarlierCallInitiation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := NewHostWithConfigRoot(t.TempDir(), t.TempDir())
		var mu sync.Mutex
		var order []string
		record := func(step string) {
			mu.Lock()
			order = append(order, step)
			mu.Unlock()
		}
		entered := make(chan struct{})
		release := make(chan struct{})
		h.SetCallHandler(func(_ string, call *CallPayload) (*CallResultPayload, error) {
			if call.Method == "ui.select" {
				close(entered)
				<-release
				record("dialog written")
			}
			return &CallResultPayload{}, nil
		})
		h.SetRuntimeDrainHandler(func() { record("drained") })
		hostSide, extSide := net.Pipe()
		ctx, cancel := context.WithCancel(t.Context())
		conn := NewConn("drain", hostSide)
		conn.Start(ctx)
		me := withConn(&managedExt{config: ExtConfig{Name: "drain"}, host: h}, conn)
		me.shuttingDown.Store(true)
		incomingDone := make(chan struct{})
		go func() {
			defer close(incomingDone)
			h.handleIncoming(me, conn)
		}()
		go func() {
			buf := make([]byte, 1<<16)
			for {
				if _, err := extSide.Read(buf); err != nil {
					return
				}
			}
		}()
		write := func(env Envelope) {
			t.Helper()
			data, err := json.Marshal(env)
			if err != nil {
				t.Fatal(err)
			}
			var length [4]byte
			binary.BigEndian.PutUint32(length[:], uint32(len(data)))
			if _, err := extSide.Write(append(length[:], data...)); err != nil {
				t.Fatal(err)
			}
		}
		write(Envelope{Type: MsgCall, ID: "select", Call: &CallPayload{Method: "ui.select", Args: json.RawMessage(`{"title":"Shutdown dialog","options":["keep"]}`)}})
		<-entered
		write(Envelope{Type: MsgNotify, Notify: &NotifyPayload{Method: "runtime_drained"}})
		synctest.Wait()
		mu.Lock()
		early := slices.Clone(order)
		mu.Unlock()
		if len(early) != 0 {
			t.Fatalf("drain handled before the earlier dialog call applied: %v", early)
		}
		close(release)
		synctest.Wait()
		mu.Lock()
		got := slices.Clone(order)
		mu.Unlock()
		if want := []string{"dialog written", "drained"}; !slices.Equal(got, want) {
			t.Fatalf("order = %v, want %v", got, want)
		}
		cancel()
		_ = extSide.Close()
		<-conn.Done()
		<-incomingDone
	})
}
