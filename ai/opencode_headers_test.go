package ai

import (
	"context"
	"testing"
)

// Pi packages/ai/src/providers/opencode-headers.ts: the session header is added from options.sessionId, never replaces an existing header (any case) and never mutates the caller's headers.
func TestWithOpenCodeSessionHeaderLikeUpstream(t *testing.T) {
	var seen []StreamOptions
	record := func(_ context.Context, _ *Model, _ TranscriptContext, options StreamOptions) (*AssistantMessageEventStream, error) {
		seen = append(seen, options)
		return nil, nil
	}
	wrapped := WithOpenCodeSessionHeader(&ProviderStreams{Stream: record, StreamSimple: record})
	existing := ProviderHeaders{"X-OpenCode-Session": new("explicit"), "Other": new("o")}
	caller := ProviderHeaders{"Other": new("o")}
	cases := []struct {
		name    string
		options StreamOptions
		want    map[string]string
	}{
		{"no session id leaves options alone", StreamOptions{Headers: caller}, map[string]string{"Other": "o"}},
		{"session id adds header and keeps others", StreamOptions{SessionID: "s1", Headers: caller}, map[string]string{"Other": "o", openCodeSessionHeader: "s1"}},
		{"existing header of any case wins", StreamOptions{SessionID: "s1", Headers: existing}, map[string]string{"X-OpenCode-Session": "explicit", "Other": "o"}},
		{"nil headers", StreamOptions{SessionID: "s2"}, map[string]string{openCodeSessionHeader: "s2"}},
	}
	for _, call := range []func(context.Context, *Model, TranscriptContext, StreamOptions) (*AssistantMessageEventStream, error){wrapped.Stream, wrapped.StreamSimple} {
		for _, tc := range cases {
			seen = nil
			if _, err := call(context.Background(), nil, TranscriptContext{}, tc.options); err != nil {
				t.Fatal(err)
			}
			got := map[string]string{}
			for name, value := range seen[0].Headers {
				got[name] = *value
			}
			if len(got) != len(tc.want) {
				t.Fatalf("%s: headers %v, want %v", tc.name, got, tc.want)
			}
			for name, value := range tc.want {
				if got[name] != value {
					t.Fatalf("%s: headers %v, want %v", tc.name, got, tc.want)
				}
			}
		}
	}
	if len(caller) != 1 {
		t.Fatalf("caller headers mutated: %v", caller)
	}
}
