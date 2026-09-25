package subprocess

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

func TestFrameTooLargeError_Message(t *testing.T) {
	e := &FrameTooLargeError{Size: 200, Max: 128}
	if got := e.Error(); !strings.Contains(got, "200") || !strings.Contains(got, "128") {
		t.Fatalf("error should name both sizes: %q", got)
	}
}

// TestConn_Send_RejectsOversizedFrame proves the host refuses to write a frame
// no compliant extension could read, returning a typed error instead of
// queueing an undeliverable frame.
func TestConn_Send_RejectsOversizedFrame(t *testing.T) {
	hostEnd, extEnd := net.Pipe()
	c := NewConn("t", hostEnd)
	c.Start(t.Context())
	go func() { _, _ = io.Copy(io.Discard, extEnd) }()

	// A result payload that pushes the marshaled envelope above MaxFrameSize.
	big, _ := json.Marshal(strings.Repeat("x", MaxFrameSize))
	err := c.Send(&Envelope{Type: MsgCallResult, ID: "1", CallResult: &CallResultPayload{Result: big}})
	var tooLarge *FrameTooLargeError
	if !errors.As(err, &tooLarge) {
		t.Fatalf("oversized send should return *FrameTooLargeError, got %v", err)
	}
	if tooLarge.Size <= MaxFrameSize {
		t.Fatalf("reported size %d should exceed MaxFrameSize %d", tooLarge.Size, MaxFrameSize)
	}

	// A normal frame still sends.
	if err := c.Send(&Envelope{Type: MsgNotify, Notify: &NotifyPayload{}}); err != nil {
		t.Fatalf("small send should succeed, got %v", err)
	}
}

// TestHandleIncoming_OversizedResultReturnsError drives the production call
// path: when a call result is too large for the IPC frame limit, the extension
// must receive a clean error result rather than nothing (a silent death).
func TestHandleIncoming_OversizedResultReturnsError(t *testing.T) {
	hostEnd, extEnd := net.Pipe()
	c := NewConn("ctx", hostEnd)
	c.Start(t.Context())

	h := NewHost(t.TempDir())
	h.SetCallHandler(func(extName string, call *CallPayload) (*CallResultPayload, error) {
		big, _ := json.Marshal(strings.Repeat("x", MaxFrameSize))
		return &CallResultPayload{Result: big}, nil
	})
	me := &managedExt{
		config:     ExtConfig{Name: "ctx"},
		host:       h,
		supervisor: NewSupervisor(DefaultSupervisorConfig()),
		conn:       c,
	}
	go h.handleIncoming(me)

	// Extension issues a call that yields an oversized result.
	callFrame, _ := json.Marshal(&Envelope{Type: MsgCall, ID: "1", Call: &CallPayload{Method: "getBranch"}})
	go func() {
		var lb [4]byte
		binary.BigEndian.PutUint32(lb[:], uint32(len(callFrame)))
		_, _ = extEnd.Write(lb[:])
		_, _ = extEnd.Write(callFrame)
	}()

	// Read the host's response frame.
	_ = extEnd.SetReadDeadline(time.Now().Add(15 * time.Second))
	var lb [4]byte
	if _, err := io.ReadFull(extEnd, lb[:]); err != nil {
		t.Fatalf("read response length: %v", err)
	}
	n := binary.BigEndian.Uint32(lb[:])
	if n > MaxFrameSize {
		t.Fatalf("error response frame %d should be small, not oversized", n)
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(extEnd, buf); err != nil {
		t.Fatalf("read response frame: %v", err)
	}
	var resp Envelope
	if err := json.Unmarshal(buf, &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Type != MsgCallResult || resp.CallResult == nil || resp.CallResult.Error == nil {
		t.Fatalf("expected an error call_result, got %+v", resp)
	}
	if resp.CallResult.Error.Code != "result_too_large" {
		t.Fatalf("expected result_too_large, got %q (%s)", resp.CallResult.Error.Code, resp.CallResult.Error.Message)
	}
}
