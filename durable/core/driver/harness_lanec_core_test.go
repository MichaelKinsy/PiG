// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package driver_test

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/core/driver"
	"github.com/MichaelKinsy/PiG/durable/harness"
)

// Pi 87eff524..e7a0df27 over the core (harness-turn-limit.test.ts, harness-continue.test.ts, harness-submit-in-commit.test.ts): the
// per-conversation turn limit, a continue that has nothing to answer, whenIdle reject, and a submission admitted inside the caller's commit.

// laneCOpen opens a Harness over the core with a faux model and a noop tool, and its root.
func laneCOpen(t *testing.T, responses ...ai.FauxResponseStep) (harness.Harness, harness.Conversation) {
	t.Helper()
	faux := ai.NewFauxProvider(ai.FauxConfig{})
	models := ai.CreateModels()
	models.SetProvider(faux.Provider())
	registry := harness.CreateRegistry()
	noop := nestedCoreTool("noop", func(context.Context, any, durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		return durable.ToolExecutionResult{Output: []ai.ToolResultMessageContent{}}, nil
	})
	if err := registry.Install(&durable.Extension{Name: "s", Tools: []*durable.ToolRegistration{harness.DefineTool(*noop)}}); err != nil {
		t.Fatal(err)
	}
	faux.SetResponses(responses)
	h, err := driver.OpenHarness(t.Context(), filepath.Join(t.TempDir(), "store.db"), harness.HarnessOptions{Models: models, Registry: registry})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close(context.Background()) })
	root, err := h.Root(t.Context(), &harness.RootOptions{Agent: &harness.AgentChange{Model: harness.SetTo(durable.ModelRef{Provider: "faux", ModelId: "faux-1"})}})
	if err != nil {
		t.Fatal(err)
	}
	h.Resume()
	return h, root
}

func laneCRound(id string) ai.FauxResponseStep {
	return ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxToolCall("noop", map[string]any{}, &ai.FauxToolCallOptions{ID: id})}, StopReason: "toolUse"})
}

func laneCAnswer(text string) ai.FauxResponseStep {
	return ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText(text)}, StopReason: "stop"})
}

func laneCSubmit(t *testing.T, root harness.Conversation, draft durable.SubmissionDraft) durable.SubmissionRecord {
	t.Helper()
	submission, err := root.Submit(t.Context(), draft)
	if err != nil {
		t.Fatal(err)
	}
	settled, err := submission.Wait(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return settled
}

// A run that reaches AgentState.maxTurns ends like a failure with reason turn_limit ("1 turn") after its tools ran, recording the run's
// first entry; a continue resumes it on the round's results and the model answers it.
func TestCoreEndsARunAtTheTurnLimitAndAContinueResumesIt(t *testing.T) {
	h, root := laneCOpen(t, laneCRound("c1"), laneCAnswer("done"))
	maxTurns := 1.0
	if err := root.Configure(t.Context(), harness.AgentChange{MaxTurns: harness.SetTo(maxTurns)}); err != nil {
		t.Fatal(err)
	}
	settled := laneCSubmit(t, root, durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText("go")})
	if settled.Status != durable.SubmissionUnanswered || settled.Reason == nil || *settled.Reason != string(harness.RunTurnLimit) {
		t.Fatalf("settled %+v, want unanswered with turn_limit", settled)
	}
	if detail, _ := settled.Detail.(string); detail != "1 turn" {
		t.Fatalf("detail %v, want \"1 turn\"", settled.Detail)
	}
	live, err := durable.Snapshot(t.Context(), h, harness.LiveDoc, root.Id())
	if err != nil || live == nil || live.RunFailure == nil || live.RunFailure.First == nil {
		t.Fatalf("live %+v, %v, want a runFailure with first", live, err)
	}
	continued := laneCSubmit(t, root, durable.SubmissionDraft{Type: durable.SubmissionTypeContinue})
	if continued.Type != durable.SubmissionTypeContinue || continued.Status != durable.SubmissionDone || continued.Answer == nil {
		t.Fatalf("continue settled %+v, want a continue answered", continued)
	}
}

