package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"

	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	btypes "github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"

	"github.com/MichaelKinsy/PiG/ai"
)

func main() {
	data, err := os.ReadFile("test/parity/testdata/bedrock-thinking-cases.json")
	if err != nil {
		panic(err)
	}
	var cases []struct {
		Name  string
		Base  string
		Level ai.ThinkingLevel
		Model struct {
			ID, Name         string
			ThinkingLevelMap ai.ThinkingLevelMap
		}
		Options struct {
			Region string
			Env    ai.ProviderEnv
		}
	}
	if err := json.Unmarshal(data, &cases); err != nil {
		panic(err)
	}
	for _, row := range cases {
		generated, ok := ai.LookupModel("amazon-bedrock/" + row.Base)
		if !ok {
			panic(row.Base)
		}
		model := generated.ToModel()
		if row.Model.ID != "" {
			model.ID = row.Model.ID
		}
		if row.Model.Name != "" {
			model.DisplayName = row.Model.Name
		}
		if row.Model.ThinkingLevelMap != nil {
			model.ThinkingLevelMap = row.Model.ThinkingLevelMap
		}
		env := ai.ProviderEnv{"AWS_BEDROCK_SKIP_AUTH": "1", "AWS_REGION": "", "AWS_DEFAULT_REGION": ""}
		for key, value := range row.Options.Env {
			env[key] = value
		}
		var captured *bedrockruntime.ConverseStreamInput
		sentinel := errors.New("payload captured")
		provider := ai.NewBedrockProviderWithModel(*model)
		_, err := provider.Stream(context.Background(), ai.NormalizeContext(ai.Context{SystemPrompt: "You are helpful.", Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("Hello"), Timestamp: 1}}}), ai.StreamOptions{
			Thinking: row.Level, IsReasoning: model.ProviderMeta.Reasoning, Region: row.Options.Region, Env: env,
			OnPayload: func(value any, _ *ai.Model) (any, error) {
				captured = value.(*bedrockruntime.ConverseStreamInput)
				return nil, sentinel
			},
		})
		if closeErr := provider.Close(); closeErr != nil {
			panic(closeErr)
		}
		if !errors.Is(err, sentinel) || captured == nil {
			panic(err)
		}
		fields, err := captured.AdditionalModelRequestFields.MarshalSmithyDocument()
		if err != nil {
			panic(err)
		}
		var systemCache, messageCache bool
		for _, block := range captured.System {
			if _, ok := block.(*btypes.SystemContentBlockMemberCachePoint); ok {
				systemCache = true
			}
		}
		for _, block := range captured.Messages[len(captured.Messages)-1].Content {
			if _, ok := block.(*btypes.ContentBlockMemberCachePoint); ok {
				messageCache = true
			}
		}
		if err := json.NewEncoder(os.Stdout).Encode(map[string]any{"name": row.Name, "fields": json.RawMessage(fields), "systemCache": systemCache, "messageCache": messageCache}); err != nil {
			panic(err)
		}
	}
}
