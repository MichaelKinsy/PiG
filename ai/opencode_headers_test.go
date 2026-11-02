package ai

// pi: packages/ai/src/providers/opencode-headers.ts

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

// Ported from opencode-provider-headers.test.ts "maps sessionId for %s requests even without cache retention" (stream and streamSimple), "preserves a case-insensitive caller override" (a string value and an explicit null) and "does not fabricate a session header when sessionId is absent".
func TestWithOpenCodeSessionHeaderPiCases(t *testing.T) {
	var seen []StreamOptions
	record := func(_ context.Context, _ *Model, _ TranscriptContext, options StreamOptions) (*AssistantMessageEventStream, error) {
		seen = append(seen, options)
		return nil, nil
	}
	wrapped := WithOpenCodeSessionHeader(&ProviderStreams{Stream: record, StreamSimple: record})
	calls := map[string]func(context.Context, *Model, TranscriptContext, StreamOptions) (*AssistantMessageEventStream, error){"stream": wrapped.Stream, "streamSimple": wrapped.StreamSimple}
	callerValue := "caller-value"
	custom := "value"
	for method, call := range calls {
		seen = nil
		if _, err := call(context.Background(), nil, TranscriptContext{}, StreamOptions{SessionID: "conversation-1", CacheRetention: CacheRetentionNone}); err != nil {
			t.Fatal(err)
		}
		if headers := seen[0].Headers; len(headers) != 1 || headers[openCodeSessionHeader] == nil || *headers[openCodeSessionHeader] != "conversation-1" {
			t.Fatalf("%s: headers = %v, want only %s: conversation-1", method, headers, openCodeSessionHeader)
		}
	}
	for name, headers := range map[string]ProviderHeaders{
		"string value": {"X-OpenCode-Session": &callerValue},
		"null value":   {"X-OpenCode-Session": nil},
	} {
		seen = nil
		if _, err := wrapped.StreamSimple(context.Background(), nil, TranscriptContext{}, StreamOptions{SessionID: "generated-value", Headers: headers}); err != nil {
			t.Fatal(err)
		}
		got := seen[0].Headers
		value, present := got["X-OpenCode-Session"]
		want := headers["X-OpenCode-Session"]
		if len(got) != 1 || !present || (value == nil) != (want == nil) || (want != nil && *value != *want) {
			t.Fatalf("%s: headers = %v, want the caller's %v untouched", name, got, headers)
		}
	}
	seen = nil
	if _, err := wrapped.StreamSimple(context.Background(), nil, TranscriptContext{}, StreamOptions{Headers: ProviderHeaders{"x-custom": &custom}}); err != nil {
		t.Fatal(err)
	}
	if headers := seen[0].Headers; len(headers) != 1 || headers["x-custom"] == nil || *headers["x-custom"] != "value" {
		t.Fatalf("absent sessionId: headers = %v, want only x-custom", headers)
	}
}
