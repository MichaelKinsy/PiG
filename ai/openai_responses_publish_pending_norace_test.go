//go:build !race

package ai

import (
	"context"
	"strings"
	"testing"
)

// A Responses text delta record changes the message only by its push: it is not published again before the next record is read.
func TestResponsesRecordsPublishOnlyPendingChanges(t *testing.T) {
	const records = 512
	head := "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_1\"}}\n\n" +
		"data: {\"type\":\"response.output_item.added\",\"output_index\":0,\"item\":{\"type\":\"message\",\"id\":\"msg_1\",\"role\":\"assistant\",\"content\":[]}}\n\n"
	delta := "data: {\"type\":\"response.output_text.delta\",\"output_index\":0,\"delta\":\"word \"}\n\n"
	tail := "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_1\",\"status\":\"completed\"}}\n\n"
	sse := head + strings.Repeat(delta, records) + tail
	provider := &openAIResponsesProvider{cfg: OpenAIResponsesConfig{ProviderID: "openai", Model: "gpt-5"}}
	allocs := testing.AllocsPerRun(10, func() {
		builder := newAssistantStreamBuilder(context.Background(), APIOpenAIResponses, "openai", "gpt-5")
		provider.parseResponsesSSE(context.Background(), strings.NewReader(sse), builder, nil)
		if result := builder.stream.Result(); result.StopReason != StopReasonStop {
			t.Fatalf("unexpected result %+v", result)
		}
	}) / records
	t.Logf("%.1f allocations per text delta record", allocs)
	if allocs > 11.5 {
		t.Fatalf("a text delta record costs %.1f allocations, want at most 11.5 (12.2 when every record published the message again)", allocs)
	}
}
