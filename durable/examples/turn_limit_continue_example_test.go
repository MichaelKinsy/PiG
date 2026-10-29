// Ports packages/durable/test/examples/33-turn-limit-and-continue.ts: a per-conversation turn limit stops a long run
// after a number of tool rounds; a continue resumes it without a user message, with a fresh budget. A host learns that
// a run stopped from pi.live.runFailure. The script's printing is the transcript this test reads.
//
// Go mapping: upstream relies on the event loop for the second continue to be submitted while the first continue's run
// still works. Goroutines have no such order, so the step tool of the third round waits for a gate the test opens once
// the busy continue was refused; nothing else differs.

package examples_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/harness"
	"github.com/MichaelKinsy/PiG/durable/storage"
)

// Pi: packages/durable/src/harness/generation.ts (maxTurns, continue).
func TestExample33TurnLimitAndContinue(t *testing.T) {
	// The second continue's run waits in its first round until the test has tried a third continue.
	gate := make(chan struct{})
	step := &durable.ToolRegistration{
		ToolSchema: ai.ToolSchema{
			Name:        "step",
			Description: "Do one step of the work",
			Parameters: map[string]any{
				"type":       "object",
				"properties": map[string]any{"n": map[string]any{"type": "number"}},
				"required":   []any{"n"},
			},
		},
		Execute: func(_ context.Context, args any, _ durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			n := args.(map[string]any)["n"].(float64)
			if n == 3 {
				<-gate
			}
			return durable.ToolExecutionResult{Output: []ai.ToolResultMessageContent{ai.TextContent{Text: fmt.Sprintf("step %d done", int(n))}}}, nil
		},
	}

	faux := ai.NewFauxProvider(ai.FauxConfig{})
	models := ai.CreateModels()
	models.SetProvider(faux.Provider())
	// Four steps, then the answer. Two tool rounds per run are allowed.
	faux.SetResponses([]ai.FauxResponseStep{
		fauxToolTurn("step", map[string]any{"n": 1}, "s1"),
		fauxToolTurn("step", map[string]any{"n": 2}, "s2"),
		fauxToolTurn("step", map[string]any{"n": 3}, "s3"),
		fauxToolTurn("step", map[string]any{"n": 4}, "s4"),
		fauxAnswer("All four steps are done."),
	})
	registry := harness.CreateRegistry()
	installed(t, registry, &durable.Extension{Name: "steps", Tools: []*durable.ToolRegistration{step}})
	opened, err := harness.OpenHarness(background, storage.NewMemoryStorage(), harness.HarnessOptions{Models: models, Registry: registry})
	if err != nil {
		t.Fatal(err)
	}
	// Stored on the conversation, like its model: children and forks inherit it.
	root := must(opened.Root(background, &harness.RootOptions{Agent: &harness.AgentChange{Model: harness.SetTo(fauxModel), MaxTurns: harness.SetTo(2.0)}}))

	first := must(must(root.Submit(background, durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText("Do the four steps.")})).Wait(background))
	failure := must(durable.Snapshot(background, opened, harness.LiveDoc, root.Id())).RunFailure
	if failure == nil || failure.Detail == nil {
		t.Fatalf("runFailure = %+v, want the turn limit with its detail", failure)
	}
	expectEqual(t, "first run", []string{string(first.Status), *first.Reason, *failure.Detail}, []string{"unanswered", "turn_limit", "2 turns"})

	// An idle conversation runs a continue on the context as it is, the round's results last, with a fresh budget.
	resumed := must(root.Submit(background, durable.SubmissionDraft{Type: durable.SubmissionTypeContinue}))
	// It never queues: while that run works, another continue is refused.
	_, err = root.Submit(background, durable.SubmissionDraft{Type: durable.SubmissionTypeContinue})
	if _, ok := errors.AsType[*durable.ConversationBusy](err); !ok {
		t.Fatalf("second continue while busy: %v, want ConversationBusy", err)
	}
	close(gate)
	second := must(resumed.Wait(background))
	expectEqual(t, "first continue", []string{string(second.Status), *second.Reason}, []string{"unanswered", "turn_limit"})
	third := must(must(root.Submit(background, durable.SubmissionDraft{Type: durable.SubmissionTypeContinue})).Wait(background))
	live := must(durable.Snapshot(background, opened, harness.LiveDoc, root.Id()))
	expectEqual(t, "second continue", string(third.Status), "done")
	if live.RunFailure != nil {
		t.Fatalf("runFailure = %+v, want it cleared", live.RunFailure)
	}

	page := must(root.Entries(background, durable.EntryQuery{Order: new(durable.ScanAscending)}, 100, nil))
	var lines []string
	for _, entry := range page.Items {
		if len(entry.Model) == 0 {
			continue
		}
		switch message := entry.Model[0].(type) {
		case ai.UserMessage:
			text, _ := message.Content.(ai.UserText)
			lines = append(lines, "> "+string(text))
		case ai.ToolResultMessage:
			if text, ok := message.Content[0].(ai.TextContent); ok {
				lines = append(lines, "  tool: "+text.Text)
			}
		case ai.AssistantMessage:
			if message.StopReason != "stop" {
				continue
			}
			var text strings.Builder
			for _, part := range message.Content {
				if content, ok := part.(ai.TextContent); ok {
					text.WriteString(content.Text)
				}
			}
			lines = append(lines, "agent: "+text.String())
		}
	}
	expectEqual(t, "transcript", lines, []string{
		"> Do the four steps.",
		"  tool: step 1 done",
		"  tool: step 2 done",
		"  tool: step 3 done",
		"  tool: step 4 done",
		"agent: All four steps are done.",
	})
	// A continue after an answer has nothing to answer: it ends without a model request.
	after := must(must(root.Submit(background, durable.SubmissionDraft{Type: durable.SubmissionTypeContinue})).Wait(background))
	expectEqual(t, "continue after the answer", []string{string(after.Status), *after.Reason}, []string{"unanswered", "nothing_to_answer"})
	if err := opened.Close(background); err != nil {
		t.Fatal(err)
	}
}
