package daemon

// pi: packages/env/daemon/src/output.rs

// Ports the rules of packages/env/daemon/src/output.rs: control frames go before bulk output, and unwindowed output waits
// while more than the bulk limit is unsent.

import (
	"bytes"
	"io"
	"testing"
	"time"
)

func TestControlFramesGoBeforeBulkOutput(t *testing.T) {
	out := newOutput()
	for index := range 3 {
		out.sendBulk(&Frame{Kind: FrameEvent, ID: uint32(index), JSON: Object{}, Payload: []byte("bulk")}, nil)
	}
	out.sendControl(&Frame{Kind: FrameResult, ID: 99, JSON: Object{}})
	var written bytes.Buffer
	done := make(chan struct{})
	go func() {
		out.run(&written)
		close(done)
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		out.mu.Lock()
		empty := len(out.control) == 0 && len(out.bulk) == 0
		out.mu.Unlock()
		if empty {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the queues were not written")
		}
		time.Sleep(time.Millisecond)
	}
	out.close()
	<-done
	reader := bytes.NewReader(written.Bytes())
	var ids []uint32
	for {
		frame, err := ReadFrame(reader)
		if err != nil || frame == nil {
			break
		}
		ids = append(ids, frame.ID)
	}
	if len(ids) != 4 || ids[0] != 99 {
		t.Fatalf("frame order %v, want the result (99) first", ids)
	}
}

func TestUnwindowedOutputWaitsWhileTooMuchIsUnsent(t *testing.T) {
	out := newOutput()
	out.sendBulk(&Frame{Kind: FrameEvent, JSON: Object{}, Payload: make([]byte, bulkLimit+1)}, nil)
	stop := make(chan struct{})
	waited := make(chan struct{})
	go func() {
		out.waitForRoom(stop)
		close(waited)
	}()
	select {
	case <-waited:
		t.Fatal("a reader did not wait with more than the bulk limit unsent")
	case <-time.After(150 * time.Millisecond):
	}
	close(stop)
	select {
	case <-waited:
	case <-time.After(5 * time.Second):
		t.Fatal("a reader did not stop waiting when told to")
	}
	// Writing the bytes out makes room.
	drained := make(chan struct{})
	go func() {
		out.waitForRoom(make(chan struct{}))
		close(drained)
	}()
	go out.run(io.Discard)
	select {
	case <-drained:
	case <-time.After(5 * time.Second):
		t.Fatal("a reader kept waiting after the bytes were written")
	}
	out.close()
}
