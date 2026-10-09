package routingtest_test

import (
	"bytes"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/experimental/protocol"
	"github.com/MichaelKinsy/PiG/internal/experimental/routing/routingtest"
)

type recordingChannel struct {
	sent      [][]byte
	fragments [][2][]byte
	closed    int
}

func (c *recordingChannel) Send(chunk []byte) error {
	c.sent = append(c.sent, bytes.Clone(chunk))
	return nil
}

func (c *recordingChannel) SendFragmented(chunk []byte, splitAt int) error {
	c.fragments = append(c.fragments, [2][]byte{bytes.Clone(chunk[:splitAt]), bytes.Clone(chunk[splitAt:])})
	return nil
}

func (c *recordingChannel) Close() error {
	c.closed++
	return nil
}

// packages/server/src/testing/client.ts: sendBytes writes the chunk through the channel, sendFragmented splits one encoded frame at the
// given offset, and close closes the channel exactly through it.
// Pi source: packages/server/src/testing/client.ts:27-40 (ProtocolTestClient).
// mutation-checked: negating the condition `err != nil` at client.go:169 fails it.
func TestProtocolTestClientDrivesItsWireChannel(t *testing.T) {
	channel := &recordingChannel{}
	var wire routingtest.WireChannel = channel
	client := routingtest.NewProtocolTestClient(wire)

	if err := client.SendBytes([]byte("raw")); err != nil {
		t.Fatal(err)
	}
	if len(channel.sent) != 1 || string(channel.sent[0]) != "raw" {
		t.Fatalf("Send saw %q", channel.sent)
	}

	message := protocol.ClientHello{Version: 1}
	frame, err := protocol.EncodeClientMessage(message, protocol.FrameDecoderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.SendFragmentedMessage(message, 3); err != nil {
		t.Fatal(err)
	}
	if len(channel.fragments) != 1 || string(channel.fragments[0][0]) != string(frame[:3]) || string(channel.fragments[0][1]) != string(frame[3:]) {
		t.Fatalf("SendFragmented saw %q, want the frame split at 3", channel.fragments)
	}

	if err := client.SendMessage(message); err != nil {
		t.Fatal(err)
	}
	if len(channel.sent) != 2 || !bytes.Equal(channel.sent[1], frame) {
		t.Fatalf("SendMessage sent %q, want the encoded frame", channel.sent[1:])
	}

	if err := client.Close(); err != nil || channel.closed != 1 {
		t.Fatalf("Close = %v, channel closed %d times", err, channel.closed)
	}
}
