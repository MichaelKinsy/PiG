package subprocess

import (
	"bytes"
	"context"
	"errors"
	"runtime"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
	"github.com/MichaelKinsy/PiG/tui"
)

// anOwner is a started owner of a screen with no extension behind it.
func anOwner(t *testing.T) (*tuiAltOwner, *tui.TuiAltScreen) {
	t.Helper()
	var out bytes.Buffer
	screen := tui.NewTuiAltScreenWithOutput(&out, 20, 4, tui.TuiAltScreenOptions{})
	//portlint:allow ctxbackground the test ends the owner itself (the cleanup cancels this context)
	ctx, end := context.WithCancel(context.Background())
	owner := newTuiAltOwner(ctx, end, nil, screen)
	owner.start()
	t.Cleanup(func() {
		end()
		owner.wait()
	})
	return owner, screen
}

// The owner runs one task at a time, in the order they were queued, from whatever goroutines queued them: tasks that touch one plain variable do not race it
// (go test -race), and a task never starts before the one before it returned.
func TestTheOwnerRunsItsTasksOneAtATimeInOrder(t *testing.T) {
	owner, _ := anOwner(t)
	var running, last int
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 50 {
				if err := owner.do(func() {
					running++
					if running != 1 {
						t.Error("two tasks of the owner ran at once")
					}
					last++
					running--
				}); err != nil {
					t.Error(err)
				}
			}
		})
	}
	wg.Wait()
	var order []int
	for i := range 20 {
		owner.post(func() { order = append(order, i) })
	}
	if err := owner.do(func() {}); err != nil {
		t.Fatal(err)
	}
	want := make([]int, 20)
	for i := range want {
		want[i] = i
	}
	if !slices.Equal(order, want) {
		t.Errorf("tasks ran in order %v, want %v", order, want)
	}
	if last != 400 {
		t.Errorf("%d of 400 tasks ran", last)
	}
}

// A call from inside a task runs at once, as Pi's one thread runs a call made from a macrotask; it does not queue behind itself.
func TestACallFromTheOwnerRunsAtOnce(t *testing.T) {
	owner, _ := anOwner(t)
	var ran bool
	returnsWithinBudget(t, "a call made from the owner", func() {
		_ = owner.do(func() {
			if err := owner.do(func() { ran = true }); err != nil {
				t.Error(err)
			}
			if !ran {
				t.Error("the call made from the owner did not run before its caller went on")
			}
		})
	})
}

// A call to an owner that has returned fails and does not wait; a task queued behind a task that ends the owner is dropped, and close ends the owner only after
// the task that asked.
func TestACallToAnOwnerThatEndedFailsAndCloseEndsAfterItsTask(t *testing.T) {
	owner, _ := anOwner(t)
	var afterClose, dropped bool
	if err := owner.do(func() {
		owner.close()
		afterClose = true
		owner.post(func() { dropped = true })
	}); err != nil {
		t.Fatal(err)
	}
	owner.wait()
	if !afterClose {
		t.Error("close ended the task that asked before it returned")
	}
	if dropped {
		t.Error("a task queued after close ran")
	}
	if err := owner.do(func() { t.Error("a task ran on an owner that ended") }); !errors.Is(err, errTuiAltScreenEnded) {
		t.Errorf("a call to an owner that ended: err = %v, want %v", err, errTuiAltScreenEnded)
	}
	if owner.post(func() {}) {
		t.Error("an owner that ended took a task")
	}
}

// The throttled frame of the screen runs on the owner: Pi's requestRender arms a timer on its event loop, so the frame is a task of that loop and not of a
// goroutine the renderer's timer starts.
func TestAScheduledFrameRunsOnTheOwner(t *testing.T) {
	owner, screen := anOwner(t)
	onOwner := make(chan bool, 4)
	screen.Add(&frameProbePart{render: func(int) []string {
		onOwner <- owner.onOwner()
		return []string{"row"}
	}})
	if err := owner.do(func() {
		screen.Start()
		screen.RequestRender()
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-onOwner:
		if !got {
			t.Error("a scheduled frame rendered on a goroutine that is not the owner")
		}
	case <-time.After(testbudget.Wait(t)):
		t.Fatal("the scheduled frame did not run")
	}
}

// Pi's handleInput(data) { ...; tui.requestRender() } asks for a frame while it runs, and the frame runs after the handler returns, never beside it. The call the
// extension's code makes from inside the callback names the callback as its parent, runs at once (it does not wait for the handler it belongs to), and leaves the
// frame pending on the owner, which waits for the handler's answer.
func TestAnInputHandlersRenderRequestRunsAfterTheHandlerAnswers(t *testing.T) {
	b, conn, peer := anOrderedScreen(t, "s")
	s, err := b.tuiAltScreen(conn, "s")
	if err != nil {
		t.Fatal(err)
	}
	seen := len(peer.snapshot())
	peer.parkNext("s", "input")
	inputDone := make(chan error, 1)
	go func() {
		_, err := b.handleTuiAlt(conn, "", json.RawMessage(`{"op":"input","id":"s","data":"x"}`))
		inputDone <- err
	}()
	var parked parkedCallback
	select {
	case parked = <-peer.parked:
	case <-time.After(testbudget.Wait(t)):
		t.Fatal("the extension was never sent the input callback")
	}
	returnsWithinBudget(t, "a call made from an input callback", func() {
		if _, err := b.handleTuiAlt(conn, parked.request, json.RawMessage(`{"op":"requestRender","id":"s"}`)); err != nil {
			t.Error(err)
		}
	})
	s.owner.mu.Lock()
	pending := !s.owner.due.IsZero()
	s.owner.mu.Unlock()
	if !pending {
		t.Error("the frame the handler asked for is not pending on the screen's owner")
	}
	peer.releaseAll()
	select {
	case err := <-inputDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(testbudget.Wait(t)):
		t.Fatal("the input call did not return once the extension answered")
	}
	want := []string{"s input begin", "s input end", "s render begin", "s render end"}
	deadline := time.Now().Add(testbudget.Wait(t))
	var got []string
	for time.Now().Before(deadline) {
		if got = peer.snapshot()[seen:]; len(got) >= len(want) {
			break
		}
		runtime.Gosched()
	}
	if !slices.Equal(got, want) {
		t.Errorf("the extension saw %q, want %q", got, want)
	}
	select {
	case <-peer.overlap:
		t.Error("the extension was sent the frame while it was still answering the input")
	default:
	}
}
