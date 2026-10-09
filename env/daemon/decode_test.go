package daemon

// Ports packages/env/daemon/src/decode.rs tests. The daemon decodes command output with Durable's StreamDecoder,
// which these cases pin down for the daemon's use.

import (
	"strings"
	"testing"

	durableenv "github.com/MichaelKinsy/PiG/durable/env"
)

func TestDecoderKeepsAFeffThatDoesNotStartTheStream(t *testing.T) {
	decoder := durableenv.NewStreamDecoder()
	var text strings.Builder
	for _, b := range []byte{0xe2, 0x82, 0xef, 0xbb, 0xbf, 0x61} {
		text.WriteString(decoder.Decode([]byte{b}))
	}
	text.WriteString(decoder.End())
	if text.String() != "\ufffd\ufeffa" {
		t.Fatalf("text %q", text.String())
	}
}

func TestDecoderDropsOnlyALeadingMarkAndNeverSniffsUTF16(t *testing.T) {
	decoder := durableenv.NewStreamDecoder()
	if got := decoder.Decode([]byte{0xef, 0xbb}) + decoder.Decode([]byte{0xbf, 0x61}) + decoder.End(); got != "a" {
		t.Fatalf("split mark: %q", got)
	}
	decoder = durableenv.NewStreamDecoder()
	if got := decoder.Decode([]byte{0xff, 0xfe, 0x41, 0x00}) + decoder.End(); got != "\ufffd\ufffdA\x00" {
		t.Fatalf("UTF-16 mark: %q", got)
	}
}

func TestDecoderReplacesAnIncompleteSequenceOnce(t *testing.T) {
	decoder := durableenv.NewStreamDecoder()
	if got := decoder.Decode([]byte{0xe2, 0x82, 0x41}) + decoder.End(); got != "\ufffdA" {
		t.Fatalf("%q", got)
	}
	decoder = durableenv.NewStreamDecoder()
	if got := decoder.Decode([]byte{0xe2, 0x82}) + decoder.End(); got != "\ufffd" {
		t.Fatalf("%q", got)
	}
}
