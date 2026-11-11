// Ports packages/durable/test/examples/45-review-on-yield.ts: an `onYield` hook that asks a review task whether an answer
// stands. While the review runs, the answer is in the transcript but the run's input stays open, so whoever waits on it
// sees only a reviewed answer. The review's `{ continue }` sends the agent back to work in the same run; a user message
// queued meanwhile wins over it. The script's printing is the transcript this test reads.
//
// Go mapping: upstream's review waits for a promise its `release()` resolves, and the script pauses a few milliseconds
// for the review to start before calling it. Here release is a buffered gate, so a release is never lost whether the
// review already waits or not; and the script waits for a yield of an answer newer than the one it reviewed last (a pause
// cannot tell the previous run's yield from the next one).

package examples_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/harness"
	"github.com/MichaelKinsy/PiG/durable/storage"
)

type reviewState struct {
	Phase string `json:"phase"`
}

// Pi: packages/durable/src/harness/generation.ts (onYield tasks, LiveRun.yield).
func TestExample45ReviewOnYield(t *testing.T) {
	var script printed

	// The review: reads the answer the hook asked about from `run.yield`, and sends the agent back when it is too short.
	released := make(chan struct{}, 8)
	release := func() { released <- struct{}{} }
	type reviewRuntime = durable.TaskRuntime[any, reviewState, harness.YieldDecision, any]
	type reviewTask = durable.RunningTask[any, reviewState, harness.YieldDecision]
	type reviewNext = durable.NextTaskState[reviewState, harness.YieldDecision]
	review := durable.DefineTask(durable.TaskDefinition[any, reviewState, harness.YieldDecision, any]{
		Name:    "example.review",
		Version: 1,
		Initial: func(any) reviewState { return reviewState{Phase: "review"} },
		Phases: map[string]durable.PhaseHandler[any, reviewState, harness.YieldDecision, any]{
			"review": func(ctx context.Context, _ reviewTask, runtime reviewRuntime) error {
				select {
				case <-released:
				case <-runtime.Signal().Done():
					return context.Cause(runtime.Signal())
				}
				live, err := durable.Snapshot(ctx, runtime, harness.LiveDoc, runtime.ConversationId())
				if err != nil {
					return err
				}
				if live == nil || live.Run == nil || live.Run.Yield == nil {
					return fmt.Errorf("the review runs without a yield: %+v", live)
				}
				entry, err := runtime.Entry(ctx, live.Run.Yield.Answer)
				if err != nil {
					return err
				}
				text := ""
				if entry != nil && len(entry.Model) > 0 {
					if message, ok := entry.Model[0].(ai.AssistantMessage); ok {
						text = assistantText(message)
					}
				}
				decision := harness.YieldDecision{}
				if len(text) < 20 {
					decision = harness.YieldDecision{Continue: ai.UserText("Too short: explain your reasoning.")}
				}
				script.add(fmt.Sprintf(`  [review] "%s" -> %s`, text, jsonText(decision)))
				return runtime.Commit(ctx, func(durable.Tx, reviewTask) (*reviewNext, error) {
					return &reviewNext{Status: durable.TaskTerminal, Outcome: &durable.TaskOutcome[harness.YieldDecision]{Status: durable.OutcomeCompleted, Result: &decision}}, nil
				})
			},
		},
		Abort: func(ctx context.Context, _ reviewTask, runtime reviewRuntime) error {
			return runtime.Commit(ctx, func(durable.Tx, reviewTask) (*reviewNext, error) {
				return &reviewNext{Status: durable.TaskTerminal, Outcome: &durable.TaskOutcome[harness.YieldDecision]{Status: durable.OutcomeAborted}}, nil
			})
		},
	})

	// A review of every answer, a continuation's too.
	reviewer := &durable.Extension{
		Name:  "reviewer",
		Tasks: []durable.AnyTask{review.Erased()},
		Hooks: []durable.HookRegistration{harness.Hook(harness.GenerationTask, &harness.GenerationHooks{
			OnYield: func(context.Context, ai.AssistantMessage, harness.HookApi) (harness.YieldResult, error) {
				return &harness.YieldTasks{Tasks: []harness.HookTask{harness.HookTaskOf[any](review, nil)}}, nil
			},
		})},
	}

	models := fauxModels(
		fauxAnswer("Yes."),
		fauxAnswer("Yes: the build passed and the tests are green, so it can ship."),
		fauxAnswer("No."),
		fauxAnswer("Answering the queued question instead."),
	)
	registry := harness.CreateRegistry()
	installed(t, registry, reviewer)
	opened, err := harness.OpenHarness(background, storage.NewMemoryStorage(), harness.HarnessOptions{Models: models, Registry: registry})
	if err != nil {
		t.Fatal(err)
	}
	root := must(opened.Root(background, &harness.RootOptions{Agent: &harness.AgentChange{Model: harness.SetTo(fauxModel)}}))
	// reviewing returns once a yield of an answer newer than `after` is open: the answer's entry.
	reviewing := func(after durable.EntryId) durable.EntryId {
		t.Helper()
		var answer durable.EntryId
		eventually(t, func() bool {
			live := liveOf(t, opened, root)
			if live.Run == nil || live.Run.Yield == nil || live.Run.Yield.Answer <= after {
				return false
			}
			answer = live.Run.Yield.Answer
			return true
		})
		return answer
	}
	transcript := func() []string {
		t.Helper()
		ascending := durable.ScanAscending
		page := must(root.Entries(background, durable.EntryQuery{Order: &ascending}, 50, nil))
		lines := []string{}
		for _, entry := range page.Items {
			if len(entry.Model) == 0 {
				continue
			}
			switch message := entry.Model[0].(type) {
			case ai.UserMessage:
				text, _ := message.Content.(ai.UserText)
				lines = append(lines, "> "+string(text))
			case ai.AssistantMessage:
				lines = append(lines, assistantText(message))
			}
		}
		return lines
	}

	// 1. The review sends the agent back; the input settles with the reviewed answer.
	first := must(root.Submit(background, durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText("Can we ship?")}))
	answered := reviewing(0)
	script.add("while reviewing, the input is: " + string(must(first.Status(background)).Status))
	release()
	answered = reviewing(answered)
	release()
	script.add("input: " + string(waitSubmission(t, first).Status))
	firstTranscript := transcript()

	// 2. A follow-up queued during the review wins over the review's continuation.
	second := must(root.Submit(background, durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText("Is it Friday?")}))
	answered = reviewing(answered)
	must(root.Submit(background, durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText("Skip that: what is 2 + 2?"), WhenBusy: durable.WhenBusyFollowUp}))
	release()
	waitSubmission(t, second)
	reviewing(answered)
	release()
	waitIdle(t, root)
	full := transcript()
	if len(full) < 4 {
		t.Fatalf("transcript = %q, want the first run's four lines before the second run", full)
	}
	secondTranscript := full[4:]

	expectEqual(t, "script", script.all(), []string{
		"while reviewing, the input is: placed",
		`  [review] "Yes." -> {"continue":"Too short: explain your reasoning."}`,
		`  [review] "Yes: the build passed and the tests are green, so it can ship." -> {}`,
		"input: done",
		`  [review] "No." -> {"continue":"Too short: explain your reasoning."}`,
		`  [review] "Answering the queued question instead." -> {}`,
	})
	expectEqual(t, "transcript", firstTranscript, []string{
		"> Can we ship?",
		"Yes.",
		"> Too short: explain your reasoning.",
		"Yes: the build passed and the tests are green, so it can ship.",
	})
	expectEqual(t, "second transcript", secondTranscript, []string{
		"> Is it Friday?",
		"No.",
		"> Skip that: what is 2 + 2?",
		"Answering the queued question instead.",
	})
	if err := opened.Close(background); err != nil {
		t.Fatal(err)
	}
}
