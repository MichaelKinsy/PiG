package protocol_test

import (
	"errors"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/experimental/protocol"
)

// packages/protocol framing.ts:12-17 FrameError, cbor/options.ts:25-30 CborError, codec.ts:13-18 ProtocolValidationError: Error classes whose
// constructors set `name`; the decoders report their failures through them.
func TestProtocolErrorClassesCarryTheirNames(t *testing.T) {
	cases := []struct {
		err           interface{ Error() string }
		name, message string
	}{
		{protocol.NewFrameError("bad frame"), "FrameError", "bad frame"},
		{protocol.NewCborError("bad cbor"), "CborError", "bad cbor"},
		{protocol.NewProtocolValidationError("bad message"), "ProtocolValidationError", "bad message"},
	}
	for _, c := range cases {
		named := c.err.(interface{ Name() string })
		if named.Name() != c.name || c.err.Error() != c.message {
			t.Errorf("%T: name=%q message=%q, want %q %q", c.err, named.Name(), c.err.Error(), c.name, c.message)
		}
	}
	decoder, err := protocol.NewFrameDecoder(protocol.FrameDecoderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := decoder.End(); err != nil {
		t.Fatal(err)
	}
	_, err = decoder.Push([]byte{0})
	var failure *protocol.FrameError
	if !errors.As(err, &failure) || failure.Error() != "Frame decoder has ended" {
		t.Fatalf("Push after End = %#v, want a FrameError", err)
	}
}
