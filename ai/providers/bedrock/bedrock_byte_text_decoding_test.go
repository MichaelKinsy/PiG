//go:build !pig_strip_bedrock_converse_stream

package bedrock

import (
	"strings"
	"testing"
)

// Node decodes bytes as text (the Smithy event-stream header and body decoders) with the WHATWG decoder: one U+FFFD per maximal ill-formed subpart. A run of invalid bytes is several replacement characters, not one.
func TestBedrockByteTextDecodingReplacesEachIllFormedSubpart(t *testing.T) {
	const invalid = "\xff\xfe"
	const want = "\uFFFD\uFFFD"

	t.Run("bedrock event header name and value", func(t *testing.T) {
		headers := []byte{2, 0xff, 0xfe, 7, 0, 2, 0xff, 0xfe}
		parsed, err := parseBedrockEventHeaders(headers)
		if err != nil {
			t.Fatal(err)
		}
		if header, ok := parsed[want]; !ok || header.value != want {
			t.Fatalf("headers = %q", parsed)
		}
	})

	t.Run("bedrock unknown exception body", func(t *testing.T) {
		_, err := bedrockEventDeserializer(&bedrockEventMessage{
			headers: map[string]bedrockEventHeader{":message-type": {kind: "string", value: "exception"}, ":exception-type": {kind: "string", value: "NotAnException"}},
			body:    []byte("x" + invalid + "y"),
		})
		if err == nil || !strings.Contains(err.Error(), "x"+want+"y") {
			t.Fatalf("error = %v", err)
		}
	})
}
