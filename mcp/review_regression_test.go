package mcp_test

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/MichaelKinsy/PiG/mcp"
)

// streamable-http.ts send: `await response.json()` rejects an invalid body
// with JSON.parse's SyntaxError, and Response.json() drops a leading BOM.
func TestStreamableHTTPTransportRejectsAnInvalidJSONBodyWithJSONParseMessage(t *testing.T) {
	url, _ := startServer(t, func(w http.ResponseWriter, r *http.Request, _ *requestLog) {
		_, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, "{bad")
	})
	client := newHTTPClient()
	_, err := client.Connect(t.Context(), newHTTPTransport(t, mcp.StreamableHTTPTransportOptions{URL: url}))
	// node -e 'new Response("{bad").json().catch((e) => console.log(e.message))'
	if want := "Expected property name or '}' in JSON at position 1 (line 1 column 2)"; err == nil || err.Error() != want {
		t.Fatalf("err = %v, want %q", err, want)
	}
}

func TestStreamableHTTPTransportAcceptsAJSONBodyWithALeadingBOM(t *testing.T) {
	url, requests := startServer(t, func(w http.ResponseWriter, r *http.Request, requests *requestLog) {
		if r.Method == http.MethodPost {
			body := readJSONBody(r)
			if body["method"] == "initialize" {
				requests.add(recordedRequest{method: r.Method, headers: r.Header.Clone(), message: body})
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, "\uFEFF"+`{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2025-11-25","capabilities":{},"serverInfo":{"name":"s","version":"1"}}}`)
				return
			}
			w.WriteHeader(http.StatusAccepted)
			return
		}
		w.WriteHeader(http.StatusMethodNotAllowed)
	})
	client := newHTTPClient()
	if _, err := client.Connect(t.Context(), newHTTPTransport(t, mcp.StreamableHTTPTransportOptions{URL: url})); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()
	if len(requests.all()) != 1 {
		t.Fatalf("requests = %d", len(requests.all()))
	}
}

// streamable-http.ts checkResponse keeps `(await response.text()).slice(0, MAX_ERROR_BODY_BYTES)`:
// 8192 UTF-16 units of the decoded body, not 8192 bytes.
func TestStreamableHTTPTransportKeepsMaxErrorBodyUTF16UnitsOfAnErrorBody(t *testing.T) {
	url, _ := startServer(t, func(w http.ResponseWriter, r *http.Request, _ *requestLog) {
		_, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, strings.Repeat("é", 9000))
	})
	client := newHTTPClient()
	_, err := client.Connect(t.Context(), newHTTPTransport(t, mcp.StreamableHTTPTransportOptions{URL: url}))
	httpErr, ok := errors.AsType[*mcp.McpHttpError](err)
	if !ok {
		t.Fatalf("err = %v", err)
	}
	if got := len(utf16.Encode([]rune(httpErr.Body))); got != 8*1024 {
		t.Fatalf("body has %d UTF-16 units, want %d", got, 8*1024)
	}
	if httpErr.Body != strings.Repeat("é", 8*1024) {
		t.Fatalf("body is not the decoded prefix")
	}
}
