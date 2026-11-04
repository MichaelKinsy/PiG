//go:build !pig_strip_bedrock_converse_stream

package bedrock

import (
	"github.com/MichaelKinsy/PiG/ai"

	"errors"
	"testing"

	"github.com/MichaelKinsy/PiG/third_party/awssdk/service/bedrockruntime"
	bedrocktypes "github.com/MichaelKinsy/PiG/third_party/awssdk/service/bedrockruntime/types"
)

// A Bedrock model outside the catalog is still a bedrock-converse-stream model of provider amazon-bedrock, as every Pi Model is: transformMessages'
// isSameModel compares provider, api and id, so an assistant message of that provider, api and id keeps its tool-call ids (normalizeToolCallId runs only for another model's calls).
func TestBedrockUncatalogedModelIsTheSameModelAsItsOwnReplay(t *testing.T) {
	const model = "example.uncataloged-model-v1:0"
	if _, ok := ai.LookupModel("amazon-bedrock/" + model); ok {
		t.Fatalf("%s is in the catalog", model)
	}
	for _, name := range []string{"HOME", "PIG_CODING_AGENT_DIR", "PI_CODING_AGENT_DIR", "XDG_CONFIG_HOME"} {
		t.Setenv(name, t.TempDir())
	}
	for _, name := range []string{"AWS_PROFILE", "AWS_DEFAULT_PROFILE", "AWS_CONFIG_FILE", "AWS_SHARED_CREDENTIALS_FILE", "AWS_BEARER_TOKEN_BEDROCK"} {
		t.Setenv(name, "")
	}
	var captured *bedrockruntime.ConverseStreamInput
	opts := ai.StreamOptions{Env: ai.ProviderEnv{"AWS_BEDROCK_SKIP_AUTH": "1"}, OnPayload: func(value any, _ *ai.Model) (any, error) {
		captured = value.(*bedrockruntime.ConverseStreamInput)
		return nil, errors.New("payload captured")
	}}
	provider := NewBedrockProviderWithName(model, "Uncataloged", "http://127.0.0.1:9")
	defer func() { _ = provider.Close() }()
	stream, err := provider.Stream(t.Context(), ai.NormalizeContext(ai.Context{Messages: []ai.Message{
		ai.UserMessage{Content: ai.UserText("hi")},
		ai.AssistantMessage{Provider: "amazon-bedrock", API: ai.APIBedrockConverseStream, Model: model, Content: []ai.AssistantContentBlock{
			ai.ToolCall{ID: "call|with|bars", Name: "lookup", Arguments: ai.JsonObject{"q": "x"}},
		}},
		ai.ToolResultMessage{ToolCallID: "call|with|bars", ToolName: "lookup", Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "found"}}},
	}}), opts)
	if stream != nil {
		for range stream.Events(t.Context()) {
		}
	} else if captured == nil {
		t.Fatal(err)
	}
	if captured == nil {
		t.Fatal("payload was not captured")
	}
	var id string
	for _, message := range captured.Messages {
		for _, block := range message.Content {
			if use, ok := block.(*bedrocktypes.ContentBlockMemberToolUse); ok && use.Value.ToolUseId != nil {
				id = *use.Value.ToolUseId
			}
		}
	}
	if id != "call|with|bars" {
		t.Fatalf("toolUseId = %q, want the same model's id kept unnormalized", id)
	}
}
