package link_test

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"testing"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/link"
)

// Helpers shared by the link tests: the port of the parts of packages/chord/test/next/support.ts they use
// (forEachCodec, RawPeer's role, transported).

var background = chordctx.Background()

const incarnation = "0190f5a2-7c3e-7d4a-9b1c-2d3e4f5a6b7c"

// rawPeerIncarnation is RAW_PEER: the incarnation a raw peer greets with.
const rawPeerIncarnation = "00000000-0000-7000-8000-000000000000"

// server is the address a hold names: `Server()` on the wire.
func server() link.WireAddress {
	return link.WireAddress{Kind: "server", Params: delta.NewJsonObject(0)}
}

func object(pairs ...any) *delta.JsonObject { return delta.JsonObjectOf(pairs...) }

// chordError is a ChordError's wire error.
func chordError(code, message string) link.WireError {
	return object("kind", "chord", "error", object("code", code, "message", message))
}

func pluginError(code, message string, details ...any) link.WireError {
	wire := object("kind", "plugin", "code", code, "message", message)
	if len(details) > 0 {
		wire.Set("details", details[0])
	}
	return wire
}

func some(value int) link.Optional[int] { return link.Some(value) }

func codecs() []link.Codec { return []link.Codec{link.BinaryCodec, link.TextCodec} }

// eachCodec runs body once per codec, as `forEachCodec` runs a suite as `binary codec` and `text codec`.
func eachCodec(t *testing.T, body func(t *testing.T, codec link.Codec)) {
	t.Helper()
	for _, codec := range codecs() {
		t.Run(codec.Name()+" codec", func(t *testing.T) { body(t, codec) })
	}
}

func encode(t testing.TB, codec link.Codec, message link.Message) []byte {
	t.Helper()
	encoded, err := codec.Encode(message)
	if err != nil {
		t.Fatalf("%s encode %s: %v", codec.Name(), link.DescribeMessage(message), err)
	}
	return encoded
}

func decode(t testing.TB, codec link.Codec, bytes []byte) link.Message {
	t.Helper()
	message, err := codec.Decode(bytes)
	if err != nil {
		t.Fatalf("%s decode: %v", codec.Name(), err)
	}
	return message
}

// canon is a message with every part as text and its segments' bytes: two messages with equal canon are equal on the
// wire and in every field the codecs carry.
func canon(message link.Message) string {
	var text strings.Builder
	text.WriteString(link.DescribeMessage(message))
	var segments [][]byte
	switch typed := message.(type) {
	case *link.CallMessage:
		segments = typed.Segments
	case *link.ResultMessage:
		segments = typed.Segments
	}
	for _, segment := range segments {
		fmt.Fprintf(&text, " seg:%s", hex.EncodeToString(segment))
	}
	return text.String()
}

func requireCanon(t testing.TB, got, want link.Message) {
	t.Helper()
	if canon(got) != canon(want) {
		t.Fatalf("message = %s, want %s", canon(got), canon(want))
	}
}

// requireErrorContains fails unless err is not nil and its message contains text.
func requireErrorContains(t testing.TB, err error, text string) {
	t.Helper()
	if err == nil {
		t.Fatalf("no error, want one containing %q", text)
	}
	if !strings.Contains(err.Error(), text) {
		t.Fatalf("error = %q, want it to contain %q", err.Error(), text)
	}
}

func requireNoError(t testing.TB, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// rawPeer is a peer speaking the wire by hand at the other end of a link, in a codec: it records what arrives.
type rawPeer struct {
	t     testing.TB
	link  link.Link
	codec link.Codec

	mu       sync.Mutex
	received []link.Message
	closed   bool
}

func newRawPeer(t testing.TB, end link.Link, codec link.Codec) *rawPeer {
	t.Helper()
	peer := &rawPeer{t: t, link: end, codec: codec}
	end.OnMessage(func(bytes []byte) {
		message, err := codec.Decode(bytes)
		if err != nil {
			t.Errorf("raw peer decode: %v", err)
			return
		}
		peer.mu.Lock()
		peer.received = append(peer.received, message)
		peer.mu.Unlock()
	})
	end.OnClose(func() {
		peer.mu.Lock()
		peer.closed = true
		peer.mu.Unlock()
	})
	return peer
}

func (peer *rawPeer) send(message link.Message) { peer.link.Send(encode(peer.t, peer.codec, message)) }

func (peer *rawPeer) sendBytes(bytes []byte) { peer.link.Send(bytes) }

func (peer *rawPeer) kinds() []string {
	peer.mu.Lock()
	defer peer.mu.Unlock()
	kinds := []string{}
	for _, message := range peer.received {
		kinds = append(kinds, message.Kind())
	}
	return kinds
}

func (peer *rawPeer) isClosed() bool {
	peer.mu.Lock()
	defer peer.mu.Unlock()
	return peer.closed
}

func (peer *rawPeer) hello() {
	peer.send(&link.HelloMessage{Protocol: 1, Incarnation: rawPeerIncarnation, Calls: 256})
}

// transported waits until messages and closes in flight on memory links have arrived (support.ts `transported`).
func transported(ends ...*link.MemoryLink) {
	for range 6 {
		for _, end := range ends {
			end.Flushed()
		}
	}
}

func bytesEqual(a, b []byte) bool { return bytes.Equal(a, b) }