// A continue in a conversation that ends in an answer has nothing to answer: no request is sent and the run fails nothing_to_answer.
func TestCoreEndsAContinueWithNothingToAnswer(t *testing.T) {
	_, root := laneCOpen(t, laneCAnswer("hi"))
	laneCSubmit(t, root, durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText("go")})
	settled := laneCSubmit(t, root, durable.SubmissionDraft{Type: durable.SubmissionTypeContinue})
	if settled.Status != durable.SubmissionUnanswered || settled.Reason == nil || *settled.Reason != string(harness.RunNothingToAnswer) {
		t.Fatalf("continue settled %+v, want unanswered with nothing_to_answer", settled)
	}
}

// An input with whenIdle reject is refused on an idle conversation with ConversationIdle, writing nothing.
func TestCoreRejectsAnInputWhenIdle(t *testing.T) {
	_, root := laneCOpen(t, laneCAnswer("hi"))
	_, err := root.Submit(t.Context(), durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText("go"), WhenIdle: durable.WhenIdleReject})
	if idle, ok := errors.AsType[*durable.ConversationIdle](err); !ok || idle.ConversationId != root.Id() {
		t.Fatalf("submit error %v, want ConversationIdle for the root", err)
	}
	if entries := laneCEntries(t, root); len(entries) != 0 {
		t.Fatalf("%d entries written, want none", len(entries))
	}
}

func laneCEntries(t *testing.T, root harness.Conversation) []durable.EntryRecord {
	t.Helper()
	page, err := root.Entries(t.Context(), durable.EntryQuery{}, 100, nil)
	if err != nil {
		t.Fatal(err)
	}
	return page.Items
}

// Conversation.SubmitIn admits the submission inside the caller's commit: stored with it, refused after the commit's first table write.
func TestCoreAdmitsASubmissionInsideTheCallersCommit(t *testing.T) {
	h, root := laneCOpen(t, laneCAnswer("hi"))
	var admitted durable.SubmissionId
	if _, err := h.Commit(t.Context(), func(tx durable.Tx) (any, error) {
		id, err := root.SubmitIn(tx, durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText("in a commit")})
		admitted = id
		return nil, err
	}); err != nil {
		t.Fatal(err)
	}
	submission, err := h.Submission(t.Context(), admitted)
	if err != nil {
		t.Fatal(err)
	}
	settled, err := submission.Wait(t.Context())
	if err != nil || settled.Status != durable.SubmissionDone {
		t.Fatalf("settled %+v, %v, want the run answered", settled, err)
	}
	_, err = h.Commit(t.Context(), func(tx durable.Tx) (any, error) {
		if _, err := tx.CreateConversation(durable.CreateConversationOptions{}); err != nil {
			return nil, err
		}
		_, err := root.SubmitIn(tx, durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText("late")})
		return nil, err
	})
	var after *durable.ReadAfterWrite
	if !errors.As(err, &after) || after.Operation != "submit(tx, draft)" {
		t.Fatalf("SubmitIn after a table write: %v, want ReadAfterWrite for submit(tx, draft)", err)
	}
	if !slices.Contains([]string{"submit(tx, draft)"}, after.Operation) {
		t.Fatal("unreachable")
	}
}

type afterRunState struct {
	Phase string `json:"phase"`
}

