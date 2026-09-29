package ai

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestAzureOpenAIResponsesReasoningReplayUpstream(t *testing.T) {
	for _, tc := range []struct{ name, id, done, want string }{
		// .upstream/v0.87.1/packages/ai/test/azure-openai-responses-reasoning-replay.test.ts:83
		{"preserves existing encrypted_content from output_item.done", "rs_done", "from-output-item-done", "from-output-item-done"},
		// .upstream/v0.87.1/packages/ai/test/azure-openai-responses-reasoning-replay.test.ts:111
		{"fills encrypted_content when output_item.done omitted it", "rs_missing", "", "from-response-completed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			done := map[string]any{"type": "reasoning", "id": tc.id, "summary": []any{}}
			if tc.done != "" {
				done["encrypted_content"] = tc.done
			}
			doneJSON, err := json.Marshal(done)
			if err != nil {
				t.Fatal(err)
			}
			sse := fmt.Sprintf("data: {\"type\":\"response.output_item.added\",\"output_index\":0,\"sequence_number\":0,\"item\":{\"type\":\"reasoning\",\"id\":%q,\"summary\":[]}}\n\ndata: {\"type\":\"response.output_item.done\",\"output_index\":0,\"sequence_number\":1,\"item\":%s}\n\ndata: {\"type\":\"response.completed\",\"sequence_number\":2,\"response\":{\"id\":\"resp_test\",\"status\":\"completed\",\"output\":[{\"type\":\"reasoning\",\"id\":%q,\"summary\":[],\"encrypted_content\":\"from-response-completed\"}]}}\n\n", tc.id, doneJSON, tc.id)
			provider := &openAIResponsesProvider{cfg: OpenAIResponsesConfig{ProviderID: "azure-openai-responses", Model: "gpt-5-mini"}}
			builder := newAssistantStreamBuilder(t.Context(), APIAzureOpenAIResponses, "azure-openai-responses", "gpt-5-mini")
			provider.parseResponsesSSE(t.Context(), strings.NewReader(sse), builder, nil)
			output := builder.stream.Result()
			input, err := provider.convertMessages([]Message{UserMessage{Content: UserText("first")}, *output, UserMessage{Content: UserText("follow-up")}}, nil)
			if err != nil {
				t.Fatal(err)
			}
			for _, item := range input {
				if item.Type == "reasoning" {
					var encrypted string
					if err := json.Unmarshal(item.EncryptedContent, &encrypted); err != nil {
						t.Fatal(err)
					}
					if item.ID != tc.id || encrypted != tc.want {
						t.Fatalf("reasoning=%#v, want %q", item, tc.want)
					}
					return
				}
			}
			t.Fatalf("missing replay reasoning: %#v", input)
		})
	}
}
