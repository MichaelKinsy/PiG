package codingagent

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
	"github.com/MichaelKinsy/PiG/tui"
)

// overlayPeer is the extension end of a real subprocess.Conn. It answers
// heartbeats and reports each ui.custom.input notification the host sends.
type overlayPeer struct {
	inputs chan subprocess.RemoteOverlayInputPayload
}

func attachOverlayPeer(t *testing.T, name string) (*subprocess.Conn, *overlayPeer) {
	t.Helper()
	hostEnd, extEnd := net.Pipe()
	conn := subprocess.NewConn(name, hostEnd)
	ctx, cancel := context.WithCancel(context.Background())
	conn.Start(ctx)
	peer := &overlayPeer{inputs: make(chan subprocess.RemoteOverlayInputPayload, 8)}
	remote := &remoteInputExtension{peer: extEnd}
	go func() {
		for {
			env, err := readInputQueueFrame(extEnd)
			if err != nil {
				return
			}
			switch {
			case env.Type == subprocess.MsgPing && env.Ping != nil:
				remote.write(&subprocess.Envelope{Type: subprocess.MsgPong, Pong: &subprocess.PongPayload{Nonce: env.Ping.Nonce}})
			case env.Type == subprocess.MsgNotify && env.Notify != nil && env.Notify.Method == subprocess.NotifyUICustomInput:
				var input subprocess.RemoteOverlayInputPayload
				if json.Unmarshal(env.Notify.Args, &input) == nil {
					peer.inputs <- input
				}
			}
		}
	}()
	t.Cleanup(func() {
		cancel()
		_ = extEnd.Close()
		_ = hostEnd.Close()
	})
	return conn, peer
}

// A key chunk the input pump had already handed to the owner loop reaches the
// focused overlay's input handler on that loop. The bridge reads the mounted
// overlay's state before it forwards the key, and the read used to queue a task
// on the owner loop and wait for it, so the loop waited for work only it could
// run. Pi reads the same state synchronously on its single event loop.
// This drives the production queued dispatcher: a running input loop, uiTaskCh,
// and the subprocess bridge.
func TestRemoteOverlayKeyOnOwnerLoopDoesNotWaitOnOwnerQueue(t *testing.T) {
	q := newInputQueueMode(t)
	q.start(t)
	conn, peer := attachOverlayPeer(t, "atlas")
	bridge := subprocess.NewUIBridge(func() {})
	bridge.SetUIContext(&ExtUIContext{m: q.InteractiveMode})
	bridge.RegisterExtConn("atlas", conn)

	args, err := json.Marshal(subprocess.RemoteOverlayOpenPayload{Key: "atlas-1", Overlay: true})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	wg.Go(func() {
		_, _ = bridge.HandleCallFrom("atlas", conn, &subprocess.CallPayload{Method: subprocess.CallUICustom, Args: args})
	})
	var overlay *customOverlay
	deadline := time.Now().Add(testbudget.Wait(t))
	for overlay == nil {
		overlay, _ = runOnQueueLoop(t, q.InteractiveMode, func() tui.Component { return q.tuiInst.ActiveOverlay() }).(*customOverlay)
		if overlay == nil && time.Now().After(deadline) {
			t.Fatal("overlay never mounted")
		}
		time.Sleep(time.Millisecond)
	}
	t.Cleanup(func() {
		overlay.Close(nil) // releases HandleCallFrom even when the owner loop is stuck
		released := make(chan struct{})
		go func() { wg.Wait(); close(released) }()
		select {
		case <-released:
		case <-time.After(testbudget.Wait(t)):
			t.Error("ui.custom call did not return after its overlay closed")
		}
	})

	handled := make(chan error, 1)
	if !q.postUITask(func() { handled <- q.handleKey(q.runCtx, "x") }) {
		t.Fatal("owner loop task queue is full")
	}
	select {
	case err := <-handled:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(testbudget.Wait(t)):
		t.Fatal("the owner loop deadlocked delivering a key to a focused remote overlay")
	}
	select {
	case input := <-peer.inputs:
		if input.Key != "atlas-1" || input.Data != "x" || input.State == nil || !input.State.Focused || !input.State.Visible || input.State.Hidden {
			t.Fatalf("forwarded input = %+v state=%+v", input, input.State)
		}
	case <-time.After(testbudget.Wait(t)):
		t.Fatal("the extension never received the key")
	}
}

// The owner loop is recognised by goroutine: a caller on it runs owner work
// inline, every other goroutine and a mode with no running loop queue.
func TestOwnerLoopIdentity(t *testing.T) {
	q := newInputQueueMode(t)
	if q.onOwnerLoop() {
		t.Fatal("a mode with no running loop claims the test goroutine is its owner")
	}
	q.start(t)
	if q.onOwnerLoop() {
		t.Fatal("a goroutine outside the loop is the owner")
	}
	if !runOnQueueLoop(t, q.InteractiveMode, q.onOwnerLoop) {
		t.Fatal("a task running on the input loop is not on the owner loop")
	}
	other := make(chan bool, 1)
	go func() { other <- q.onOwnerLoop() }()
	if <-other {
		t.Fatal("a worker goroutine is the owner")
	}
}

