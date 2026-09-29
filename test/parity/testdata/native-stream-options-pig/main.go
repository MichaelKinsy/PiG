package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"

	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"

	"github.com/MichaelKinsy/PiG/ai"
)

func main() {
	captured := errors.New("captured before network")
	var networkCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		networkCalls.Add(1)
		http.Error(w, "payload escaped capture", http.StatusBadRequest)
	}))
	defer server.Close()
	rows := [][]any{}
	for _, tc := range []struct{ model, raw string }{
		{"claude-haiku-4-5", `{"thinkingEnabled":true}`},
		{"claude-haiku-4-5", `{"thinkingEnabled":true,"thinkingBudgetTokens":2048}`},
		{"claude-opus-4-6", `{"thinkingEnabled":true,"effort":"medium"}`},
	} {
		var opts ai.StreamOptions
		if err := json.Unmarshal([]byte(tc.raw), &opts); err != nil {
			panic(err)
		}
		opts.OnPayload = func(payload any, _ *ai.Model) (any, error) {
			data, err := json.Marshal(payload)
			if err != nil {
				return nil, err
			}
			var body map[string]any
			if err := json.Unmarshal(data, &body); err != nil {
				return nil, err
			}
			thinking, _ := body["thinking"].(map[string]any)
			config, _ := body["output_config"].(map[string]any)
			rows = append(rows, []any{tc.model, thinking["type"], thinking["budget_tokens"], thinking["display"], config["effort"]})
			return nil, captured
		}
		provider := ai.NewAnthropicProvider(ai.AnthropicConfig{APIKey: "test", Model: tc.model, BaseURL: server.URL})
		before := len(rows)
		stream, err := provider.Stream(context.Background(), ai.NormalizeContext(ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("hi")}}}), opts)
		if err := requireCapturedPayload(stream, err, captured); err != nil {
			panic(err)
		}
		if len(rows) != before+1 {
			panic("payload callback must run exactly once")
		}
	}
	for _, metadata := range []map[string]string{{"app": "pi-test", "env": "ci"}, nil} {
		opts := ai.StreamOptions{Env: ai.ProviderEnv{"AWS_BEDROCK_SKIP_AUTH": "1", "AWS_REGION": "us-east-1"}, RequestMetadata: metadata}
		opts.OnPayload = func(payload any, _ *ai.Model) (any, error) {
			input := payload.(*bedrockruntime.ConverseStreamInput)
			rows = append(rows, []any{"bedrock", input.RequestMetadata})
			return nil, captured
		}
		before := len(rows)
		stream, err := ai.NewBedrockProvider("global.anthropic.claude-sonnet-4-5-20250929-v1:0", server.URL).Stream(context.Background(), ai.NormalizeContext(ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("Say hi.")}}}), opts)
		if err := requireCapturedPayload(stream, err, captured); err != nil {
			panic(err)
		}
		if len(rows) != before+1 {
			panic("payload callback must run exactly once")
		}
	}
	if networkCalls.Load() != 0 {
		panic("payload callback allowed a network request")
	}
	if err := json.NewEncoder(os.Stdout).Encode(rows); err != nil {
		panic(err)
	}
}

// Go setup failures may return directly; rejected payload callbacks may instead terminate a created stream. Neither path may accept an unrelated failure or an unconsumed result.
func requireCapturedPayload(stream *ai.AssistantMessageEventStream, err, captured error) error {
	if err != nil {
		if !errors.Is(err, captured) {
			return fmt.Errorf("unexpected pre-network error: %w", err)
		}
		return nil
	}
	if stream == nil {
		return errors.New("payload capture returned no stream or error")
	}
	result := stream.Result()
	if result == nil || result.StopReason != ai.StopReasonError || result.ErrorMessage != captured.Error() {
		return fmt.Errorf("unexpected capture result: %+v", result)
	}
	return nil
}
