package subprocess

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/goroutineid"
)

// A TuiAltScreen frame's goroutine sends a component's render and waits for it holding the frame lock; the calls the extension's render makes to the screen
// must run on that goroutine, in the wait, since no other goroutine can take the lock. withRequestInterjections hands them to it.
func TestRequestInterjectionsRunOnTheWaitingGoroutine(t *testing.T) {
	hostSide, extSide := net.Pipe()
	conn := NewConn("interject", hostSide)
	conn.Start(t.Context())
	go func() { _, _ = io.Copy(io.Discard, extSide) }()
	defer func() {
		_ = conn.Close("test done")
		_ = extSide.Close()
	}()

	runs := make(chan func())
	waiter := make(chan uint64, 1)
	done := make(chan error, 1)
	go func() {
		waiter <- goroutineid.Current()
		ctx := withRequestInterjections(context.WithoutCancel(t.Context()), runs)
		_, err := conn.Request(ctx, &Envelope{ID: "r1", Type: MsgRequest, Request: &RequestPayload{Method: "x"}})
		done <- err
	}()
	want := <-waiter
	for range 3 {
		ran := make(chan uint64, 1)
		select {
		case runs <- func() { ran <- goroutineid.Current() }:
		case <-time.After(10 * time.Second):
			t.Fatal("the request did not take the call while it waited for its response")
		}
		if got := <-ran; got != want {
			t.Fatalf("the call ran on goroutine %d, want the one that waits for the response, %d", got, want)
		}
	}
	select {
	case err := <-done:
		t.Fatalf("the request returned before its response: %v", err)
	default:
	}

	data, err := json.Marshal(Envelope{Type: MsgResponse, ID: "r1", Response: &ResponsePayload{}})
	if err != nil {
		t.Fatal(err)
	}
	var length [4]byte
	binary.BigEndian.PutUint32(length[:], uint32(len(data)))
	if _, err := extSide.Write(append(length[:], data...)); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("the request failed: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the request did not return its response")
	}
}
