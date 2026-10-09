package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"

	"github.com/MichaelKinsy/PiG/ai"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		if strings.HasPrefix(r.URL.Path, "/timeout") {
			<-r.Context().Done()
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"service_tier\":\"default\",\"usage\":{\"input_tokens\":1000000,\"output_tokens\":1000000,\"total_tokens\":2000000}}}\n\n")
	}))
	defer server.Close()
	token := "aaa." + base64.StdEncoding.EncodeToString([]byte(`{"https://api.openai.com/auth":{"chatgpt_account_id":"acc_test"}}`)) + ".bbb"
	model := &ai.Model{ID: "gpt-5.5", DisplayName: "Mapped Codex", ThinkingLevelMap: ai.ThinkingLevelMap{ai.ThinkingMinimal: new("low"), ai.ThinkingXHigh: new("xhigh")}, Capabilities: ai.ModelCapabilities{MaxThinking: ai.ThinkingLevelXHigh}}
	cfg := ai.OpenAICodexResponsesConfig{APIKey: token, Model: model.ID, ModelMetadata: model, ProviderID: "openai-codex", BaseURL: server.URL}
	for _, tier := range []string{"flex", "priority"} {
		provider := ai.NewOpenAICodexResponsesProvider(cfg)
		var tool ai.ToolSchema
		if err := json.Unmarshal([]byte(`{"name":"optional","parameters":{"type":"object","properties":{"value":{"type":"string"}},"required":["value"]},"constrainedSampling":false}`), &tool); err != nil {
			return err
		}
		var payload struct {
			Reasoning struct{ Effort string }
			Tools     []struct{ Strict json.RawMessage }
		}
		stream, err := provider.Stream(context.Background(), ai.NormalizeContext(ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("Hi"), Timestamp: 1}}, Tools: []ai.ToolSchema{tool}}), ai.StreamOptions{Transport: ai.TransportSSE, ReasoningEffort: "minimal", ServiceTier: tier, ModelCost: ai.ModelCost{Input: 1, Output: 2}, OnPayload: func(value any, _ *ai.Model) (any, error) {
			data, err := json.Marshal(value)
			if err == nil {
				err = json.Unmarshal(data, &payload)
			}
			return nil, err
		}})
		if err != nil {
			return err
		}
		result := stream.Result()
		_ = provider.Close()
		if err := json.NewEncoder(os.Stdout).Encode(struct {
			Tier   string          `json:"tier"`
			Effort string          `json:"effort"`
			Strict json.RawMessage `json:"strict"`
			Cost   float64         `json:"cost"`
			Stop   ai.StopReason   `json:"stop"`
		}{tier, payload.Reasoning.Effort, payload.Tools[0].Strict, result.Usage.Cost.Total, result.StopReason}); err != nil {
			return err
		}
	}
	cfg.BaseURL = server.URL + "/timeout"
	provider := ai.NewOpenAICodexResponsesProvider(cfg)
	defer func() { _ = provider.Close() }()
	// pi.mjs prints the stream result's errorMessage and stopReason; stream() returns before the request settles.
	stream, err := provider.Stream(context.Background(), ai.NormalizeContext(ai.Context{}), ai.StreamOptions{Transport: ai.TransportSSE, TimeoutMs: new(10)})
	if err != nil {
		return err
	}
	result := stream.Result()
	if result.StopReason != ai.StopReasonError {
		return fmt.Errorf("expected header timeout")
	}
	return json.NewEncoder(os.Stdout).Encode(struct {
		Timeout string        `json:"timeout"`
		Stop    ai.StopReason `json:"stop"`
	}{result.ErrorMessage, result.StopReason})
}
