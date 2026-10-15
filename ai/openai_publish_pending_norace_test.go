//go:build !race

package ai

import (
	"context"
	"strings"
	"testing"
)

// completionsTextDeltaAllocs is the allocations per record of a completions stream of text deltas, read whole by parseSSE.
func completionsTextDeltaAllocs(t *testing.T) float64 {
	const records = 512
	chunk := "data: {\"id\":\"chatcmpl-1\",\"model\":\"gpt-x\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"word \"}}]}\n\n"
	sse := strings.Repeat(chunk, records) + "data: {\"id\":\"chatcmpl-1\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n"
	provider := &openAIProvider{cfg: OpenAIConfig{Model: "gpt-x", ProviderID: "openai"}}
	return testing.AllocsPerRun(10, func() {
		builder := newAssistantStreamBuilder(context.Background(), APIOpenAICompletions, "openai", "gpt-x")
		provider.parseSSE(context.Background(), strings.NewReader(sse), builder, nil)
		if result := builder.stream.Result(); result.StopReason != StopReasonStop || len(result.Content) != 1 {
			t.Fatalf("unexpected result %+v", result)
		}
	}) / records
}

// A completions record whose changes were all pushed is not published again before the next record is read: only a direct change to the
// message (metadata, stop reason, usage, a reasoning signature) marks it for the next publication.
func TestCompletionsRecordsPublishOnlyPendingChanges(t *testing.T) {
	allocs := completionsTextDeltaAllocs(t)
	t.Logf("%.1f allocations per text delta record", allocs)
	if allocs > 14.5 {
		t.Fatalf("a text delta record costs %.1f allocations, want at most 14.5 (15.2 when every record published the message again)", allocs)
	}
}
