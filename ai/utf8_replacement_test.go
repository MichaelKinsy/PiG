//go:build !pig_strip_bedrock_converse_stream

package ai

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Node decodes bytes as text (readFile "utf8", Response.text(), the Smithy event-stream header and body decoders) with the WHATWG decoder: one U+FFFD per maximal ill-formed subpart. A run of invalid bytes is
// several replacement characters, not one.
func TestByteTextDecodingReplacesEachIllFormedSubpart(t *testing.T) {
	const invalid = "\xff\xfe"
	const want = "\uFFFD\uFFFD"

	t.Run("anthropic identity token file", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "token")
		if err := os.WriteFile(path, []byte(" ab"+invalid+"cd\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		got, err := readAnthropicIdentityToken(path)
		if err != nil || got != "ab"+want+"cd" {
			t.Fatalf("token = %q, %v", got, err)
		}
	})

	t.Run("google non-JSON error body", func(t *testing.T) {
		resp := &http.Response{StatusCode: 500, Status: "500 Server Error", Header: http.Header{"Content-Type": {"text/plain"}}}
		err := googleHTTPError(resp, []byte("bad"+invalid+"body"))
		if err == nil || !strings.Contains(err.Error(), "bad"+want+"body") {
			t.Fatalf("error = %v", err)
		}
	})

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
