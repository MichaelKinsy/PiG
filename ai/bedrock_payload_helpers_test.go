//go:build !pig_strip_bedrock_converse_stream

package ai

import (
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
)

func captureBedrockCommand(t *testing.T, model *Model, request Context, opts StreamOptions) *bedrockruntime.ConverseStreamInput {
	t.Helper()
	for _, name := range []string{"HOME", "PIG_CODING_AGENT_DIR", "PI_CODING_AGENT_DIR", "XDG_CONFIG_HOME"} {
		t.Setenv(name, t.TempDir())
	}
	for _, name := range []string{"AWS_PROFILE", "AWS_DEFAULT_PROFILE", "AWS_CONFIG_FILE", "AWS_SHARED_CREDENTIALS_FILE", "AWS_BEARER_TOKEN_BEDROCK", "PI_CACHE_RETENTION"} {
		t.Setenv(name, "")
	}
	opts.Env = mergeProviderEnv(opts.Env, ProviderEnv{"AWS_BEDROCK_SKIP_AUTH": "1"})
	var captured *bedrockruntime.ConverseStreamInput
	opts.OnPayload = func(value any, _ *Model) (any, error) {
		captured = value.(*bedrockruntime.ConverseStreamInput)
		return nil, errors.New("payload captured")
	}
	copyModel := *model
	copyModel.ProviderMeta.BaseURL = "http://127.0.0.1:9"
	provider := NewBedrockProviderWithModel(copyModel)
	t.Cleanup(func() {
		if err := provider.Close(); err != nil {
			t.Error(err)
		}
	})
	stream, _ := provider.Stream(t.Context(), NormalizeContext(request), opts)
	if stream != nil {
		for event := range stream.Events(t.Context()) {
			if event.EventType() == EventError {
				break
			}
		}
		stream.Result()
	}
	if captured == nil {
		t.Fatal("Expected Bedrock payload to be captured before request abort")
	}
	return captured
}
