package sdk_test

import (
	"fmt"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// Pricing what compaction would drop from the branch GetBranch returns: find where Pi's findCutPoint cuts, then add up estimateTokens over the context
// messages of the entries before the cut. The expected output is what Pi's own functions print for the same branch and budget.
func ExampleFindCutPoint() {
	text := func(role, body string) map[string]any {
		message := map[string]any{"role": role, "content": body}
		if role == "assistant" {
			message = map[string]any{"role": role, "content": []any{map[string]any{"type": "text", "text": body}}, "stopReason": "stop"}
		}
		return map[string]any{"type": "message", "message": message}
	}
	// The shape of ReadonlySessionManager.GetBranch: decoded JSON session entries, oldest first.
	branch := []map[string]any{
		text("user", "first question, forty characters of text....."),
		text("assistant", "first answer, forty characters of text......."),
		text("user", "second question, forty characters of text...."),
		text("assistant", "second answer, forty characters of text......"),
	}

	cut := sdk.FindCutPoint(branch, 0, len(branch), 20)
	dropped := 0
	for _, entry := range branch[:cut.FirstKeptEntryIndex] {
		messages, err := sdk.SessionEntryToContextMessages(entry)
		if err != nil {
			panic(err)
		}
		for _, message := range messages {
			dropped += sdk.EstimateTokens(message.(map[string]any))
		}
	}
	fmt.Printf("keeps from entry %d, split turn %t, drops about %d tokens\n", cut.FirstKeptEntryIndex, cut.IsSplitTurn, dropped)
	// Output: keeps from entry 2, split turn false, drops about 24 tokens
}
