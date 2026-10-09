//go:build !pig_strip_mistral_conversations

package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

func TestMistralToolIdentityUsesIndexOrID(t *testing.T) {
	// Pi consumeChatStream keys by index when present, otherwise by ID, and retains the first name/ID for each key.
	for _, indexed := range []bool{false, true} {
		t.Run(fmt.Sprintf("indexed=%t", indexed), func(t *testing.T) {
			var body strings.Builder
			for _, fragment := range []struct {
				id, name, args string
				index          int
			}{
				{"abc123456", "first", `{"x":`, 0}, {"def123456", "second", `{"y":`, 1},
				{"abc123456", "ignored-first", `1}`, 0}, {"def123456", "ignored-second", `2}`, 1},
			} {
				call := map[string]any{"id": fragment.id, "function": map[string]any{"name": fragment.name, "arguments": fragment.args}}
				if indexed {
					call["index"] = fragment.index
				}
				chunk, err := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"tool_calls": []any{call}}}}})
				if err != nil {
					t.Fatal(err)
				}
				fmt.Fprintf(&body, "data: %s\n\n", chunk)
			}
			body.WriteString("data: {\"choices\":[{\"finish_reason\":\"tool_calls\",\"delta\":{}}]}\n\ndata: [DONE]\n\n")
			builder := newAssistantStreamBuilder(t.Context(), APIMistralConversations, "mistral", "model")
			mistralUpstreamProvider(t).consumeStream(t.Context(), io.NopCloser(strings.NewReader(body.String())), builder)
			result := builder.stream.Result()
			if result.StopReason != StopReasonToolUse {
				t.Fatal(result)
			}
			assertCatalogJSON(t, result.Content, `[{"type":"toolCall","id":"abc123456","name":"first","arguments":{"x":1}},{"type":"toolCall","id":"def123456","name":"second","arguments":{"y":2}}]`)
		})
	}
}

func TestMistralErrorTruncatesUTF16(t *testing.T) {
	// Pi truncateErrorText uses string.length and slice, including a lone high surrogate at the boundary.
	for _, tc := range []struct{ name, input, want string }{
		{"empty", "", ""},
		{"ordinary", "blocked", "blocked"},
		{"boundary", strings.Repeat("é", 4000), strings.Repeat("é", 4000)},
		{"bmp overflow", strings.Repeat("é", 4001), strings.Repeat("é", 4000) + "... [truncated 1 chars]"},
		{"surrogate boundary", strings.Repeat("a", 3999) + "🌍x", strings.Repeat("a", 3999) + jsstring.FromUTF16([]uint16{0xd83c}) + "... [truncated 2 chars]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := truncateMistralError(tc.input); got != tc.want {
				t.Fatalf("truncation mismatch: got %q want %q", got, tc.want)
			}
		})
	}
}

type mistralTrackedBody struct {
	io.ReadCloser
	closed atomic.Int32
}

func (body *mistralTrackedBody) Close() error {
	body.closed.Add(1)
	return body.ReadCloser.Close()
}

func TestMistralRequestLifetimeClosesBodyOnce(t *testing.T) {
	for _, mode := range []string{"success", "abort", "timeout", "response-error"} {
		t.Run(mode, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				body := &mistralTrackedBody{ReadCloser: io.NopCloser(strings.NewReader(mistralUpstreamTerminal))}
				if mode == "abort" || mode == "timeout" {
					r, w := io.Pipe()
					defer func() { _ = r.Close() }()
					defer func() { _ = w.Close() }()
					body.ReadCloser = r
				}
				var requestCtx context.Context
				opts := StreamOptions{Fetch: &http.Client{Transport: FetchFunction(func(r *http.Request) (*http.Response, error) {
					requestCtx = r.Context()
					return &http.Response{StatusCode: 200, Header: http.Header{}, Body: body}, nil
				})}}
				if mode == "timeout" {
					opts.TimeoutMs = new(5)
				}
				if mode == "response-error" {
					opts.OnResponse = func(context.Context, ProviderResponse, *Model) error { return io.ErrUnexpectedEOF }
				}
				stream, err := mistralUpstreamProvider(t).Stream(ctx, mistralUpstreamContext(), opts)
				if mode == "response-error" {
					if err == nil {
						t.Fatal("response callback error lost")
					}
				} else {
					if err != nil {
						t.Fatal(err)
					}
					synctest.Wait()
					if mode == "abort" {
						cancel()
					}
					if mode == "timeout" {
						time.Sleep(5 * time.Millisecond)
					}
					synctest.Wait()
					stream.Result()
				}
				synctest.Wait()
				if body.closed.Load() != 1 {
					t.Fatalf("close count=%d", body.closed.Load())
				}
				if requestCtx.Err() == nil {
					t.Fatal("request timer/context retained after stream completion")
				}
			})
		})
	}
}

func BenchmarkMistralWirePayload(b *testing.B) {
	messages := make([]mistralMessage, 64)
	for i := range messages {
		messages[i] = mistralMessage{Role: "assistant", Prefix: new(false), Content: []mistralContentChunk{{Type: "text", Text: new(strings.Repeat("x", 1024))}}}
	}
	payload := mistralRequest{Model: "mistral-large-latest", Stream: true, Messages: messages}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := toMistralWirePayload(payload); err != nil {
			b.Fatal(err)
		}
	}
}
