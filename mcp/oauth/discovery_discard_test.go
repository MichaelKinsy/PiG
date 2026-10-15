package oauth

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

type closeCounter struct {
	io.Reader
	closed int
}

func (c *closeCounter) Close() error { c.closed++; return nil }

// discovery.ts:20 `void response?.body?.cancel().catch(() => {})`: a discarded discovery response has its body released, and an absent response
// or an absent body is skipped without failing.
// Pi: packages/mcp/src/oauth/discovery.ts:20
func TestDiscardReleasesTheBodyAndToleratesAbsentResponsesAndBodies(t *testing.T) {
	body := &closeCounter{Reader: strings.NewReader("ignored")}
	discard(&http.Response{Body: body})
	if body.closed != 1 {
		t.Fatalf("body closed %d times, want 1", body.closed)
	}
	discard(nil)
	discard(&http.Response{})
}
