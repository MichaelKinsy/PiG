//go:build !windows

package interop

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/chord"
	"github.com/MichaelKinsy/PiG/internal/experimental/protocol"
)

// Ports packages/server/test/protocol.test.ts and packages/server/src/server.ts handshake and failure behavior as a raw-byte
// differential: the pinned Node server and the Go server receive the same bytes and must answer with the same bytes and
// close the connection the same way.

type wireCase struct {
	name   string
	writes [][]byte
	// unordered compares the replies as a set: concurrent requests complete in an unspecified order (upstream's order is only
	// the order of its microtask queue, not a protocol guarantee; requests are correlated by id).
	unordered bool
	// awaitFirstReply makes the exchange wait for the server's reply to the first write before it sends the next one. A case whose
	// first write is a complete hello continues on an established connection; a fixed delay cannot ensure that, because the
	// time the Node server needs for its first handshake varies with the host's load (a later write that races the handshake
	// is a different, timing-dependent case, which TestSecondHelloDuringHandshakeSendsOnlyTheError pins on the Go server).
	awaitFirstReply bool
}

type wireOutcome struct {
	received string
	eof      bool
}

func clientFrame(t testing.TB, message protocol.ClientMessage) []byte {
	t.Helper()
	data, err := protocol.EncodeClientMessage(message, protocol.FrameDecoderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func serverTarget() protocol.ServerTarget { return protocol.ServerTarget{ServerId: logicalServerID} }

func wireCases(t testing.TB) []wireCase {
	hello := clientFrame(t, protocol.ClientHello{Version: protocol.ProtocolVersion})
	request := func(id string, target protocol.RpcTarget, serviceID, member string, arguments ...string) []byte {
		raw := make([]json.RawMessage, len(arguments))
		for i, argument := range arguments {
			raw[i] = json.RawMessage(argument)
		}
		value, err := protocol.FromJSON(mustJSON(chord.ServiceCall{ServiceId: serviceID, Member: member, Args: raw}))
		if err != nil {
			t.Fatal(err)
		}
		return clientFrame(t, protocol.RequestEnvelope{Id: id, Target: target, Call: value})
	}
	cases := []wireCase{
		{name: "hello", writes: [][]byte{hello}},
		{name: "hello byte by byte", writes: splitBytes(hello)},
		{name: "hello twice in one write", writes: [][]byte{slices.Concat(hello, hello)}},
		{name: "hello then hello", writes: [][]byte{hello, hello}},
		{name: "empty connection"},
		{name: "unsupported version 7", writes: [][]byte{clientFrame(t, protocol.ClientHello{Version: 7})}},
		{name: "unsupported version 9", writes: [][]byte{clientFrame(t, protocol.ClientHello{Version: 9})}},
		{name: "version 0", writes: [][]byte{clientFrame(t, protocol.ClientHello{Version: 0})}},
		{name: "request before hello", writes: [][]byte{request("r1", serverTarget(), "pi.session-management", "detach")}},
		{name: "cancel before hello", writes: [][]byte{clientFrame(t, protocol.CancelEnvelope{Id: "r1", Target: serverTarget()})}},
		{name: "garbage", writes: [][]byte{{0x01, 0x02, 0x03, 0x04, 0x05, 0x06}}},
		{name: "length over default frame limit", writes: [][]byte{{0x01, 0x00, 0x00, 0x01}}},
		{name: "length max uint32", writes: [][]byte{{0xff, 0xff, 0xff, 0xff}}},
		{name: "zero length frame", writes: [][]byte{{0, 0, 0, 0}}},
		{name: "partial header", writes: [][]byte{{0, 0}}},
		{name: "truncated hello", writes: [][]byte{hello[:len(hello)-3]}},
		{name: "hello then garbage", writes: [][]byte{hello, {0xde, 0xad, 0xbe, 0xef, 0x00}}},
		{name: "hello then zero length frame", writes: [][]byte{hello, {0, 0, 0, 0}}},
		{name: "hello then unsupported server call", writes: [][]byte{hello, request("r1", serverTarget(), "x", "y")}},
		{name: "hello then wrong server", writes: [][]byte{hello, request("r1", protocol.ServerTarget{ServerId: otherServerID}, "x", "y")}},
		{name: "hello then unknown session attach", writes: [][]byte{hello, request("r1", serverTarget(), "pi.session-management", "attach", `"nope"`)}},
		{name: "hello then unattached session call", writes: [][]byte{hello, request("r1", protocol.SessionTarget{ServerId: logicalServerID, SessionId: "session-1", AttachmentId: "a"}, "s", "echo")}},
		{name: "hello then cancel of unknown request", writes: [][]byte{hello, clientFrame(t, protocol.CancelEnvelope{Id: "never", Target: serverTarget()})}},
		{name: "hello then cancel for wrong server", writes: [][]byte{hello, clientFrame(t, protocol.CancelEnvelope{Id: "never", Target: protocol.ServerTarget{ServerId: otherServerID}})}},
		{name: "attach with extra argument", writes: [][]byte{hello, request("r1", serverTarget(), "pi.session-management", "attach", `"session-1"`, `1`)}},
		{name: "detach with argument", writes: [][]byte{hello, request("r1", serverTarget(), "pi.session-management", "detach", `1`)}},
		{name: "many requests in one write", unordered: true, writes: [][]byte{slices.Concat(hello, request("a", serverTarget(), "x", "y"), request("b", serverTarget(), "x", "y"), request("c", serverTarget(), "x", "y"))}},
	}
	// Single-byte and truncation mutations of every message exercise the parser's rejection paths end to end.
	for index, base := range [][]byte{hello, request("r1", serverTarget(), "x", "y")} {
		for _, mutated := range mutations(base) {
			if len(mutated) > 400 {
				continue
			}
			prefix := hello
			if index == 0 {
				prefix = nil
			}
			cases = append(cases, wireCase{name: fmt.Sprintf("mutation %d %x", index, mutated), writes: [][]byte{slices.Concat(prefix, mutated)}})
		}
	}
	for i := range cases {
		cases[i].awaitFirstReply = len(cases[i].writes) > 1 && bytes.Equal(cases[i].writes[0], hello)
	}
	return cases
}

func mustJSON(value any) []byte {
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return data
}

func splitBytes(data []byte) [][]byte {
	out := make([][]byte, len(data))
	for i := range data {
		out[i] = data[i : i+1]
	}
	return out
}

// exchange writes the case to the server and reads until the server closes or stays silent.
func exchange(path string, c wireCase) wireOutcome {
	conn, err := net.Dial("unix", path)
	if err != nil {
		return wireOutcome{received: "dial: " + err.Error()}
	}
	defer func() { _ = conn.Close() }()
	var received []byte
	buffer := make([]byte, 64*1024)
	for index, write := range c.writes {
		if _, err := conn.Write(write); err != nil {
			break
		}
		if index == 0 && c.awaitFirstReply {
			for len(received) < 4 || len(received) < 4+int(binary.BigEndian.Uint32(received)) {
				_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
				n, err := conn.Read(buffer)
				received = append(received, buffer[:n]...)
				if err != nil {
					return wireOutcome{received: hex.EncodeToString(received), eof: errors.Is(err, io.EOF)}
				}
			}
		}
		time.Sleep(40 * time.Millisecond) // separate reads on every server: coalesced frames change the handshake interleaving
	}
	for {
		_ = conn.SetReadDeadline(time.Now().Add(800 * time.Millisecond))
		n, err := conn.Read(buffer)
		received = append(received, buffer[:n]...)
		if err != nil {
			var netError net.Error
			timedOut := errors.As(err, &netError) && netError.Timeout()
			return wireOutcome{received: hex.EncodeToString(received), eof: !timedOut && errors.Is(err, io.EOF)}
		}
	}
}

// sortedFrames orders the length-prefixed frames of a reply.
func sortedFrames(received string) string {
	raw, _ := hex.DecodeString(received)
	var frames []string
	for len(raw) >= 4 {
		length := 4 + int(raw[0])<<24 + int(raw[1])<<16 + int(raw[2])<<8 + int(raw[3])
		if length > len(raw) {
			break
		}
		frames = append(frames, hex.EncodeToString(raw[:length]))
		raw = raw[length:]
	}
	slices.Sort(frames)
	return strings.Join(frames, "") + hex.EncodeToString(raw)
}

// describe renders received bytes as protocol messages for a readable failure.
func describe(received string) string {
	raw, _ := hex.DecodeString(received)
	decoder, _ := protocol.NewServerMessageDecoder(protocol.FrameDecoderOptions{})
	messages, err := decoder.Push(raw)
	return fmt.Sprintf("%+v err=%v", messages, err)
}

func runWire(t *testing.T, fixture *serverFixture, cases []wireCase) []wireOutcome {
	t.Helper()
	outcomes := make([]wireOutcome, len(cases))
	var group sync.WaitGroup
	slots := make(chan struct{}, 16)
	for i := range cases {
		slots <- struct{}{}
		group.Go(func() {
			defer func() { <-slots }()
			outcomes[i] = exchange(fixture.path, cases[i])
		})
	}
	group.Wait()
	return outcomes
}

func TestServerWireDifferential(t *testing.T) {
	t.Parallel()
	cases := wireCases(t)
	oracleServer := startNodeServer(t, nil)
	oracle := runWire(t, oracleServer, cases)
	for _, tc := range []struct {
		name  string
		start func(*testing.T, *float64) *serverFixture
	}{{"go server", startGoServer}} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := runWire(t, tc.start(t, nil), cases)
			var bad mismatches
			for i, c := range cases {
				if c.unordered {
					got[i].received, oracle[i].received = sortedFrames(got[i].received), sortedFrames(oracle[i].received)
				}
				if got[i] != oracle[i] {
					bad.add("%s\n  go:   eof=%v %.300s\n  node: eof=%v %.300s", c.name, got[i].eof, describe(got[i].received), oracle[i].eof, describe(oracle[i].received))
				}
			}
			bad.report(t, len(cases))
		})
	}
	// Prove the corpus reaches both outcomes: answered-and-open and closed-with-failure.
	var closed, answered int
	for _, outcome := range oracle {
		if outcome.eof {
			closed++
		}
		if outcome.received != "" {
			answered++
		}
	}
	if closed < 10 || answered < 20 {
		t.Fatalf("oracle corpus is not exercising the server: %d closed, %d answered of %d", closed, answered, len(cases))
	}
}
