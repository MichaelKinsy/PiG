package mcp_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/mcp"
)

// Pi packages/mcp/test/streamable-http.test.ts (4a8b60bc3, #11335): the three tests added for readBodyText. Inputs and expectations are Pi's.

type bodyFetch struct {
	status int
	header http.Header
	body   io.ReadCloser
}

func (f bodyFetch) Do(*http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: f.status, Header: f.header, Body: f.body}, nil
}

func jsonHeader() http.Header { return http.Header{"Content-Type": []string{"application/json"}} }

func sendToolsList(t *testing.T, options mcp.StreamableHTTPTransportOptions) ([]mcp.JSONRPCMessage, error) {
	t.Helper()
	options.URL = "http://127.0.0.1/mcp"
	options.OpenGetStream = new(false)
	transport := newHTTPTransport(t, options)
	var messages []mcp.JSONRPCMessage
	transport.OnMessage(func(message mcp.JSONRPCMessage) { messages = append(messages, message) })
	if err := transport.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = transport.Close() }()
	err := transport.Send(t.Context(), mcp.NewRequest(mcp.NumberID(1), "tools/list", nil))
	return messages, err
}

// mutation-checked: removing the cap on the application/json body, or `>` for `>=` / `>=` for `>` at the limit, fails it.
func TestStreamableHTTPAppliesMaxMessageBytesToJSONResponses(t *testing.T) {
	reply := func(descriptionBytes int) []byte {
		body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "result": map[string]any{"tools": []any{map[string]any{"name": "echo", "description": strings.Repeat("x", descriptionBytes), "inputSchema": map[string]any{}}}}})
		return body
	}
	send := func(body []byte, limit int) ([]mcp.JSONRPCMessage, error) {
		return sendToolsList(t, mcp.StreamableHTTPTransportOptions{MaxMessageBytes: limit, Fetch: bodyFetch{status: 200, header: jsonHeader(), body: io.NopCloser(bytes.NewReader(body))}})
	}
	if messages, err := send(reply(32), 512); err != nil || len(messages) != 1 {
		t.Fatalf("small reply: messages=%d err=%v", len(messages), err)
	}
	if _, err := send(reply(2048), 512); err == nil || err.Error() != "MCP JSON response exceeds 512 bytes" {
		t.Fatalf("2 KB reply: err = %v, want MCP JSON response exceeds 512 bytes", err)
	}
	// Pi counts bytes and rejects only a body above maxBytes: a body of exactly maxBytes is accepted.
	exact := reply(32)
	if messages, err := send(exact, len(exact)); err != nil || len(messages) != 1 {
		t.Fatalf("body of exactly the limit: messages=%d err=%v", len(messages), err)
	}
	if _, err := send(exact, len(exact)-1); err == nil || err.Error() != "MCP JSON response exceeds "+itoa(len(exact)-1)+" bytes" {
		t.Fatalf("body one byte over the limit: err = %v", err)
	}
}

func itoa(n int) string { return strconv.Itoa(n) }

// byteReader hands out one byte per Read, like Pi's one-byte chunks.
type byteReader struct{ data []byte }

func (r *byteReader) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, io.EOF
	}
	p[0] = r.data[0]
	r.data = r.data[1:]
	return 1, nil
}
func (r *byteReader) Close() error { return nil }

