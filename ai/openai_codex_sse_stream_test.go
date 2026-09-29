package ai

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
	"time"
)

// Expected values come from Pi 0.87.1's real stream against a loopback server serving the same bodies
// (parseSSE, openai-codex-responses.ts:773-830 and mapCodexEvents, :725-762).
func TestCodexSSEFramingMatchesPiParseSSE(t *testing.T) {
	created := `{"type":"response.created","response":{"id":"r1"}}`
	completed := `{"type":"response.completed","response":{"id":"r1","status":"completed"}}`
	for _, test := range []struct {
		name  string
		body  string
		stop  StopReason
		error string
	}{
		{"crlf records are not split", "data: " + created + "\r\n\r\ndata: " + completed + "\r\n\r\n", StopReasonError,
			"Invalid Codex SSE JSON: Unexpected non-whitespace character after JSON at position 51 (line 2 column 1)"},
		{"leading byte order mark", "\ufeffdata: " + created + "\n\ndata: " + completed + "\n\n", StopReasonStop, ""},
		{"no space, comment and event fields", "data:" + created + "\n\n: comment\n\nevent: x\ndata: " + completed + "\n\n", StopReasonStop, ""},
		{"residual frame at EOF", "data: " + created + "\n\ndata: " + completed, StopReasonStop, ""},
		{"non-object records are skipped", "data: 123\n\ndata: [1]\n\ndata: \"s\"\n\ndata: " + completed + "\n\n", StopReasonStop, ""},
		{"null record throws a TypeError", "data: null\n\ndata: " + completed + "\n\n", StopReasonError, "Cannot read properties of null (reading 'type')"},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, test.body)
			}))
			defer server.Close()
			provider := observationProvider(t, APIOpenAICodexResponses, server.URL)
			defer func() { _ = provider.Close() }()
			stream, err := provider.Stream(t.Context(), NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("x")}}}), StreamOptions{Transport: TransportSSE})
			if err != nil {
				t.Fatal(err)
			}
			result := stream.Result()
			if result.StopReason != test.stop || result.ErrorMessage != test.error {
				t.Fatalf("stop=%q error=%q, want %q %q", result.StopReason, result.ErrorMessage, test.stop, test.error)
			}
		})
	}
}

func TestCodexTextDecoderStreamsLikeTextDecoder(t *testing.T) {
	// TextDecoder("utf-8"): decode(bytes, {stream: true}) holds an incomplete tail, a malformed byte becomes U+FFFD,
	// one leading BOM is stripped, and the flushing decode() replaces an incomplete tail with one U+FFFD.
	var decoder codexTextDecoder
	euro := []byte("€")
	got := decoder.decode([]byte("\xef\xbb\xbfa"), true) + decoder.decode(euro[:1], true) + decoder.decode(euro[1:2], true) +
		decoder.decode(append(append([]byte(nil), euro[2:]...), 0xff, 'b', 0xe2, 0x82), true) + decoder.decode(nil, false)
	if want := "a€\ufffdb\ufffd"; got != want {
		t.Fatalf("decoded %q, want %q", got, want)
	}
	var second codexTextDecoder
	if got := second.decode([]byte("\xef\xbb\xbf\xef\xbb\xbfx"), true); got != "\ufeffx" {
		t.Fatalf("only the first BOM is stripped: %q", got)
	}
}

// The body worker is joined and the request released on every end of the stream: completion, an in-stream error and
// abort while a read is pending.
func TestCodexSSEStreamJoinsBodyWorker(t *testing.T) {
	created := "data: {\"type\":\"response.created\",\"response\":{\"id\":\"r1\"}}\n\n"
	completed := "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"r1\",\"status\":\"completed\"}}\n\n"
	for _, test := range []struct {
		name  string
		first string
		abort bool
		stop  StopReason
		error string
	}{
		{"completed", created + completed, false, StopReasonStop, ""},
		{"error record", created + "data: {\"type\":\"error\",\"message\":\"broken\"}\n\n", false, StopReasonError, "Codex error: broken"},
		{"abort while the body read is pending", created, true, StopReasonAborted, "Request was aborted"},
	} {
		t.Run(test.name, func(t *testing.T) {
			before := runtime.NumGoroutine()
			release := make(chan struct{})
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, test.first)
				w.(http.Flusher).Flush()
				if test.abort {
					select {
					case <-release:
					case <-r.Context().Done():
					}
				}
			}))
			server.Start()
			provider := observationProvider(t, APIOpenAICodexResponses, server.URL)
			ctx, cancel := context.WithCancel(t.Context())
			stream, err := provider.Stream(ctx, NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("x")}}}), StreamOptions{Transport: TransportSSE})
			if err != nil {
				t.Fatal(err)
			}
			for event := range stream.Events(context.WithoutCancel(ctx)) {
				if _, ok := event.(StartEvent); ok && test.abort {
					cancel()
				}
			}
			result := stream.Result()
			cancel()
			close(release)
			if result.StopReason != test.stop || result.ErrorMessage != test.error {
				t.Fatalf("stop=%q error=%q, want %q %q", result.StopReason, result.ErrorMessage, test.stop, test.error)
			}
			_ = provider.Close()
			server.Close()
			deadline := time.Now().Add(5 * time.Second)
			for runtime.NumGoroutine() > before && time.Now().Before(deadline) {
				time.Sleep(10 * time.Millisecond)
			}
			if after := runtime.NumGoroutine(); after > before {
				buf := make([]byte, 1<<16)
				t.Fatalf("goroutines %d -> %d\n%s", before, after, buf[:runtime.Stack(buf, true)])
			}
		})
	}
}

// A body without a readiness owner (a caller-supplied fetch) is always pending: every read completes as an external event.
func TestCodexSSEOpaqueBodyIsAlwaysPending(t *testing.T) {
	body := "data: {\"type\":\"response.created\",\"response\":{\"id\":\"r1\"}}\n\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"r1\",\"status\":\"completed\"}}\n\n"
	ctx, executor := withContinuationExecutor(t.Context())
	externals := 0
	executor.trace = &executorTrace{external: func() { externals++ }}
	provider := NewOpenAICodexResponsesProvider(OpenAICodexResponsesConfig{Model: "strict", APIKey: codexTestToken(t, "acct")})
	defer func() { _ = provider.Close() }()
	fetch := codexRoundTripper(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	stream, err := provider.Stream(ctx, NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("x")}}}), StreamOptions{Transport: TransportSSE, Fetch: &http.Client{Transport: fetch}})
	if err != nil {
		t.Fatal(err)
	}
	if result := stream.Result(); result.StopReason != StopReasonStop || result.ResponseID != "r1" {
		t.Fatalf("result=%#v", result)
	}
	// The header grant plus the one body read that carried both records.
	if externals != 2 {
		t.Fatalf("external completions=%d, want 2", externals)
	}
}
