package subprocess

import (
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

// TestConn_AcceptsFrameAboveLegacyCap locks the frame-size fix: a frame larger
// than the old 16 MB cap (which silently disabled extensions like context-info
// when getBranch on a long session exceeded it) must now be accepted, up to
// MaxFrameSize. Regression for the 16 MB → 128 MB bump.
func TestConn_AcceptsFrameAboveLegacyCap(t *testing.T) {
	hostEnd, extEnd := net.Pipe()
	c := NewConn("test", hostEnd)
	ctx := t.Context()
	c.Start(ctx)

	// ~20 MB call frame: above the legacy 16 MB cap, below the 128 MB cap.
	const legacyCap = 16 * 1024 * 1024
	bigArgs, _ := json.Marshal(strings.Repeat("x", 20*1024*1024))
	frame, _ := json.Marshal(&Envelope{
		Type: MsgCall,
		ID:   "1",
		Call: &CallPayload{Method: "noop", Args: bigArgs},
	})
	if len(frame) <= legacyCap {
		t.Fatalf("test frame %d bytes is not above the legacy cap %d", len(frame), legacyCap)
	}
	if len(frame) > MaxFrameSize {
		t.Fatalf("test frame %d bytes exceeds MaxFrameSize %d", len(frame), MaxFrameSize)
	}

	go func() {
		var lenBuf [4]byte
		binary.BigEndian.PutUint32(lenBuf[:], uint32(len(frame)))
		_, _ = extEnd.Write(lenBuf[:])
		_, _ = extEnd.Write(frame)
	}()

	select {
	case env, ok := <-c.inCh:
		if !ok {
			t.Fatal("readLoop closed inCh: frame above the legacy 16 MB cap was rejected")
		}
		if env.Type != MsgCall || env.Call == nil || env.Call.Method != "noop" {
			t.Fatalf("decoded frame mismatch: %+v", env)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for the large frame: it was rejected or not dispatched")
	}
}

func TestConnPeerFailureClosesTransportLifetime(t *testing.T) {
	hostEnd, extEnd := net.Pipe()
	conn := NewConn("focused", hostEnd)
	conn.Start(t.Context())
	_ = extEnd.Close()
	select {
	case <-conn.Done():
	case <-time.After(time.Second):
		t.Fatal("connection lifetime remained blocked after writer failure")
	}
}

func TestConnCloseSendsShutdownBarrier(t *testing.T) {
	hostEnd, extEnd := net.Pipe()
	conn := NewConn("focused", hostEnd)
	conn.Start(t.Context())
	read := make(chan Envelope, 1)
	go func() {
		defer func() { _ = extEnd.Close() }()
		var header [4]byte
		if _, err := io.ReadFull(extEnd, header[:]); err != nil {
			return
		}
		payload := make([]byte, binary.BigEndian.Uint32(header[:]))
		if _, err := io.ReadFull(extEnd, payload); err != nil {
			return
		}
		var envelope Envelope
		if json.Unmarshal(payload, &envelope) == nil {
			read <- envelope
		}
	}()
	closed := make(chan error, 1)
	go func() { closed <- conn.Close("reload") }()
	select {
	case envelope := <-read:
		if envelope.Type != MsgShutdown || envelope.Shutdown == nil || envelope.Shutdown.Reason != "reload" {
			t.Fatalf("shutdown envelope = %+v", envelope)
		}
	case <-time.After(time.Second):
		t.Fatal("Close did not send the shutdown barrier")
	}
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("Close did not finish")
	}
}
