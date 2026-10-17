//go:build !race

package coding

import (
	"fmt"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/perfgate"
)

// streamTestDetails is a tool result's details as a built-in tool returns them: a struct, which normalization turns into decoded JSON that is not plain, so validating it again marshals it again.
type streamTestDetails struct {
	Path  string `json:"path"`
	Lines int    `json:"lines"`
}

// The Session's stream function neither copies nor validates the transcript again. Pi's streamFn hands the agent's context straight to modelRuntime.streamSimple (packages/coding-agent/src/core/sdk.ts:420-430) and normalizeContext shares context.messages (packages/ai/src/utils/transcript.ts:30-34), so Pi copies nothing. The agent loop's transcript is validated and immutable, so the request shares it (TranscriptContext.FrozenContext, D104) and the provider's request check skips it. The transcript carries tool results whose details would be marshaled again by a second validation. The denominator is the measured cost of one Messages copy, so the bound does not depend on how a message is cloned.
func TestSessionStreamSharesTheTranscript(t *testing.T) {
	sess, provider := newWarmingSession(t, "off", SessionOptions{NoSession: true})
	stream := cacheWarmingStreamFn(func() *Session { return sess })
	model := sess.Agent().Model()
	const n = 400
	transcripts := map[int]ai.TranscriptContext{}
	for _, size := range []int{n, 2 * n} {
		messages := make([]ai.Message, 0, size)
		for i := range size {
			id := fmt.Sprintf("call-%d", i)
			switch i % 4 {
			case 0:
				messages = append(messages, ai.UserMessage{Content: ai.UserContentBlocks{ai.TextContent{Text: fmt.Sprintf("question %d", i)}}})
			case 1:
				messages = append(messages, ai.AssistantMessage{API: model.ProviderMeta.API, Provider: "capture-provider", Model: model.ID, Content: []ai.AssistantContentBlock{ai.ToolCall{ID: id, Name: "read", Arguments: ai.JsonObject{"path": "f"}}}, StopReason: ai.StopReasonToolUse})
			case 2:
				messages = append(messages, ai.ToolResultMessage{ToolCallID: fmt.Sprintf("call-%d", i-1), ToolName: "read", Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "ok"}}, Details: streamTestDetails{Path: "f", Lines: i}})
			default:
				messages = append(messages, ai.AssistantMessage{API: model.ProviderMeta.API, Provider: "capture-provider", Model: model.ID, Content: []ai.AssistantContentBlock{ai.TextContent{Text: fmt.Sprintf("answer %d", i)}}, StopReason: ai.StopReasonStop})
			}
		}
		transcripts[size] = ai.NormalizeContext(ai.Context{Messages: messages})
		if transcripts[size].Len() != size {
			t.Fatalf("transcript of %d messages failed to normalize", size)
		}
	}
	request := perfgate.Marginal(n, func(units int) {
		// A request that is not the Session's own does not start warming (sdk.ts:427), so only the request itself is measured.
		response, err := stream(t.Context(), model, transcripts[units], ai.StreamOptions{SessionID: "other"})
		if err != nil {
			t.Fatal(err)
		}
		if result := response.Result(); result.StopReason != ai.StopReasonStop {
			t.Fatalf("request failed: %+v", result)
		}
	})
	copied := perfgate.Marginal(n, func(units int) { _ = transcripts[units].Messages() })
	t.Logf("allocations per message: request %.2f, one Messages copy %.2f", request.Allocs, copied.Allocs)
	if copied.Allocs < 1 {
		t.Fatalf("one Messages copy allocates %.2f per message; the bound needs a copy that allocates", copied.Allocs)
	}
	if request.Allocs > 0.05*copied.Allocs {
		t.Errorf("a request allocates %.2f per transcript message, %.2f Messages copies; the stream function must share the validated transcript, not copy or validate it again", request.Allocs, request.Allocs/copied.Allocs)
	}
	if calls := len(provider.calls()); calls != 4 {
		t.Fatalf("provider calls = %d, want the four measured requests", calls)
	}
}
