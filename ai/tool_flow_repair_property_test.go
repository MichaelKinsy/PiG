package ai

import (
	"math/rand"
	"testing"
)

// intactToolFlow reports whether every call of a turn is answered exactly once, right after the turn, and every result answers such a call.
func intactToolFlow(messages []Message) bool {
	open := map[string]int{}
	pending := 0
	for _, message := range messages {
		switch typed := message.(type) {
		case AssistantMessage:
			if pending > 0 {
				return false
			}
			clear(open)
			for _, block := range typed.Content {
				if call, ok := block.(ToolCall); ok {
					open[call.ID]++
					pending++
				}
			}
		case ToolResultMessage:
			if open[typed.ToolCallID] == 0 {
				return false
			}
			open[typed.ToolCallID]--
			pending--
		default:
			if pending > 0 {
				return false
			}
		}
	}
	return true
}

// The repair changes only a request Pi would send with a broken tool flow, and it mends every such request whose turns do not repeat a call id. A request Pi sends intact reaches the provider unchanged, even when a provider reuses call ids every turn.
func TestRepairToolFlowChangesOnlyBrokenRequests(t *testing.T) {
	model := &Model{ID: "m", Input: []string{"text", "image"}}
	ids := []string{"a", "b", "c"}
	stops := []StopReason{StopReasonToolUse, StopReasonStop, StopReasonError, StopReasonAborted}
	for seed := range 20000 {
		random := rand.New(rand.NewSource(int64(seed)))
		var messages []Message
		repeatsID := false
		for range 1 + random.Intn(8) {
			switch random.Intn(5) {
			case 0:
				messages = append(messages, UserMessage{Content: UserContentBlocks{TextContent{Text: "u"}}})
			case 1, 2:
				assistant := AssistantMessage{StopReason: stops[random.Intn(len(stops))], Model: "m"}
				seen := map[string]bool{}
				for range random.Intn(3) {
					id := ids[random.Intn(len(ids))]
					repeatsID = repeatsID || seen[id] && assistant.StopReason != StopReasonError && assistant.StopReason != StopReasonAborted
					seen[id] = true
					assistant.Content = append(assistant.Content, ToolCall{ID: id, Name: "t"})
				}
				messages = append(messages, assistant)
			case 3:
				messages = append(messages, ToolResultMessage{ToolCallID: ids[random.Intn(len(ids))], ToolName: "t"})
			case 4:
				messages = append(messages, SystemMessage{Content: SystemText("s")})
			}
		}
		pi := TransformMessages(messages, model, nil)
		repaired := TransformMessages(RepairToolFlow(messages), model, nil)
		switch {
		case intactToolFlow(pi) && shape(pi) != shape(repaired):
			t.Fatalf("seed %d changed a request Pi sends intact\nin   %s\npi   %s\ngot  %s", seed, shape(messages), shape(pi), shape(repaired))
		case !repeatsID && !intactToolFlow(repaired):
			t.Fatalf("seed %d left a broken tool flow\nin   %s\ngot  %s", seed, shape(messages), shape(repaired))
		}
	}
}
