package subprocess

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"net"
	"testing"
)

// echoPeer answers every request frame with an empty result by pasting the
// request's id into a canned response, so its own cost allocates nothing and a
// benchmark's allocations are the host connection's.
func echoPeer(tb testing.TB, peer net.Conn) {
	tb.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		var length [4]byte
		frame := make([]byte, 0, 4096)
		reply := make([]byte, 0, 256)
		for {
			if _, err := io.ReadFull(peer, length[:]); err != nil {
				return
			}
			n := int(binary.BigEndian.Uint32(length[:]))
			if cap(frame) < n {
				frame = make([]byte, n)
			}
			frame = frame[:n]
			if _, err := io.ReadFull(peer, frame); err != nil {
				return
			}
			if !bytes.Contains(frame, []byte(`"type":"request"`)) {
				continue
			}
			_, rest, ok := bytes.Cut(frame, []byte(`"id":"`))
			if !ok {
				continue
			}
			id, _, _ := bytes.Cut(rest, []byte(`"`))
			reply = append(reply[:4], `{"type":"response","id":"`...)
			reply = append(reply, id...)
			reply = append(reply, `","response":{"result":{}}}`...)
			binary.BigEndian.PutUint32(reply[:4], uint32(len(reply)-4))
			if _, err := peer.Write(reply); err != nil {
				return
			}
		}
	}()
	tb.Cleanup(func() { _ = peer.Close(); <-done })
}

// loopbackPair returns the two ends of a connected TCP loopback socket, the
// transport every platform has.
func loopbackPair(tb testing.TB) (host, peer net.Conn) {
	tb.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		tb.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	accepted := make(chan net.Conn, 1)
	go func() {
		c, _ := listener.Accept()
		accepted <- c
	}()
	host, err = net.Dial("tcp", listener.Addr().String())
	if err != nil {
		tb.Fatal(err)
	}
	return host, <-accepted
}

// BenchmarkConnRequestRoundTrip is the host side of one extension request: the
// envelope encode, the frame write, the response frame read and decode, and the
// request bookkeeping, against a peer that costs no allocation.
func BenchmarkConnRequestRoundTrip(b *testing.B) {
	for _, payload := range []struct {
		name string
		args string
	}{{"empty", ``}, {"1KiB", `"` + string(bytes.Repeat([]byte("x"), 1024)) + `"`}, {"64KiB", `"` + string(bytes.Repeat([]byte("x"), 64*1024)) + `"`}} {
		b.Run(payload.name, func(b *testing.B) {
			hostEnd, peerEnd := loopbackPair(b)
			echoPeer(b, peerEnd)
			conn := NewConn("bench", hostEnd)
			conn.Start(b.Context())
			b.Cleanup(func() { _ = conn.Close("bench done") })
			request := func() *Envelope {
				env := &Envelope{Type: MsgRequest, Request: &RequestPayload{Method: "command", Tool: "noop"}}
				if payload.args != "" {
					env.Request.Args = []byte(payload.args)
				}
				return env
			}
			ctx := context.Background()
			b.ReportAllocs()
			b.SetBytes(int64(len(payload.args)))
			for b.Loop() {
				if _, err := conn.Request(ctx, request()); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkConnNotify is the host side of a fire-and-forget frame (state
// update, event delivery): encode and queue, written by the writer goroutine.
func BenchmarkConnNotify(b *testing.B) {
	hostEnd, peerEnd := loopbackPair(b)
	go func() { _, _ = io.Copy(io.Discard, peerEnd) }()
	b.Cleanup(func() { _ = peerEnd.Close() })
	conn := NewConn("bench", hostEnd)
	conn.Start(b.Context())
	b.Cleanup(func() { _ = conn.Close("bench done") })
	env := &Envelope{Type: MsgNotify, Notify: &NotifyPayload{Method: "state_update", Args: []byte(`{"isIdle":true,"thinkingLevel":"high"}`)}}
	b.ReportAllocs()
	for b.Loop() {
		if err := conn.Send(env); err != nil {
			b.Fatal(err)
		}
	}
}
