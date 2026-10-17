package daemon

import (
	"sync/atomic"
	"testing"
	"time"
)

// In Pi's Rust daemon each frame worker and each exec or watch request runs on its own thread (daemon/src/main.rs:334-341,
// :401-403), and a panic there unwinds that thread only: the request is never answered and the daemon keeps serving. A
// panic in a Go goroutine with no recover ends the whole process, so without recoverThreadPanic this test binary dies.
// mutation-checked: dropping either `defer recoverThreadPanic()` in serveFrames crashes the test binary.
func TestServeOutlivesAPanickingRequestAsTheDaemonsThreadsDo(t *testing.T) {
	var panics atomic.Int32
	hook := func(op string) {
		if op == "lstat" || op == "exec" {
			if panics.Add(1) <= 2 {
				panic("request bug")
			}
		}
	}
	respondHook.Store(&hook)
	t.Cleanup(func() { respondHook.Store(nil) })

	p := startServe(t, Options{})
	directory := t.TempDir()
	// One frame worker and one exec request panic; neither is answered.
	p.request(1, Object{"op": "lstat", "path": directory}, nil)
	p.request(2, Object{"op": "exec", "cwd": directory, "command": "true"}, nil)
	// Exec runs on its own goroutine, so a later lstat could reach the hook first and take the second panic; send the
	// rest only once both panics happened.
	for deadline := time.Now().Add(10 * time.Second); panics.Load() < 2; time.Sleep(time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("the first lstat and exec reached the hook %d times, want 2", panics.Load())
		}
	}
	// The remaining workers still answer, and so does the next exec.
	for id := uint32(3); id < 3+workers; id++ {
		p.request(id, Object{"op": "hello", "protocol": float64(1)}, nil)
	}
	p.request(100, Object{"op": "lstat", "path": directory}, nil)
	p.request(101, Object{"op": "exec", "cwd": directory, "command": "true"}, nil)
	answered := map[uint32]uint8{}
	for len(answered) < workers+2 {
		frame := p.next()
		if frame.Kind == FrameResult || frame.Kind == FrameError {
			answered[frame.ID] = frame.Kind
		}
	}
	for id := uint32(3); id < 3+workers; id++ {
		if answered[id] != FrameResult {
			t.Fatalf("hello %d: %v", id, answered)
		}
	}
	if answered[100] != FrameResult || answered[101] != FrameResult {
		t.Fatalf("requests after the panics: %v", answered)
	}
	if got := panics.Load(); got != 4 {
		t.Fatalf("hook ran %d times for lstat and exec, want 4", got)
	}
	// Nothing ever answers the two that panicked.
	deadline := time.After(200 * time.Millisecond)
	for draining := true; draining; {
		select {
		case frame, ok := <-p.frames:
			if ok && (frame.Kind == FrameResult || frame.Kind == FrameError) {
				answered[frame.ID] = frame.Kind
			}
			draining = ok
		case <-deadline:
			draining = false
		}
	}
	if _, ok := answered[1]; ok {
		t.Fatalf("the panicked lstat was answered: %v", answered)
	}
	if _, ok := answered[2]; ok {
		t.Fatalf("the panicked exec was answered: %v", answered)
	}
}