// mutation-checked: decoding each chunk on its own, or keeping a leading BOM, fails it.
func TestStreamableHTTPDecodesJSONResponsesSplitInsideABOMAndAMultiByteCharacter(t *testing.T) {
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "result": map[string]any{"text": "é€"}})
	messages, err := sendToolsList(t, mcp.StreamableHTTPTransportOptions{Fetch: bodyFetch{status: 200, header: jsonHeader(), body: &byteReader{data: append([]byte("\uFEFF"), body...)}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 {
		t.Fatalf("messages = %d", len(messages))
	}
	var result struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(messages[0].Result, &result); err != nil || result.Text != "é€" {
		t.Fatalf("result = %s err=%v", messages[0].Result, err)
	}
}

// endlessBody never ends; closing it records the cancel.
type endlessBody struct{ closed atomic.Bool }

func (b *endlessBody) Read(p []byte) (int, error) {
	if b.closed.Load() {
		return 0, errors.New("closed")
	}
	return copy(p, bytes.Repeat([]byte("x"), min(len(p), 1024))), nil
}
func (b *endlessBody) Close() error { b.closed.Store(true); return nil }

// mutation-checked: reading the whole error body (never finishes), or keeping more or fewer than 8 KiB, fails it.
func TestStreamableHTTPReadsOnlyAnExcerptOfHTTPErrorBodiesAndCancelsTheRest(t *testing.T) {
	body := &endlessBody{}
	_, err := sendToolsList(t, mcp.StreamableHTTPTransportOptions{Fetch: bodyFetch{status: 500, body: body}})
	httpErr, ok := errors.AsType[*mcp.McpHttpError](err)
	if !ok {
		t.Fatalf("err = %v, want McpHttpError", err)
	}
	if len(httpErr.Body) != 8*1024 {
		t.Fatalf("body length = %d, want %d", len(httpErr.Body), 8*1024)
	}
	if !body.closed.Load() {
		t.Fatal("the rest of the error body was not cancelled")
	}
}

// stalledBody yields data, then blocks until it is closed.
type stalledBody struct {
	data   []byte
	closed chan struct{}
	once   sync.Once
}

func (b *stalledBody) Read(p []byte) (int, error) {
	if len(b.data) > 0 {
		n := copy(p, b.data)
		b.data = b.data[n:]
		return n, nil
	}
	<-b.closed
	return 0, errors.New("closed")
}
func (b *stalledBody) Close() error { b.once.Do(func() { close(b.closed) }); return nil }

// Pi's readBodyText rejects a body as soon as a chunk takes it past maxBytes, without waiting for the rest: a server that
// sends maxBytes+1 bytes and then stalls is answered at once.
// mutation-checked: reading one byte past the limit+1 that proves the overflow (LimitReader maxBytes+2) hangs it.
func TestStreamableHTTPRejectsAnOversizedJSONReplyWithoutWaitingForTheRestOfTheBody(t *testing.T) {
	const limit = 512
	body := &stalledBody{data: bytes.Repeat([]byte("x"), limit+1), closed: make(chan struct{})}
	t.Cleanup(func() { _ = body.Close() })
	done := make(chan error, 1)
	go func() {
		_, err := sendToolsList(t, mcp.StreamableHTTPTransportOptions{MaxMessageBytes: limit, Fetch: bodyFetch{status: 200, header: jsonHeader(), body: body}})
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil || err.Error() != "MCP JSON response exceeds 512 bytes" {
			t.Fatalf("err = %v, want MCP JSON response exceeds 512 bytes", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Send waited for the rest of an oversized body")
	}
}

// failingBody yields data, then fails.
type failingBody struct{ data []byte }

func (b *failingBody) Read(p []byte) (int, error) {
	if len(b.data) > 0 {
		n := copy(p, b.data)
		b.data = b.data[n:]
		return n, nil
	}
	return 0, errors.New("connection reset")
}
func (b *failingBody) Close() error { return nil }

// Pi's checkResponse is `readBodyText(...).catch(() => ({ text: "" }))`: a body that fails mid-read leaves an empty body, not the
// bytes read before the failure.
// mutation-checked: returning the partial data of a failed read fails it.
func TestStreamableHTTPAnErrorBodyThatFailsMidReadIsEmpty(t *testing.T) {
	_, err := sendToolsList(t, mcp.StreamableHTTPTransportOptions{Fetch: bodyFetch{status: 500, body: &failingBody{data: []byte("partial")}}})
	httpErr, ok := errors.AsType[*mcp.McpHttpError](err)
	if !ok {
		t.Fatalf("err = %v, want McpHttpError", err)
	}
	if httpErr.Body != "" {
		t.Fatalf("body = %q, want empty", httpErr.Body)
	}
	if want := "MCP HTTP request failed with status 500"; httpErr.Error() != want {
		t.Fatalf("message = %q, want %q", httpErr.Error(), want)
	}
}
