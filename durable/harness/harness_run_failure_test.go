// Ports packages/durable/test/harness-run-failure.test.ts: pi.live.runFailure, why the newest run ended without an answer, as
// state a renderer shows (Pi #11327).

package harness

import (
	"context"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/storage"
)

func runFailureOf(t *testing.T, harness Harness, conversation Conversation) *RunFailure {
	t.Helper()
	return liveOf(t, harness, conversation.Id()).RunFailure
}

func expectRunFailure(t *testing.T, got *RunFailure, inputs []durable.SubmissionId, reason RunFailureReason, detail *string) {
	t.Helper()
	if got == nil || !slices.Equal(got.Inputs, inputs) || got.Reason != reason || (detail == nil) != (got.Detail == nil) || (detail != nil && *detail != *got.Detail) {
		t.Fatalf("runFailure = %s, want inputs %v, reason %s, detail %v", jsonText(t, got), inputs, reason, detail)
	}
}

func TestRunFailure(t *testing.T) {
	t.Run("records a model error with its text, and a later run clears it", func(t *testing.T) {
		// upstream: packages/durable/test/harness-run-failure.test.ts:35
		setup := chatSetup(t)
		setup.Faux.SetResponses([]ai.FauxResponseStep{
			ai.FauxStaticStep(ai.FauxResponse{StopReason: "error", ErrorMessage: "Invalid request"}),
			fauxAnswer("fine"),
		})
		harness, root := openChat(t, storage.NewMemoryStorage(), setup)
		submission := submit(t, root, inputDraft("hi"))
		settled := must(submission.Wait(testContext))
		expectSettled(t, settled, durable.SubmissionUnanswered, "model_error")
		if detail, _ := settled.Detail.(string); detail != "Invalid request" {
			t.Fatalf("settlement detail = %v, want Invalid request", settled.Detail)
		}
		failure := runFailureOf(t, harness, root)
		expectRunFailure(t, failure, []durable.SubmissionId{submission.Id()}, RunModelError, new("Invalid request"))
		if failure.TaskId == 0 {
			t.Fatal("runFailure names no task")
		}
		next := submit(t, root, inputDraft("again"))
		// The commit that starts the next run removes it.
		if got := runFailureOf(t, harness, root); got != nil {
			t.Fatalf("runFailure after the next run started = %+v", got)
		}
		if status := must(next.Wait(testContext)).Status; status != durable.SubmissionDone {
			t.Fatalf("next = %s, want done", status)
		}
		expectEqualJSON(t, liveOf(t, harness, root.Id()), `{}`)
		closeHarness(t, harness)
	})

	t.Run("records a missing model with which one", func(t *testing.T) {
		// upstream: packages/durable/test/harness-run-failure.test.ts:62
		harness, root := openChat(t, storage.NewMemoryStorage(), chatSetup(t))
		if err := root.Configure(testContext, AgentChange{Model: SetTo(durable.ModelRef{Provider: "faux", ModelId: "gone"})}); err != nil {
			t.Fatal(err)
		}
		submission := submit(t, root, inputDraft("hi"))
		settled := must(submission.Wait(testContext))
		expectSettled(t, settled, durable.SubmissionUnanswered, "no_model")
		// The settlement is unchanged: no detail.
		if settled.Detail != nil {
			t.Fatalf("settlement detail = %v, want none", settled.Detail)
		}
		expectRunFailure(t, runFailureOf(t, harness, root), []durable.SubmissionId{submission.Id()}, RunNoModel, new("Model faux/gone is not available"))
		closeHarness(t, harness)
	})

	t.Run("records an abort", func(t *testing.T) {
		// upstream: packages/durable/test/harness-run-failure.test.ts:79
		setup := chatSetup(t)
		busy := unanswered()
		setup.Faux.SetResponses([]ai.FauxResponseStep{busy.step})
		harness, root := openChat(t, storage.NewMemoryStorage(), setup)
		submission := submit(t, root, inputDraft("hi"))
		<-busy.reached
		taskId := liveOf(t, harness, root.Id()).Run.TaskId
		if err := root.Abort(testContext, nil); err != nil {
			t.Fatal(err)
		}
		expectSettled(t, must(submission.Wait(testContext)), durable.SubmissionUnanswered, "aborted")
		failure := runFailureOf(t, harness, root)
		expectRunFailure(t, failure, []durable.SubmissionId{submission.Id()}, RunAborted, nil)
		if failure.TaskId != taskId {
			t.Fatalf("runFailure task = %d, want %d", failure.TaskId, taskId)
		}
		closeHarness(t, harness)
	})

	t.Run("records a reset that cut the run before an answer, kept by the run its queued input starts", func(t *testing.T) {
		// upstream: packages/durable/test/harness-run-failure.test.ts:94
		setup := chatSetup(t)
		gate, started := deferred(), deferred()
		addTool(t, setup.Registry, DefineTool(durable.ToolRegistration{
			ToolSchema: ai.ToolSchema{Name: "hold", Description: "Waits", Parameters: map[string]any{"type": "object", "properties": map[string]any{}}},
			Execute: func(ctx context.Context, _ any, _ durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
				started.resolve()
				if err := gate.wait(ctx); err != nil {
					return durable.ToolExecutionResult{}, err
				}
				return durable.ToolExecutionResult{Output: []ai.ToolResultMessageContent{}}, nil
			},
		}))
		second := unanswered()
		setup.Faux.SetResponses([]ai.FauxResponseStep{holdStep(), second.step})
		harness, root := openChat(t, storage.NewMemoryStorage(), setup)
		first := submit(t, root, inputDraft("go"))
		if err := started.wait(testContext); err != nil {
			t.Fatal(err)
		}
		if err := root.Reset(testContext, nil); err != nil {
			t.Fatal(err)
		}
		followUp := submit(t, root, inputDraft("next", durable.WhenBusyFollowUp))
		gate.resolve()
		expectSettled(t, must(first.Wait(testContext)), durable.SubmissionUnanswered, "reset")
		<-second.reached
		state := liveOf(t, harness, root.Id())
		// The follow-up's run started in the commit that ended the first; the failure stays until a later run starts.
		if state.Run == nil || !slices.Equal(state.Run.Inputs, []durable.SubmissionId{followUp.Id()}) {
			t.Fatalf("run = %+v, want the follow-up's", state.Run)
		}
		expectRunFailure(t, state.RunFailure, []durable.SubmissionId{first.Id()}, RunReset, nil)
		if err := root.Abort(testContext, nil); err != nil {
			t.Fatal(err)
		}
		expectRunFailure(t, runFailureOf(t, harness, root), []durable.SubmissionId{followUp.Id()}, RunAborted, nil)
		closeHarness(t, harness)
	})

	t.Run("keeps the failure while a run continues the queue in the same commit, and replaces it with the next", func(t *testing.T) {
		// upstream: packages/durable/test/harness-run-failure.test.ts:131
		for _, policy := range []durable.AfterFailedRun{durable.AfterFailedRunContinue, durable.AfterFailedRunKeep} {
			t.Run(string(policy), func(t *testing.T) {
				setup := chatSetup(t)
				setup.SetSettings(func(settings *HarnessSettings) { settings.AfterFailedRun = policy })
				failing := gatedResponse(ai.FauxResponse{StopReason: "error", ErrorMessage: "Invalid request"})
				busy := unanswered()
				setup.Faux.SetResponses([]ai.FauxResponseStep{failing.step, busy.step, fauxAnswer("third")})
				harness, root := openChat(t, storage.NewMemoryStorage(), setup)
				first := submit(t, root, inputDraft("first"))
				failing.awaitReached(t)
				second := submit(t, root, inputDraft("second", durable.WhenBusyFollowUp))
				failing.release()
				must(first.Wait(testContext))
				if policy == durable.AfterFailedRunContinue {
					<-busy.reached
					state := liveOf(t, harness, root.Id())
					if state.Run == nil || !slices.Equal(state.Run.Inputs, []durable.SubmissionId{second.Id()}) {
						t.Fatalf("run = %+v, want the queued input's", state.Run)
					}
					expectRunFailure(t, state.RunFailure, []durable.SubmissionId{first.Id()}, RunModelError, new("Invalid request"))
				} else {
					must0(t, harness.WaitForIdle(testContext))
					if failure := runFailureOf(t, harness, root); failure == nil || !slices.Equal(failure.Inputs, []durable.SubmissionId{first.Id()}) {
						t.Fatalf("runFailure = %+v, want the first run's", failure)
					}
				}
				closeHarness(t, harness)
			})
		}
	})
}
