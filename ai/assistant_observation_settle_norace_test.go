//go:build !race

package ai

import (
	"fmt"
	"strings"
	"testing"
)

// A cell that publishes a message of many finished tool calls, and settles it for a reader, allocates for each event what the event itself needs: the calls it leaves alone are neither parsed, copied nor boxed again, so the allocations of an event do not depend on how many calls the message holds.
func TestSettledToolCallsAreNotParsedCopiedOrBoxedAgain(t *testing.T) {
	var member strings.Builder
	member.WriteByte('{')
	for i := range 20 {
		fmt.Fprintf(&member, `"member%d":{"path":"file%d.go","lines":[1,2,3]},`, i, i)
	}
	member.WriteString(`"last":true}`)
	deferred := func(id, text string) ToolCall {
		call := ToolCall{ID: id, Name: "tool", Arguments: JsonObject{}}
		call.deferArguments(text)
		return call
	}
	allocations := func(calls int) float64 {
		partial := &AssistantMessage{API: APIOpenAICompletions, Provider: "p", Model: "m", StopReason: StopReasonPending}
		for call := range calls {
			partial.Content = append(partial.Content, deferred(fmt.Sprintf("call_%d", call), member.String()))
		}
		partial.Content = append(partial.Content, deferred("open", `{"v":"`))
		cell := newAssistantMessageCell(partial)
		cell.view() // settles every call once
		step := 0
		return testing.AllocsPerRun(50, func() {
			step++
			partial.Content[calls] = deferred("open", `{"v":"`+strings.Repeat("x", step))
			cell.publish(partial, assistantMessageReplacements{})
			exclusive := cell.readLock(nil)
			cell.readUnlock(exclusive)
		})
	}
	small, large := allocations(25), allocations(250)
	t.Logf("an event of a message of 25 finished calls allocates %.0f times, of 250 finished calls %.0f", small, large)
	if large > small+4 {
		t.Fatalf("an event of 250 finished calls allocates %.0f times, of 25 calls %.0f: the finished calls are handled again by each event", large, small)
	}
}
