package ai

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Node decodes bytes as text (readFile "utf8", Response.text()) with the WHATWG decoder: one U+FFFD per maximal ill-formed subpart. A run of invalid bytes is
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
}
