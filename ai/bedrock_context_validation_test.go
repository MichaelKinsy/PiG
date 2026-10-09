//go:build !pig_strip_bedrock_converse_stream

package ai

import (
	"context"
	"testing"
)

// validateProviderRequest rejects a nil Go context. Constructing stream state must not panic before that public boundary check.
func TestBedrockRequestContextValidation(t *testing.T) {
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	for _, tc := range []struct {
		name    string
		ctx     context.Context
		wantErr string
	}{
		{"nil context", nil, "amazon-bedrock: invalid transcript: provider context is nil"},
		{"canceled context", canceled, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			isolateBedrockConfig(t)
			provider := NewBedrockProvider("us.anthropic.claude-opus-4-8", "http://127.0.0.1:9")
			stream, err := provider.Stream(tc.ctx, NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("hello")}}}), StreamOptions{Env: ProviderEnv{"AWS_BEDROCK_SKIP_AUTH": "1"}})
			if tc.wantErr != "" {
				if err == nil || err.Error() != tc.wantErr || stream != nil {
					t.Fatalf("stream=%v error=%v, want nil stream and %q", stream, err, tc.wantErr)
				}
				return
			}
			if err != nil || stream == nil {
				t.Fatalf("stream=%v error=%v, want an aborted stream", stream, err)
			}
			if result := stream.Result(); result.StopReason != StopReasonAborted || result.ErrorMessage != "Request aborted" {
				t.Fatalf("result=%+v", result)
			}
		})
	}
}
