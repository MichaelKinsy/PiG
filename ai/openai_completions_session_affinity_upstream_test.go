package ai

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// openai-completions.ts:776-787 buildHeaders: with a session id and compat.sendSessionAffinityHeaders, the "openrouter" format sends x-session-id
// alone, "openai" sends session_id, x-client-request-id and x-session-affinity, and any other format sends the last two. :1680-1681 getCompat
// detects sendSessionAffinityHeaders as isOpenRouter and the format as "openrouter" for OpenRouter, else "openai"; :1729 lets the model's own
// compat override each. :348 passes no session id when options.cacheRetention is "none".
func TestOpenAICompletionsSessionAffinityHeadersFollowThePiCompat(t *testing.T) {
	yes, no := true, false
	const id = "sess-42"
	type headers struct{ sessionID, clientRequestID, affinity, underscore string }
	cases := []struct {
		name      string
		provider  string
		compat    *OpenAICompat
		retention CacheRetention
		session   string
		want      headers
	}{
		{name: "openrouter sends x-session-id only by detection", provider: "openrouter", session: id, want: headers{sessionID: id}},
		{name: "a non-openrouter provider sends none by detection", provider: "custom", session: id},
		{name: "the model compat turns it on with the openai format", provider: "custom", compat: &OpenAICompat{SendSessionAffinityHeaders: &yes}, session: id, want: headers{underscore: id, clientRequestID: id, affinity: id}},
		{name: "the nosession format omits session_id", provider: "custom", compat: &OpenAICompat{SendSessionAffinityHeaders: &yes, SessionAffinityFormat: SessionAffinityOpenAINoSession}, session: id, want: headers{clientRequestID: id, affinity: id}},
		{name: "the model compat turns it off for openrouter", provider: "openrouter", compat: &OpenAICompat{SendSessionAffinityHeaders: &no}, session: id},
		{name: "the model compat picks the openrouter format elsewhere", provider: "custom", compat: &OpenAICompat{SendSessionAffinityHeaders: &yes, SessionAffinityFormat: SessionAffinityOpenRouter}, session: id, want: headers{sessionID: id}},
		{name: "no session id sends none", provider: "openrouter"},
		{name: "cache retention none sends none", provider: "openrouter", retention: CacheRetentionNone, session: id},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got headers
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				got = headers{sessionID: r.Header.Get("x-session-id"), clientRequestID: r.Header.Get("x-client-request-id"), affinity: r.Header.Get("x-session-affinity"), underscore: r.Header.Get("session_id")}
				w.WriteHeader(http.StatusBadRequest)
				_, _ = io.WriteString(w, "{}")
			}))
			defer srv.Close()
			provider := NewOpenAIProvider(OpenAIConfig{APIKey: "k", Model: "m", BaseURL: srv.URL, ProviderID: tc.provider, Compat: tc.compat})
			stream, err := provider.Stream(context.Background(), NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("hi")}}}), StreamOptions{SessionID: tc.session, CacheRetention: tc.retention})
			if err != nil {
				t.Fatal(err)
			}
			for range stream.Events(context.Background()) {
			}
			if got != tc.want {
				t.Fatalf("headers = %+v, want %+v", got, tc.want)
			}
		})
	}
}
