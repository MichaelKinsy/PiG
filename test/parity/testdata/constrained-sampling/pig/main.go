package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"

	"github.com/MichaelKinsy/PiG/ai"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	body, err := os.ReadFile("test/parity/testdata/constrained-sampling/events.sse")
	if err != nil {
		return err
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write(body)
	}))
	defer server.Close()
	provider := ai.NewOpenAIResponsesProvider(ai.OpenAIResponsesConfig{APIKey: "test", Model: "gpt-test", ProviderID: "openai", BaseURL: server.URL, Compat: &ai.OpenAIResponsesCompat{SupportsOpenAIGrammarTools: new(true)}})
	tools := []ai.ToolSchema{{Name: "sample_tool", Description: "Sample tool", Parameters: map[string]any{"type": "object", "properties": map[string]any{"payload": map[string]any{"type": "string"}}, "required": []string{"payload"}, "additionalProperties": false}, ConstrainedSampling: &ai.ConstrainedSamplingConfig{Type: "grammar", Variants: map[string]string{"openai_lark": "start: /[a-z]+/"}}}}
	stream, err := provider.Stream(context.Background(), ai.NormalizeContext(ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("hi")}}, Tools: tools}), ai.StreamOptions{})
	if err != nil {
		return err
	}
	starts, partials := []ai.JsonObject{}, []ai.JsonObject{}
	deltas := ""
	for event := range stream.Events(context.Background()) {
		switch event := event.(type) {
		case ai.ToolCallStartEvent:
			starts = append(starts, event.Partial.Observe().Content[event.ContentIndex].(ai.ToolCall).Arguments)
		case ai.ToolCallDeltaEvent:
			deltas += event.Delta
			partials = append(partials, event.Partial.Observe().Content[event.ContentIndex].(ai.ToolCall).Arguments)
		}
	}
	result := stream.Result()
	if result.StopReason == ai.StopReasonError {
		return errors.New(result.ErrorMessage)
	}
	call := result.Content[0].(ai.ToolCall)
	replay := ai.Context{Tools: tools, Messages: []ai.Message{*result, ai.ToolResultMessage{ToolCallID: call.ID, ToolName: call.Name, Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "done"}}}}}
	capture := func(request ai.Context) (any, error) {
		var input any
		sentinel := errors.New("captured")
		_, err := provider.Stream(context.Background(), ai.NormalizeContext(request), ai.StreamOptions{OnPayload: func(payload any, _ *ai.Model) (any, error) {
			raw, err := json.Marshal(payload)
			if err != nil {
				return nil, err
			}
			var object map[string]any
			if err := json.Unmarshal(raw, &object); err != nil {
				return nil, err
			}
			input = object["input"]
			return nil, sentinel
		}})
		if !errors.Is(err, sentinel) {
			return nil, fmt.Errorf("payload capture: %w", err)
		}
		return input, nil
	}
	native, err := capture(replay)
	if err != nil {
		return err
	}
	foreign := *result
	foreign.Provider = "other"
	foreign.API = ai.APIOpenAICompletions
	foreign.Model = "source"
	foreignCall := call
	foreignCall.ID = "call 1|fc_native"
	foreignCall.Namespace = "functions"
	foreign.Content = []ai.AssistantContentBlock{foreignCall}
	replay.Messages = []ai.Message{foreign, ai.ToolResultMessage{ToolCallID: foreignCall.ID, ToolName: call.Name, Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "done"}}}}
	foreignInput, err := capture(replay)
	if err != nil {
		return err
	}
	var deltaArgs any
	if err := json.Unmarshal([]byte(deltas), &deltaArgs); err != nil {
		return err
	}
	rawTool, err := os.ReadFile("test/parity/testdata/constrained-sampling/ordered-tool.json")
	if err != nil {
		return err
	}
	var orderedTool ai.ToolSchema
	if err := json.Unmarshal(rawTool, &orderedTool); err != nil {
		return err
	}
	strictProvider := ai.NewOpenAIResponsesProvider(ai.OpenAIResponsesConfig{APIKey: "test", Model: "gpt-test", Compat: &ai.OpenAIResponsesCompat{SupportsStrictMode: new(true)}})
	var required any
	captured := errors.New("captured")
	_, err = strictProvider.Stream(context.Background(), ai.NormalizeContext(ai.Context{Tools: []ai.ToolSchema{orderedTool}}), ai.StreamOptions{OnPayload: func(payload any, _ *ai.Model) (any, error) {
		raw, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		var object map[string]any
		if err := json.Unmarshal(raw, &object); err != nil {
			return nil, err
		}
		required = object["tools"].([]any)[0].(map[string]any)["parameters"].(map[string]any)["required"]
		return nil, captured
	}})
	if !errors.Is(err, captured) {
		return fmt.Errorf("strict capture: %w", err)
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]any{"strictRequired": required, "starts": starts, "partials": partials, "deltaArguments": deltaArgs, "finalArguments": call.Arguments, "reason": result.StopReason, "replay": native, "foreignReplay": foreignInput})
}