// A nested input loop runs on the owner goroutine and must neither lose the
// owner when it starts nor leave it recorded after it returns.
func TestNestedOwnerLoopRestoresOwner(t *testing.T) {
	m := newInputQueueMode(t).InteractiveMode
	leaveOuter := m.enterOwnerLoop()
	outer := m.ownerGoroutine.Load()
	leaveInner := m.enterOwnerLoop()
	if m.ownerGoroutine.Load() != outer || !m.onOwnerLoop() {
		t.Fatal("nested loop changed the owner")
	}
	leaveInner()
	if !m.onOwnerLoop() {
		t.Fatal("leaving the nested loop dropped the owner")
	}
	leaveOuter()
	if m.onOwnerLoop() || m.ownerGoroutine.Load() != 0 {
		t.Fatal("owner survived its loop")
	}
}

// Waiting for an owner task from the owner loop must run it inline: the queue
// is served only by the goroutine that waits.
func TestRunOnMainAndWaitOnOwnerLoopRunsInline(t *testing.T) {
	q := newInputQueueMode(t)
	q.start(t)
	type outcome struct {
		ran       bool
		err       error
		cancelled error
	}
	got := runOnQueueLoop(t, q.InteractiveMode, func() outcome {
		var o outcome
		o.err = q.runOnMainAndWait(q.runCtx, func() error { o.ran = true; return nil })
		cancelled, cancel := context.WithCancel(q.runCtx)
		cancel()
		o.cancelled = q.runOnMainAndWait(cancelled, func() error { t.Error("work ran for a cancelled context"); return nil })
		return o
	})
	if !got.ran || got.err != nil || !errors.Is(got.cancelled, context.Canceled) {
		t.Fatalf("inline wait = %+v", got)
	}
}

// The owner loop posting to its own full queue cannot wait for room, because
// only that goroutine drains it, and must not run the task inline either:
// upstream runs a posted callback after the current task and after every
// callback queued before it, and runOnMain promises a caller's posts run in
// order. With room the task queues; when the queue is full it still runs after
// the tasks already queued, and later owner posts stay behind it.
func TestRunOnMainFromOwnerLoopNeverBlocksOnItsOwnQueue(t *testing.T) {
	q := newInputQueueMode(t)
	q.start(t)
	type posted struct {
		ranDuringTask []string
		fillers       int
	}
	var order []string // appended only on the owner loop
	done := make(chan struct{})
	got := runOnQueueLoop(t, q.InteractiveMode, func() posted {
		q.runOnMain(q.runCtx, func() { order = append(order, "first") })
		fillers := 0
		for len(q.uiTaskCh) < cap(q.uiTaskCh) {
			q.uiTaskCh <- func() { order = append(order, "queued") }
			fillers++
		}
		q.runOnMain(q.runCtx, func() { order = append(order, "overflow-1") })
		q.runOnMain(q.runCtx, func() { order = append(order, "overflow-2") })
		// The queue may have room again by now; this post must still wait behind the overflow.
		q.runOnMain(q.runCtx, func() { order = append(order, "after-overflow"); close(done) })
		return posted{ranDuringTask: slices.Clone(order), fillers: fillers}
	})
	if len(got.ranDuringTask) != 0 {
		t.Fatalf("posted tasks ran inside the posting task: %v", got.ranDuringTask)
	}
	select {
	case <-done:
	case <-time.After(testbudget.Wait(t)):
		t.Fatal("the owner loop never ran the tasks it posted to its own full queue")
	}
	want := []string{"first"}
	for range got.fillers {
		want = append(want, "queued")
	}
	want = append(want, "overflow-1", "overflow-2", "after-overflow")
	if !slices.Equal(order, want) {
		t.Fatalf("order on the loop = %v, want %v", order, want)
	}
	if q.ownerOverflowActive.Load() {
		t.Fatal("owner overflow still marked active after it drained")
	}
}

// A post the queue accepts at once does not read the goroutine id: that read
// walks the caller's stack and allocates, and every streamed update posts.
func TestPostToMainWithRoomSkipsOwnerCheck(t *testing.T) {
	m := newInputQueueMode(t).InteractiveMode
	leave := m.enterOwnerLoop()
	defer leave()
	fn := func() {}
	allocs := testing.AllocsPerRun(100, func() {
		if err := m.postToMain(m.runCtx, fn); err != nil {
			t.Fatal(err)
		}
		<-m.uiTaskCh
	})
	if allocs != 0 {
		t.Fatalf("postToMain with room allocated %v times per post", allocs)
	}
}

// runOnOwner from the owner loop runs inline, so a result channel read
// straight after it cannot wait on the queue.
func TestRunOnOwnerInlineOnLoopQueuedElsewhere(t *testing.T) {
	q := newInputQueueMode(t)
	q.start(t)
	inline := runOnQueueLoop(t, q.InteractiveMode, func() bool {
		ran := false
		q.runOnOwner(q.runCtx, func() { ran = true })
		return ran
	})
	if !inline {
		t.Fatal("runOnOwner on the owner loop did not run inline")
	}
	done := make(chan bool, 1)
	q.runOnOwner(q.runCtx, func() { done <- q.onOwnerLoop() })
	if onLoop := <-done; !onLoop {
		t.Fatal("runOnOwner from another goroutine did not run on the owner loop")
	}
}
