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
	"time"
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
		h.exts = map[string]*managedExt{"drain": me}
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

// A Node process reports its drain on each connection it hosts. The host acts on it after every connection it holds for the process reported or closed, because a connection's earlier frames, such as a command's response, are ordered with its own report only. The host counts the connections itself, at the first report.
func TestRuntimeDrainWaitsForEveryConnectionTheHostHolds(t *testing.T) {
	newProcess := func(t *testing.T, names ...string) (*Host, []*managedExt, []*Conn) {
		h := NewHostWithConfigRoot(t.TempDir(), t.TempDir())
		process := &packedProcessState{node: true}
		h.exts = map[string]*managedExt{}
		var members []*managedExt
		var conns []*Conn
		for _, name := range names {
			conn := &Conn{}
			me := withConn(&managedExt{config: ExtConfig{Name: name}, host: h, packedProcess: process}, conn)
			h.exts[name] = me
			members = append(members, me)
			conns = append(conns, conn)
		}
		return h, members, conns
	}

	t.Run("every connection reports", func(t *testing.T) {
		h, m, c := newProcess(t, "first", "second")
		if h.noteRuntimeDrain(m[0], c[0], notifyRuntimeDrained, true) {
			t.Fatal("drain acted on after one of two connections reported")
		}
		if h.noteRuntimeDrain(m[0], c[0], notifyRuntimeDrained, true) {
			t.Fatal("a repeated report on one connection completed the drain")
		}
		if !h.noteRuntimeDrain(m[1], c[1], notifyRuntimeDrained, true) {
			t.Fatal("drain not acted on after both connections reported")
		}
		if h.noteRuntimeDrain(m[0], c[0], notifyRuntimeDrained, true) {
			t.Fatal("drain state survived the completed drain")
		}
	})

	t.Run("another process does not wait for this one", func(t *testing.T) {
		h, m, c := newProcess(t, "first", "second")
		other := withConn(&managedExt{config: ExtConfig{Name: "other"}, host: h}, &Conn{})
		h.exts["other"] = other
		if !h.noteRuntimeDrain(other, other.connection(), notifyRuntimeDrained, true) {
			t.Fatal("another process's drain waited for this process's connections")
		}
		if h.noteRuntimeDrain(m[0], c[0], notifyRuntimeDrained, true) {
			t.Fatal("drain acted on after one of two connections reported")
		}
	})

	t.Run("a connection that closes after the first report", func(t *testing.T) {
		h, m, c := newProcess(t, "first", "second")
		h.commandFlightMu.Lock()
		h.inputEnded = true
		h.commandFlightMu.Unlock()
		if h.noteRuntimeDrain(m[0], c[0], notifyRuntimeDrained, true) {
			t.Fatal("drain acted on before the second connection reported or closed")
		}
		if len(h.noteRuntimeConnClosed(m[1], c[1])) == 0 {
			t.Fatal("the close of the connection that had not reported did not complete the drain")
		}
		if len(h.noteRuntimeConnClosed(m[1], c[1])) > 0 {
			t.Fatal("a close after the completed drain reported another drain")
		}
	})

	// The host closes a member (reload, retirement, quarantine) before any report, and Node still counted that connection.
	t.Run("a host-side close before the first report", func(t *testing.T) {
		h, m, c := newProcess(t, "first", "second")
		h.commandFlightMu.Lock()
		h.inputEnded = true
		h.commandFlightMu.Unlock()
		if len(h.noteRuntimeConnClosed(m[1], c[1])) > 0 {
			t.Fatal("a close without a drain in progress completed a drain")
		}
		if !h.noteRuntimeDrain(m[0], c[0], notifyRuntimeDrained, true) {
			t.Fatal("the drain waited for a connection that closed before the first report")
		}
	})

	t.Run("a connection already closed at the first report", func(t *testing.T) {
		h, m, c := newProcess(t, "first", "second")
		c[1].closed.Store(true)
		if !h.noteRuntimeDrain(m[0], c[0], notifyRuntimeDrained, true) {
			t.Fatal("the drain waited for a connection the host had closed")
		}
	})

	// After a reload the host drops frames of a generation it no longer accepts, but the drain notification still orders that connection's earlier responses.
	t.Run("a stale generation's report", func(t *testing.T) {
		h, m, c := newProcess(t, "first", "second")
		if h.noteRuntimeDrain(m[1], c[1], notifyRuntimeDrained, false) {
			t.Fatal("a report of a stale generation alone triggered the drain")
		}
		if !h.noteRuntimeDrain(m[0], c[0], notifyRuntimeDrained, true) {
			t.Fatal("the stale generation's report was not counted as delivered")
		}
	})

	t.Run("a wholly stale process", func(t *testing.T) {
		h, m, c := newProcess(t, "first", "second")
		if h.noteRuntimeDrain(m[0], c[0], notifyRuntimeDrained, false) || h.noteRuntimeDrain(m[1], c[1], notifyRuntimeDrained, false) {
			t.Fatal("a process whose every generation is stale triggered the drain")
		}
	})
}