// An afterRun hook asks for a task: the run keeps its busy state while the task runs, the task's entry lands after the answer, and the
// run's input is settled done with the answer already (harness-after-run.test.ts "asks for tasks after the answer and waits for them").
func TestCoreRunsTheTasksAnAfterRunHookAsksFor(t *testing.T) {
	ran := make(chan string, 4)
	tests := durable.DefineTask(durable.TaskDefinition[arInput, afterRunState, string, any]{
		Name:    "test.run-tests",
		Version: 1,
		Initial: func(arInput) afterRunState { return afterRunState{Phase: "run"} },
		Phases: map[string]durable.PhaseHandler[arInput, afterRunState, string, any]{
			"run": func(ctx context.Context, task durable.RunningTask[arInput, afterRunState, string], runtime durable.TaskRuntime[arInput, afterRunState, string, any]) error {
				ran <- task.Input.Label
				return runtime.Commit(ctx, func(tx durable.Tx, _ durable.RunningTask[arInput, afterRunState, string]) (*durable.NextTaskState[afterRunState, string], error) {
					if _, err := tx.AppendEntry(runtime.ConversationId(), durable.EntryDraft{Kind: "test.tests", Data: task.Input.Label}); err != nil {
						return nil, err
					}
					return &durable.NextTaskState[afterRunState, string]{Status: durable.TaskTerminal, Outcome: &durable.TaskOutcome[string]{Status: durable.OutcomeCompleted, Result: &task.Input.Label}}, nil
				})
			},
		},
	})
	faux := ai.NewFauxProvider(ai.FauxConfig{})
	models := ai.CreateModels()
	models.SetProvider(faux.Provider())
	registry := harness.CreateRegistry()
	asked := make(chan harness.RunSummary, 4)
	if err := registry.Install(&durable.Extension{
		Name:  "after-run",
		Tasks: []durable.AnyTask{tests.Erased()},
		Hooks: []durable.HookRegistration{harness.Hook(harness.GenerationTask, &harness.GenerationHooks{AfterRun: func(_ context.Context, run harness.RunSummary, _ harness.HookApi) (*harness.RunEndTasks, error) {
			asked <- run
			return &harness.RunEndTasks{Tasks: []harness.RunEndTask{harness.RunEndTaskOf(tests, arInput{Label: "run 1"})}}, nil
		}})},
	}); err != nil {
		t.Fatal(err)
	}
	faux.SetResponses([]ai.FauxResponseStep{laneCAnswer("hi")})
	h, err := driver.OpenHarness(t.Context(), filepath.Join(t.TempDir(), "store.db"), harness.HarnessOptions{Models: models, Registry: registry})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close(context.Background()) })
	root, err := h.Root(t.Context(), &harness.RootOptions{Agent: &harness.AgentChange{Model: harness.SetTo(durable.ModelRef{Provider: "faux", ModelId: "faux-1"})}})
	if err != nil {
		t.Fatal(err)
	}
	h.Resume()
	settled := laneCSubmit(t, root, durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText("go")})
	if settled.Status != durable.SubmissionDone || settled.Answer == nil {
		t.Fatalf("settled %+v, want done with the answer", settled)
	}
	run := <-asked
	if run.Reason != harness.RunEndAnswer || run.Answer != *settled.Answer || run.Last != run.Answer {
		t.Fatalf("the hook saw %+v for the answer %d", run, *settled.Answer)
	}
	if label := <-ran; label != "run 1" {
		t.Fatalf("the task ran with %q", label)
	}
	if err := h.WaitForIdle(t.Context()); err != nil {
		t.Fatal(err)
	}
	pollUntil(t, "the run to end", func() bool {
		live, err := durable.Snapshot(t.Context(), h, harness.LiveDoc, root.Id())
		return err == nil && live != nil && live.Run == nil
	})
	kinds := []string{}
	for _, entry := range laneCEntries(t, root) {
		if entry.Kind != "pi.system" {
			kinds = append(kinds, entry.Kind)
		}
	}
	slices.Reverse(kinds) // Entries lists newest first.
	if want := []string{"pi.user", "pi.assistant", "test.tests"}; !slices.Equal(kinds, want) {
		t.Fatalf("entries %v, want %v", kinds, want)
	}
}

type arInput struct {
	Label string `json:"label"`
}
