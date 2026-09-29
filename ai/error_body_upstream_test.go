package ai

import (
	"encoding/json"
	"strings"
	"testing"
)

// Go's HTTP adapters own one typed carrier instead of TS SDK-specific property bags. The cases retain the exact raw status/body/message values and normalization assertions.
func TestNormalizeProviderErrorUpstream(t *testing.T) {
	for _, tc := range []struct {
		name, message string
		status        *int
		body          any
		wantBody      *string
		carries       bool
	}{
		// .upstream/v0.87.1/packages/ai/test/error-body.test.ts:12
		{"extracts status and body from a Mistral-shaped error", "Mistral request failed", new(403), `{"error":"blocked by gateway WAF"}`, new(`{"error":"blocked by gateway WAF"}`), false},
		// .upstream/v0.87.1/packages/ai/test/error-body.test.ts:25
		{"reads the parsed body off an openai APIError when the message is opaque", "403 status code (no body)", new(403), json.RawMessage(`{"error":"blocked by gateway WAF"}`), new(`{"error":"blocked by gateway WAF"}`), false},
		// .upstream/v0.87.1/packages/ai/test/error-body.test.ts:40
		{"preserves the message when @google/genai already folds the body into it", `{"error":{"code":403,"message":"Permission denied"}}`, new(403), nil, nil, true},
		// .upstream/v0.87.1/packages/ai/test/error-body.test.ts:53
		{"extracts status and body from a Bedrock-shaped ServiceException", "UnknownError", new(403), `{"message":"blocked by gateway WAF"}`, new(`{"message":"blocked by gateway WAF"}`), false},
		// .upstream/v0.87.1/packages/ai/test/error-body.test.ts:67
		{"ignores a Bedrock response stream instead of serializing its internals", "Invocation of model ID anthropic.claude-opus-5 with on-demand throughput isn't supported.", new(400), strings.NewReader("unread stream internals"), nil, true},
		// .upstream/v0.87.1/packages/ai/test/error-body.test.ts:88
		{"ignores a class-instance response body without a pipe method instead of serializing it", "Input is too long for requested model.", new(400), struct {
			Locked bool
			State  map[string]any
		}{State: map[string]any{"storedError": nil}}, nil, true},
		// .upstream/v0.87.1/packages/ai/test/error-body.test.ts:110
		{"ignores a class-instance error field instead of serializing it", "TLS handshake failed", new(502), struct {
			Code          string
			InternalState map[string]any
		}{Code: "EPROTO", InternalState: map[string]any{}}, nil, true},
		// .upstream/v0.87.1/packages/ai/test/error-body.test.ts:127
		{"still surfaces a plain parsed JSON body object", "400 status code (no body)", new(400), json.RawMessage(`{"message":"schema validation failed","field":"tools[0]"}`), new(`{"message":"schema validation failed","field":"tools[0]"}`), false},
		// .upstream/v0.87.1/packages/ai/test/error-body.test.ts:148
		{"treats an empty parsed body object as no body", "403 status code (no body)", new(403), map[string]any{}, nil, true},
		// .upstream/v0.87.1/packages/ai/test/error-body.test.ts:173
		{"sets messageCarriesBody when the message already contains the extracted body", "500: upstream exploded", new(500), "upstream exploded", new("upstream exploded"), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := NormalizeProviderError(&providerError{message: tc.message, status: tc.status, body: tc.body})
			if got.Status == nil || *got.Status != *tc.status || got.Message != tc.message || got.MessageCarriesBody != tc.carries {
				t.Fatalf("normalized=%+v", got)
			}
			if (got.Body == nil) != (tc.wantBody == nil) || (got.Body != nil && *got.Body != *tc.wantBody) {
				t.Fatalf("body=%v want=%v", got.Body, tc.wantBody)
			}
		})
	}
	// .upstream/v0.87.1/packages/ai/test/error-body.test.ts:139
	t.Run("JSON-stringifies a non-Error thrown value", func(t *testing.T) {
		got := NormalizeProviderError(map[string]any{"reason": "boom"})
		if got.Status != nil || got.Body != nil || got.Message != `{"reason":"boom"}` || got.MessageCarriesBody {
			t.Fatalf("normalized=%+v", got)
		}
	})
	// .upstream/v0.87.1/packages/ai/test/error-body.test.ts:160
	t.Run("truncates the body at the cap", func(t *testing.T) {
		body := strings.Repeat("x", MaxProviderErrorBodyChars+50)
		got := NormalizeProviderError(&providerError{message: "failed", status: new(500), body: body})
		if got.Body == nil || !strings.Contains(*got.Body, "... [truncated 50 chars]") || len(*got.Body) >= len(body) {
			t.Fatalf("body=%v", got.Body)
		}
	})
}

func TestProviderErrorTrimmingMatchesJS(t *testing.T) {
	// error-body.ts:extractBody uses String.trim: BOM is whitespace, but NEL is not.
	for _, test := range []struct {
		body string
		want *string
	}{
		{"  body\t\n", new("body")},
		{"\ufeffbody\ufeff", new("body")},
		{"\ufeff", nil},
		{"\u0085body\u0085", new("\u0085body\u0085")},
		{"\u0085", new("\u0085")},
	} {
		got := NormalizeProviderError(&providerError{message: "opaque", status: new(400), body: test.body})
		if (got.Body == nil) != (test.want == nil) {
			t.Errorf("body %q presence=%v, want %v", test.body, got.Body != nil, test.want != nil)
		} else if got.Body != nil && *got.Body != *test.want {
			t.Errorf("body %q normalized to %q, want %q", test.body, *got.Body, *test.want)
		}
	}
}

func TestFormatProviderErrorUpstream(t *testing.T) {
	opaque := NormalizeProviderError(&providerError{message: "403 status code (no body)", status: new(403), body: json.RawMessage(`{"error":"blocked by gateway WAF"}`)})
	// .upstream/v0.87.1/packages/ai/test/error-body.test.ts:186
	t.Run("surfaces status and body without a prefix", func(t *testing.T) {
		got := FormatProviderError(opaque)
		if !strings.Contains(got, "403") || !strings.Contains(got, "blocked by gateway WAF") || got == "403 status code (no body)" {
			t.Fatal(got)
		}
	})
	// .upstream/v0.87.1/packages/ai/test/error-body.test.ts:201
	t.Run("applies a provider prefix with status and body", func(t *testing.T) {
		if got := FormatProviderError(opaque, "OpenAI API error"); got != `OpenAI API error (403): {"error":"blocked by gateway WAF"}` {
			t.Fatal(got)
		}
	})
	// .upstream/v0.87.1/packages/ai/test/error-body.test.ts:214
	t.Run("preserves the message with prefix and status when it already carries the body", func(t *testing.T) {
		body := `{"error":{"message":"Permission denied"}}`
		norm := NormalizeProviderError(&providerError{message: body, status: new(403)})
		if got := FormatProviderError(norm, "OpenAI API error"); got != "OpenAI API error (403): "+body {
			t.Fatal(got)
		}
	})
	// .upstream/v0.87.1/packages/ai/test/error-body.test.ts:221
	t.Run("returns the bare message for a non-Error value", func(t *testing.T) {
		if got := FormatProviderError(NormalizeProviderError(map[string]any{"reason": "boom"})); got != `{"reason":"boom"}` {
			t.Fatal(got)
		}
	})
}
