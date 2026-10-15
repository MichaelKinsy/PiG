package ai

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Ports headersToRecord (packages/ai/src/utils/headers.ts:3-9): the record is `for (const [key, value] of headers.entries())`, and Headers.entries
// yields lowercase names, repeated headers joined with ", ", and every set-cookie as its own entry, so the last set-cookie wins.
func TestHeadersToRecordUpstream(t *testing.T) {
	cases := []struct {
		name string
		in   http.Header
		want map[string]string
	}{
		{"no headers", http.Header{}, map[string]string{}},
		{"names are lowercased", http.Header{"Content-Type": {"text/event-stream"}, "X-Request-Id": {"r1"}}, map[string]string{"content-type": "text/event-stream", "x-request-id": "r1"}},
		{"repeated headers are joined with a comma and a space", http.Header{"Accept": {"a", "b", "c"}}, map[string]string{"accept": "a, b, c"}},
		{"the last set-cookie wins", http.Header{"Set-Cookie": {"first=1", "second=2"}}, map[string]string{"set-cookie": "second=2"}},
		{"a header with no values is absent", http.Header{"Empty": {}}, map[string]string{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := headersToRecord(tc.in)
			if len(got) != len(tc.want) {
				t.Fatalf("headersToRecord = %v, want %v", got, tc.want)
			}
			for key, want := range tc.want {
				if got[key] != want {
					t.Errorf("%s = %q, want %q", key, got[key], want)
				}
			}
		})
	}

	// The production path: the provider's OnResponse receives the record of the endpoint's response headers.
	t.Run("OnResponse receives the record", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Add("X-Multi", "one")
			w.Header().Add("X-Multi", "two")
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
		}))
		defer srv.Close()
		model := &Model{ID: "gpt-4o-mini", DisplayName: "g", Input: []string{"text"}, ProviderMeta: ProviderMetadata{API: APIOpenAICompletions, ProviderID: "openai", BaseURL: srv.URL}}
		var seen map[string]string
		stream, err := OpenAICompletionsAPI().Stream(t.Context(), model, NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("hi")}}}), StreamOptions{APIKey: "k", OnResponse: func(_ context.Context, r ProviderResponse, _ *Model) error {
			seen = r.Headers
			return nil
		}})
		if err != nil {
			t.Fatal(err)
		}
		stream.Result()
		if seen["x-multi"] != "one, two" || seen["content-type"] != "text/event-stream" {
			t.Errorf("OnResponse headers = %v", seen)
		}
	})
}

// Ports isThinkingPart (packages/ai/src/api/google-shared.ts:128): `part.thought === true`. A thought signature alone does not make a part thinking.
func TestGoogleIsThinkingPartUpstream(t *testing.T) {
	yes, no := true, false
	sig := "c2ln"
	cases := []struct {
		name string
		part geminiPart
		want bool
	}{
		{"thought true", geminiPart{Thought: &yes}, true},
		{"thought true with a signature", geminiPart{Thought: &yes, ThoughtSignature: sig}, true},
		{"thought false", geminiPart{Thought: &no}, false},
		{"thought absent", geminiPart{}, false},
		{"a signature without thought", geminiPart{ThoughtSignature: sig}, false},
	}
	for _, tc := range cases {
		if got := isThinkingPart(tc.part); got != tc.want {
			t.Errorf("%s: isThinkingPart = %v, want %v", tc.name, got, tc.want)
		}
	}

	// The production path: a streamed part with thought:true becomes a thinking block, a signed part without it stays text.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"plan\",\"thought\":true},{\"text\":\"answer\",\"thoughtSignature\":\"c2ln\"}]},\"finishReason\":\"STOP\"}]}\n\n")
	}))
	defer srv.Close()
	model := &Model{ID: "gemini-2.5-flash", DisplayName: "g", Input: []string{"text"}, ProviderMeta: ProviderMetadata{API: APIGoogleGenerativeAI, ProviderID: "google", BaseURL: srv.URL, Reasoning: true}}
	stream, err := GoogleGenerativeAIAPI().Stream(t.Context(), model, NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("hi")}}}), StreamOptions{APIKey: "k"})
	if err != nil {
		t.Fatal(err)
	}
	message := stream.Result()
	var kinds []string
	for _, block := range message.Content {
		switch b := block.(type) {
		case ThinkingContent:
			kinds = append(kinds, "thinking:"+b.Thinking)
		case TextContent:
			kinds = append(kinds, "text:"+b.Text)
		default:
			kinds = append(kinds, fmt.Sprintf("%T", block))
		}
	}
	if fmt.Sprint(kinds) != "[thinking:plan text:answer]" {
		t.Errorf("content = %v (stop %v %q), want [thinking:plan text:answer]", kinds, message.StopReason, message.ErrorMessage)
	}
}
