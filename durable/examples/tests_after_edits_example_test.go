// Ports packages/durable/test/examples/32-tests-after-edits.ts: run the project's tests at the end of every run that
// edited files, with an afterRun hook: the run waits for the tests, a failure starts a fix run, and input sent
// meanwhile waits for them too. The script's printing is the transcript this test reads.
//
// Go mapping: upstream relies on the event loop for the user's second question to arrive while the tests run (the task
// starts after the awaiting script continued). Goroutines have no such order, so the tests task here waits for a gate
// the test opens once the question was submitted; nothing else differs.

package examples_test

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/harness"
	"github.com/MichaelKinsy/PiG/durable/storage"
)

type testsInput struct {
	Attempt int `json:"attempt"`
}

type testsState struct {
	Phase string `json:"phase"`
}

type testsResult struct {
	Passed bool   `json:"passed"`
	Output string `json:"output"`
}

// Pi: packages/durable/src/harness/generation.ts (afterRun).
func TestExample32TestsAfterEdits(t *testing.T) {
	// The project under test, in memory: the tests pass once `add` adds.
	var filesMu sync.Mutex
	files := map[string]string{"math.ts": "export const add = (a, b) => a - b;"}
	runTests := func() testsResult {
		filesMu.Lock()
		defer filesMu.Unlock()
		passed := strings.Contains(files["math.ts"], "a + b")
		output := "add(1, 2): expected 3, got -1"
		if passed {
			output = "1 passed"
		}
		return testsResult{Passed: passed, Output: output}
	}

	edit := &durable.ToolRegistration{
		ToolSchema: ai.ToolSchema{
			Name:        "edit",
			Description: "Replace a file's content",
			Parameters: map[string]any{
				"type":       "object",
				"properties": map[string]any{"path": map[string]any{"type": "string"}, "content": map[string]any{"type": "string"}},
				"required":   []any{"path", "content"},
			},
		},
		Execute: func(_ context.Context, args any, _ durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			input := args.(map[string]any)
			filesMu.Lock()
			files[input["path"].(string)] = input["content"].(string)
			filesMu.Unlock()
			return durable.ToolExecutionResult{Output: []ai.ToolResultMessageContent{ai.TextContent{Text: "wrote " + input["path"].(string)}}}, nil
		},
	}

	// A durable task: it runs once the run answered, and the run ends after it. A crash resumes it.
	questionSent := make(chan struct{})
	var attemptsRun []int
	var attemptsMu sync.Mutex
	runTestsTask := durable.DefineTask(durable.TaskDefinition[testsInput, testsState, bool, any]{
		Name:    "app.run-tests",
		Version: 1,
		Initial: func(testsInput) testsState { return testsState{Phase: "run"} },
		Phases: map[string]durable.PhaseHandler[testsInput, testsState, bool, any]{
			"run": func(ctx context.Context, task durable.RunningTask[testsInput, testsState, bool], runtime durable.TaskRuntime[testsInput, testsState, bool, any]) error {
				if task.Input.Attempt == 1 {
					select {
					case <-questionSent:
					case <-runtime.Signal().Done():
						return context.Cause(runtime.Signal())
					}
				}
				result := runTests()
				attemptsMu.Lock()
				attemptsRun = append(attemptsRun, task.Input.Attempt)
				attemptsMu.Unlock()
				// The handle is fetched before the commit: inside it, runtime reads would wait for the commit itself.
				conversation, err := runtime.Conversation(ctx, runtime.ConversationId())
				if err != nil {
					return err
				}
				return runtime.Commit(ctx, func(tx durable.Tx, _ durable.RunningTask[testsInput, testsState, bool]) (*durable.NextTaskState[testsState, bool], error) {
					// Admitted with this commit, before its own table writes: placed when the run ends.
					if !result.Passed && task.Input.Attempt < 3 {
						requestId := "fix-" + string(rune('0'+task.Input.Attempt))
						if _, err := conversation.SubmitIn(tx, durable.SubmissionDraft{
							Type:      durable.SubmissionTypeInput,
							Content:   ai.UserText("The tests failed: " + result.Output + ". Fix it."),
							WhenBusy:  durable.WhenBusyFollowUp,
							RequestId: &requestId,
						}); err != nil {
							return nil, err
						}
					}
					if _, err := tx.AppendEntry(runtime.ConversationId(), durable.EntryDraft{Kind: "app.tests", Data: map[string]any{"passed": result.Passed, "output": result.Output}}); err != nil {
						return nil, err
					}
					passed := result.Passed
					return &durable.NextTaskState[testsState, bool]{Status: durable.TaskTerminal, Outcome: &durable.TaskOutcome[bool]{Status: durable.OutcomeCompleted, Result: &passed}}, nil
				})
			},
		},
		Abort: func(ctx context.Context, _ durable.RunningTask[testsInput, testsState, bool], runtime durable.TaskRuntime[testsInput, testsState, bool, any]) error {
			return runtime.Commit(ctx, func(durable.Tx, durable.RunningTask[testsInput, testsState, bool]) (*durable.NextTaskState[testsState, bool], error) {
				return &durable.NextTaskState[testsState, bool]{Status: durable.TaskTerminal, Outcome: &durable.TaskOutcome[bool]{Status: durable.OutcomeAborted}}, nil
			})
		},
	})

	var attempts atomic.Int32
	testsAfterEdits := &durable.Extension{
		Name:  "tests-after-edits",
		Tools: []*durable.ToolRegistration{edit},
		Tasks: []durable.AnyTask{runTestsTask.Erased()},
		Hooks: []durable.HookRegistration{harness.Hook(harness.GenerationTask, &harness.GenerationHooks{
			AfterRun: func(ctx context.Context, run harness.RunSummary, api harness.HookApi) (*harness.RunEndTasks, error) {
				// The run's own entries, from its first input to its answer; a compaction never removes them.
				ascending := durable.ScanAscending
				page, err := api.Entries(ctx, durable.EntryQuery{MinEntryId: &run.First, MaxEntryId: &run.Last, Order: &ascending}, 1000, nil)
				if err != nil {
					return nil, err
				}
				edited := false
				for _, entry := range page.Items {
					if !durable.ToolResultEntry.Is(&entry) || len(entry.Model) == 0 {
						continue
					}
					if message, ok := entry.Model[0].(ai.ToolResultMessage); ok && message.ToolName == "edit" {
						edited = true
					}
				}
				if !edited {
					return nil, nil
				}
				// Each fix run asks again with a fresh turn budget: the extension caps the loop itself.
				return &harness.RunEndTasks{Tasks: []harness.RunEndTask{harness.RunEndTaskOf(runTestsTask, testsInput{Attempt: int(attempts.Add(1))})}}, nil
			},
		})},
	}

	faux := ai.NewFauxProvider(ai.FauxConfig{})
	models := ai.CreateModels()
	models.SetProvider(faux.Provider())
	faux.SetResponses([]ai.FauxResponseStep{
		// The first run edits, wrongly.
		fauxToolTurn("edit", map[string]any{"path": "math.ts", "content": "export const add = (a, b) => a - b + 0;"}, "e1"),
		fauxAnswer("I cleaned up add."),
		// The user's question, queued first, runs first; it edits nothing, so no tests run after it.
		fauxAnswer("Not yet: add subtracts. The fix is queued next."),
		// The fix run the failed tests started.
		fauxToolTurn("edit", map[string]any{"path": "math.ts", "content": "export const add = (a, b) => a + b;"}, "e2"),
		fauxAnswer("Fixed: add adds now."),
	})
	registry := harness.CreateRegistry()
	installed(t, registry, testsAfterEdits)
	opened, err := harness.OpenHarness(background, storage.NewMemoryStorage(), harness.HarnessOptions{Models: models, Registry: registry})
	if err != nil {
		t.Fatal(err)
	}
	root := must(opened.Root(background, &harness.RootOptions{Agent: &harness.AgentChange{Model: harness.SetTo(fauxModel)}}))

	submission := must(root.Submit(background, durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText("Clean up math.ts.")}))
	// Resolves with the answer; the conversation stays busy while the tests run.
	answered := must(submission.Wait(background))
	expectEqual(t, "answered", string(answered.Status), "done")
	live := must(durable.Snapshot(background, opened, harness.LiveDoc, root.Id()))
	if live.Run == nil || live.Run.AfterRun == nil {
		t.Fatalf("live = %+v, want a run busy with after-run tasks", live)
	}
	// Sent while the tests run: it waits for them. Follow-ups run in queue order, so it runs before the fix run.
	must(root.Submit(background, durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText("Are the tests green now?")}))
	close(questionSent)

	if err := root.WaitForIdle(background); err != nil {
		t.Fatal(err)
	}
	page := must(root.Entries(background, durable.EntryQuery{Order: new(durable.ScanAscending)}, 100, nil))
	var lines []string
	for _, entry := range page.Items {
		if entry.Kind == "app.tests" {
			lines = append(lines, "tests: "+must(durable.FromJsonValue[testsResult](entry.Data)).Output)
			continue
		}
		if len(entry.Model) == 0 {
			continue
		}
		switch message := entry.Model[0].(type) {
		case ai.UserMessage:
			text, _ := message.Content.(ai.UserText)
			lines = append(lines, "> "+string(text))
		case ai.AssistantMessage:
			var text strings.Builder
			for _, part := range message.Content {
				if content, ok := part.(ai.TextContent); ok {
					text.WriteString(content.Text)
				}
			}
			if text.Len() > 0 {
				lines = append(lines, "agent: "+text.String())
			}
		}
	}
	expectEqual(t, "transcript", lines, []string{
		"> Clean up math.ts.",
		"agent: I cleaned up add.",
		"tests: add(1, 2): expected 3, got -1",
		"> Are the tests green now?",
		"agent: Not yet: add subtracts. The fix is queued next.",
		"> The tests failed: add(1, 2): expected 3, got -1. Fix it.",
		"agent: Fixed: add adds now.",
		"tests: 1 passed",
	})
	attemptsMu.Lock()
	expectEqual(t, "test runs", attemptsRun, []int{1, 2})
	attemptsMu.Unlock()
	if err := opened.Close(background); err != nil {
		t.Fatal(err)
	}
}