// A generation the host no longer accepts still sent its earlier calls first, and the report that follows them counts as delivered only after those calls applied their synchronous parts, as on the accepted path.
func TestStaleRuntimeDrainWaitsForEarlierCallInitiation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := NewHostWithConfigRoot(t.TempDir(), t.TempDir())
		entered := make(chan struct{})
		release := make(chan struct{})
		h.SetCallHandler(func(_ string, call *CallPayload) (*CallResultPayload, error) {
			if call.Method == "ui.select" {
				close(entered)
				<-release
			}
			return &CallResultPayload{}, nil
		})
		hostSide, extSide := net.Pipe()
		ctx, cancel := context.WithCancel(t.Context())
		conn := NewConn("stale", hostSide)
		conn.Start(ctx)
		process := &packedProcessState{node: true}
		first := withConn(&managedExt{config: ExtConfig{Name: "first"}, host: h, packedProcess: process}, conn)
		first.shuttingDown.Store(true)
		second := withConn(&managedExt{config: ExtConfig{Name: "second"}, host: h, packedProcess: process}, &Conn{})
		h.exts = map[string]*managedExt{"first": first, "second": second}
		incomingDone := make(chan struct{})
		go func() {
			defer close(incomingDone)
			h.handleIncoming(first, conn)
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
		write(Envelope{Type: MsgCall, ID: "select", Call: &CallPayload{Method: "ui.select", Args: json.RawMessage(`{"title":"Ask","options":["keep"]}`)}})
		<-entered
		process.stopping.Store(true)
		write(Envelope{Type: MsgNotify, Notify: &NotifyPayload{Method: notifyRuntimeDrained}})
		synctest.Wait()
		reported := func() bool {
			h.mu.Lock()
			defer h.mu.Unlock()
			return len(h.runtimeDrains) > 0
		}
		if reported() {
			t.Fatal("a stale generation's report counted before its earlier call applied")
		}
		close(release)
		synctest.Wait()
		if !reported() {
			t.Fatal("the stale generation's report never counted")
		}
		cancel()
		_ = extSide.Close()
		<-conn.Done()
		<-incomingDone
	})
}

// Shutdown joins the task that finishes a drain after a connection closed, and a task that starts once Shutdown waits does not run.
func TestShutdownJoinsDrainTasks(t *testing.T) {
	h := NewHost(t.TempDir())
	release := make(chan struct{})
	ran := make(chan struct{})
	h.goDrainTask(func() {
		<-release
		close(ran)
	})
	done := make(chan struct{})
	go func() {
		h.Shutdown("test done")
		close(done)
	}()
	select {
	case <-done:
		t.Fatal("Shutdown returned before the drain task finished")
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	<-done
	<-ran
	started := false
	h.goDrainTask(func() { started = true })
	if started {
		t.Fatal("a drain task started after Shutdown")
	}
}
