package childwait

import (
	"os"
	"sync"
	"testing"
	"time"
)

func newPipePair(t *testing.T) (read, write *os.File) {
	t.Helper()
	r, w, err := NewPipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close(); _ = w.Close() })
	return r, w
}

// What the writer put into one pipe before it put anything into the next is delivered first, on every platform: the stdout-before-stderr order of Node's
// loop, which a byte cap that spans both streams makes observable. Writes land before the readers start, so the order is the writes' and nothing else's.
func TestPipesDeliverTheEarlierPipeFirstWhateverTheReadersScheduling(t *testing.T) {
	for round := range 300 {
		var files, writers []*os.File
		for range 2 {
			r, w := newPipePair(t)
			files, writers = append(files, r), append(writers, w)
		}
		var (
			mu    sync.Mutex
			order []int
		)
		_, _ = writers[0].WriteString("out")
		_, _ = writers[1].WriteString("err")
		pipes := Start(files, func(index int, _ []byte) {
			mu.Lock()
			order = append(order, index)
			mu.Unlock()
		}, nil)
		for _, w := range writers {
			_ = w.Close()
		}
		if !pipes.Wait(func() bool { return false }) {
			t.Fatalf("round %d: Wait did not report ended pipes", round)
		}
		mu.Lock()
		got := append([]int(nil), order...)
		mu.Unlock()
		if len(got) != 2 || got[0] != 0 || got[1] != 1 {
			t.Fatalf("round %d: delivery order %v, want [0 1]", round, got)
		}
	}
}

// pollReadable is told of data written while it waits, promptly and without a poll interval in between, and of the last write end closing.
func TestPollReadableWakesOnDataAndOnEnd(t *testing.T) {
	r, w := newPipePair(t)
	type answer struct{ readable, closed bool }
	got := make(chan answer, 1)
	go func() {
		readable, closed := pollReadable(r, 10_000)
		got <- answer{readable, closed}
	}()
	time.Sleep(50 * time.Millisecond)
	select {
	case a := <-got:
		t.Fatalf("pollReadable answered %+v with nothing in the pipe", a)
	default:
	}
	_, _ = w.WriteString("x")
	select {
	case a := <-got:
		if !a.readable || a.closed {
			t.Fatalf("pollReadable = %+v, want readable", a)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("pollReadable did not wake on data")
	}
	buf := make([]byte, 8)
	if n, err := r.Read(buf); err != nil || n != 1 {
		t.Fatalf("Read = %d, %v", n, err)
	}
	go func() {
		readable, closed := pollReadable(r, 10_000)
		got <- answer{readable, closed}
	}()
	time.Sleep(50 * time.Millisecond)
	_ = w.Close()
	select {
	case a := <-got:
		if !a.readable || a.closed {
			t.Fatalf("pollReadable after the write end closed = %+v, want readable", a)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("pollReadable did not wake when the write end closed")
	}
}

// Closing the file ends a wait for it, as Wait does when it releases a held pipe: Close itself, which waits for the wait to leave the handle, is timed.
// The wait is as long as awaitTurn's, so the test measures what Wait's release costs, not the wait's own timeout.
func TestPollReadableReturnsWhenTheFileIsClosed(t *testing.T) {
	const turn = 10 // awaitTurn's timeout in milliseconds
	r, _ := newPipePair(t)
	done := make(chan struct{})
	go func() {
		defer close(done)
		// Asked again as awaitTurn does, until the file says it is gone.
		for {
			if _, closed := pollReadable(r, turn); closed {
				return
			}
		}
	}()
	time.Sleep(50 * time.Millisecond)
	start := time.Now()
	closed := make(chan struct{})
	go func() { _ = r.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not return while a wait for the file was pending")
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("pollReadable did not return after the file was closed")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("closing a file with a pending wait took %v", elapsed)
	}
}
