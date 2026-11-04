package mcp_test

import (
	"errors"
	"fmt"
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

// streamable-http.ts checkResponse (4a8b60bc3) keeps readBodyText(response, MAX_ERROR_BODY_BYTES): the first 8192 bytes of the
// body, decoded; a multi-byte character cut by the limit is dropped, not replaced, and a body that ends inside one is
// replaced (decoder.decode() flushes it).
// mutation-checked: counting UTF-16 units instead of bytes, or keeping the cut character's lead byte, fails it.
func TestStreamableHTTPTransportKeepsTheFirstMaxErrorBodyBytesOfAnErrorBody(t *testing.T) {
	type errorBodyCase struct{ name, body, want string }
	cases := []errorBodyCase{
		{"two-byte characters", strings.Repeat("é", 9000), strings.Repeat("é", 4*1024)},
		{"a character cut by the limit", strings.Repeat("a", 8*1024-1) + "é" + "tail", strings.Repeat("a", 8*1024-1)},
		{"a character ending at the limit", strings.Repeat("a", 8*1024-2) + "é" + "tail", strings.Repeat("a", 8*1024-2) + "é"},
		{"a body of exactly the limit", strings.Repeat("a", 8*1024), strings.Repeat("a", 8*1024)},
		{"a body ending inside a character", strings.Repeat("a", 8*1024-1) + "\xc3", strings.Repeat("a", 8*1024-1) + "\uFFFD"},
	}
	// A 3- or 4-byte character cut after k of its bytes is dropped whole, wherever the cut falls (Pi: decode(subarray, {stream: true})
	// withholds the incomplete tail); cut after all its bytes it stays. Expectations checked against Pi's readBodyText on node 24.
	for _, char := range []string{"é", "€", "😀"} {
		for k := 1; k <= len(char); k++ {
			prefix := strings.Repeat("a", 8*1024-k)
			want := prefix
			if k == len(char) {
				want += char
			}
			cases = append(cases, errorBodyCase{fmt.Sprintf("%q cut after %d of %d bytes", char, k, len(char)), prefix + char + "tail", want})
		}
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			url, _ := startServer(t, func(w http.ResponseWriter, r *http.Request, _ *requestLog) {
				_, _ = io.ReadAll(r.Body)
				w.WriteHeader(http.StatusBadRequest)
				_, _ = io.WriteString(w, tc.body)
			})
			client := newHTTPClient()
			_, err := client.Connect(t.Context(), newHTTPTransport(t, mcp.StreamableHTTPTransportOptions{URL: url}))
			httpErr, ok := errors.AsType[*mcp.McpHttpError](err)
			if !ok {
				t.Fatalf("err = %v", err)
			}
			if httpErr.Body != tc.want {
				t.Fatalf("body has %d UTF-16 units, want %d (the decoded first %d bytes)", len(utf16.Encode([]rune(httpErr.Body))), len(utf16.Encode([]rune(tc.want))), 8*1024)
			}
		})
	}
}

// Response.text() decodes with the UTF-8 decoder, which drops one leading byte order mark, so the error body Pi
// reports (streamable-http.ts checkResponse) does not start with U+FEFF.
func TestStreamableHTTPTransportDropsALeadingBOMFromAnErrorBody(t *testing.T) {
	url, _ := startServer(t, func(w http.ResponseWriter, r *http.Request, _ *requestLog) {
		_, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, "\uFEFF\uFEFFbad request")
	})
	client := newHTTPClient()
	_, err := client.Connect(t.Context(), newHTTPTransport(t, mcp.StreamableHTTPTransportOptions{URL: url}))
	httpErr, ok := errors.AsType[*mcp.McpHttpError](err)
	if !ok {
		t.Fatalf("err = %v", err)
	}
	if httpErr.Body != "\uFEFFbad request" {
		t.Fatalf("body = %q, want exactly one leading BOM removed", httpErr.Body)
	}
}
