package ai

import (
	"strings"
	"sync"
	"testing"
)

func fauxHistory(turns int) TranscriptContext {
	messages := make([]Message, 0, turns*3)
	for i := range turns {
		messages = append(messages,
			UserMessage{Content: UserText("question " + strings.Repeat("x", 40))},
			AssistantMessage{Content: []AssistantContentBlock{ToolCall{ID: "c", Name: "lookup", Arguments: JsonObject{"n": float64(i)}}}},
			ToolResultMessage{ToolCallID: "c", ToolName: "lookup", Content: []ToolResultMessageContent{TextContent{Text: strings.Repeat("y", 256)}}},
		)
	}
	return NormalizeContext(Context{Messages: messages})
}

// Requests of one session may overlap: each holds the session's prompt entry for its build and swap, so the storage one
// request reads as its previous prompt is never the storage another writes. Run under -race.
func TestFauxEstimateConcurrentRequestsOfOneSession(t *testing.T) {
	provider := NewFauxProvider(FauxConfig{})
	request := fauxHistory(50)
	options := StreamOptions{SessionID: "s"}
	content := []FauxContentBlock{FauxText("ok")}
	var group sync.WaitGroup
	for range 8 {
		group.Go(func() {
			for range 50 {
				usage := provider.estimateUsage(request, options, content)
				if usage.TotalTokens != usage.Input+usage.Output+usage.CacheRead+usage.CacheWrite || usage.Input < 0 || usage.CacheRead < 0 || usage.CacheWrite < 0 {
					t.Errorf("inconsistent usage %+v", usage)
					return
				}
			}
		})
	}
	group.Wait()
}
