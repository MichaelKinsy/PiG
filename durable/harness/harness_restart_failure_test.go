// Ports packages/durable/test/harness-restart-failure.test.ts (Pi b3a29f11): what a crash (a host that dies, is evicted, or
// is reset) does to work that was running. A generation's interrupted request is a failed attempt under the retry policy,
// and every other task ends visibly once it was cut off settings.restartLimit times in a row (#11139). A crash is
// crash(): the Harness fails, so nothing more is stored, not even the pending records a close writes for stopped tasks.

package harness

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/session"
)

// restartRun is the storage and the faux model and registry that survive the crashes of one test.
type restartRun struct {
	t       *testing.T
	storage *controlledStorage
	setup   *chatState
}

func (run *restartRun) open() (Harness, Conversation) {
	run.t.Helper()
	run.storage.Reopen()
	return openChat(run.t, run.storage, run.setup)
}

// retryPolicy sets settings.retry.
func retryPolicy(setup *chatState, enabled bool, maxRetries, baseDelayMs int) {
	setup.SetSettings(func(settings *HarnessSettings) {
		settings.Retry = &RetryPolicyPatch{Enabled: new(enabled)}
		if enabled {
			settings.Retry.MaxRetries = new(maxRetries)
			settings.Retry.BaseDelayMs = new(baseDelayMs)
		}
	})
}

// liveSnapshot is pi.live of the root conversation; the empty value when absent.
func liveSnapshot(t *testing.T, harness Harness, root Conversation) LiveState {
	t.Helper()
	live := liveOf(t, harness, root.Id())
	if live == nil {
		return LiveState{}
	}
	return *live
}

// liveValues is every pi.live value from now on, as memory adopts it.
type liveValues struct {
	mu     sync.Mutex
	values []LiveState
}

func (values *liveValues) all() []LiveState {
	values.mu.Lock()
	defer values.mu.Unlock()
	return append([]LiveState(nil), values.values...)
}

func observeLive(t *testing.T, harness Harness) *liveValues {
	t.Helper()
	values := &liveValues{}
	observeCommits(harness, func(_ context.Context, publication session.ObservedCommit) {
		for _, change := range documentChanges(publication.Changes) {
			if change.Record.Kind != "pi.live" || change.Value == nil {
				continue
			}
			live, err := durable.FromJsonValue[LiveState](change.Value)
			if err != nil {
				t.Error(err)
				return
			}
			values.mu.Lock()
			values.values = append(values.values, live)
			values.mu.Unlock()
		}
	})
	return values
}

// stored lets deferred progress commits (persistWithinMs: 0) reach Storage before a crash.
func stored() { time.Sleep(50 * time.Millisecond) }

// streamingSetup is a setup whose model streams a long answer slowly; partials are stored as soon as they are committed.
func streamingSetup(t *testing.T) *chatState {
	t.Helper()
	one := 1
	setup := chatSetup(t, ai.FauxConfig{TokensPerSecond: 20, TokenSize: &ai.FauxTokenSize{Min: &one, Max: &one}})
	setup.Faux.SetResponses([]ai.FauxResponseStep{fauxAnswer(strings.Repeat("z", 400))})
	setup.SetSettings(func(settings *HarnessSettings) { settings.Progress = &ProgressPolicyPatch{PersistWithinMs: new(0.0)} })
	return setup
}

// crashWhileStreaming submits an input to a fresh chat and crashes once its streamed partial is stored.
func (run *restartRun) crashWhileStreaming() (durable.SubmissionId, durable.TaskId) {
	t := run.t
	t.Helper()
	harness, root := run.open()
	harness.Resume()
	id := submitInput(t, root, "hi").Id()
	waitFor(t, func() bool {
		live := liveSnapshot(t, harness, root)
		return live.Generation != nil && live.Generation.Message != nil
	})
	taskId := liveSnapshot(t, harness, root).Run.TaskId
	stored()
	crash(t, harness)
	return id, taskId
}

// checkpointOfTask is the generation checkpoint of a task record.
func checkpointOfTask(t *testing.T, record *durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue]) GenerationCheckpoint {
	t.Helper()
	if record.State.Checkpoint == nil {
		return GenerationCheckpoint{}
	}
	return must(durable.FromJsonValue[GenerationCheckpoint](*record.State.Checkpoint))
}

// crashAgain reopens after a crash in the middle of a request, with another streaming answer, and crashes again.
func (run *restartRun) crashAgain(taskId durable.TaskId) {
	t := run.t
	t.Helper()
	harness, root := run.open()
	harness.Resume()
	// The first attempt was charged; the second one streams.
	waitFor(t, func() bool {
		record := must(harness.GetTask(testContext, taskId))
		if record == nil || record.State.Status != durable.TaskRunning {
			return false
		}
		checkpoint := checkpointOfTask(t, record)
		return checkpoint.Phase == generationRequest && checkpoint.Attempt == 2
	})
	waitFor(t, func() bool {
		live := liveSnapshot(t, harness, root)
		return live.Generation != nil && live.Generation.Message != nil
	})
	stored()
	crash(t, harness)
}

func settledSubmission(t *testing.T, harness Harness, id durable.SubmissionId) durable.SubmissionRecord {
	t.Helper()
	return must(must(harness.Submission(testContext, id)).Wait(testContext))
}

func expectUnanswered(t *testing.T, settled durable.SubmissionRecord, reason, detail string) {
	t.Helper()
	if settled.Status != durable.SubmissionUnanswered || settled.Reason == nil || *settled.Reason != reason {
		t.Fatalf("settled = %s", jsonText(t, settled))
	}
	if detail != "" && settled.Detail != detail {
		t.Fatalf("settled detail = %v, want %q", settled.Detail, detail)
	}
}

func expectRunFailure(t *testing.T, live LiveState, want RunFailure) {
	t.Helper()
	if !reflect.DeepEqual(live, LiveState{RunFailure: &want}) {
		t.Fatalf("pi.live = %s, want only runFailure %s", jsonText(t, live), jsonText(t, want))
	}
}

func entryStopReasons(entries []durable.EntryRecord) []ai.StopReason {
	var reasons []ai.StopReason
	for _, entry := range entries {
		if entry.Kind == durable.AssistantEntry.Kind {
			reasons = append(reasons, entry.Model[0].(ai.AssistantMessage).StopReason)
		}
	}
	return reasons
}

func hasRetry(values []LiveState) *LiveGeneration {
	for _, value := range values {
		if value.Generation != nil && value.Generation.Retry != nil {
			return value.Generation
		}
	}
	return nil
}

func TestAGenerationInterruptedWhileTheModelAnswers(t *testing.T) {
	// #11139
	t.Run("charges the attempt: keeps the partial, waits for the retry backoff, and answers on the next attempt", func(t *testing.T) {
		run := &restartRun{t: t, storage: newControlledStorage(), setup: streamingSetup(t)}
		retryPolicy(run.setup, true, 2, 1)
		id, taskId := run.crashWhileStreaming()

		var mu sync.Mutex
		var sent [][]string
		run.setup.Faux.SetResponses([]ai.FauxResponseStep{ai.FauxFactoryStep(func(request ai.TranscriptContext, _ ai.StreamOptions, _ *ai.FauxProviderState, _ *ai.Model) (ai.AssistantMessage, error) {
			var roles []string
			for _, message := range request.Messages {
				roles = append(roles, string(message.Role()))
			}
			mu.Lock()
			sent = append(sent, roles)
			mu.Unlock()
			return ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("answer")}}.AssistantMessage(), nil
		})})
		harness, root := run.open()
		values := observeLive(t, harness)
		generation := liveSnapshot(t, harness, root).Generation
		partial, _ := textOf(*generation.Message)
		if record := must(harness.GetTask(testContext, taskId)); record.Restarts != 1 {
			t.Fatalf("restarts = %d, want 1", record.Restarts)
		}
		harness.Resume()
		if settled := settledSubmission(t, harness, id); settled.Status != durable.SubmissionDone {
			t.Fatalf("settled = %s", jsonText(t, settled))
		}
		// The backoff showed in pi.live with why.
		retry := hasRetry(values.all())
		if retry == nil || retry.Attempt != 1 || retry.Retry.Error != "The host stopped while the model was answering, on attempt 1" || retry.Message != nil || retry.Deferred != nil {
			t.Fatalf("retry generation = %s", jsonText(t, retry))
		}
		attempted := false
		for _, value := range values.all() {
			attempted = attempted || (value.Generation != nil && value.Generation.Attempt == 2)
		}
		if !attempted {
			t.Fatal("no pi.live value shows attempt 2")
		}
		entries := allEntries(t, root)
		expectEqualJSON(t, entryKinds(entries), jsonText(t, []string{"pi.user", "pi.assistant", "pi.assistant"}))
		converted := entries[1].Model[0].(ai.AssistantMessage)
		if converted.StopReason != ai.StopReasonAborted {
			t.Fatalf("converted stop reason = %s, want aborted", converted.StopReason)
		}
		if got, _ := textOf(converted); got != partial {
			t.Fatalf("converted text %q, want the stored partial %q", got, partial)
		}
		// The aborted partial is excluded from the next request.
		mu.Lock()
		if !reflect.DeepEqual(sent, [][]string{{"user"}}) {
			t.Fatalf("sent = %v, want one request with the user message", sent)
		}
		mu.Unlock()
		if live := liveSnapshot(t, harness, root); !reflect.DeepEqual(live, LiveState{}) {
			t.Fatalf("pi.live = %s, want empty", jsonText(t, live))
		}
		closeHarness(t, harness)
	})

	t.Run("sleeps the backoff of settings.retry before the next attempt", func(t *testing.T) {
		run := &restartRun{t: t, storage: newControlledStorage(), setup: streamingSetup(t)}
		retryPolicy(run.setup, true, 2, 60_000)
		_, taskId := run.crashWhileStreaming()

		run.setup.Faux.SetResponses(nil)
		const now = 1_000_000.0
		run.setup.SetNow(func() float64 { return now })
		harness, root := run.open()
		harness.Resume()
		waitFor(t, func() bool { return must(harness.GetTask(testContext, taskId)).State.Status == durable.TaskSleeping })
		record := must(harness.GetTask(testContext, taskId))
		if record.State.Until == nil || *record.State.Until != now+60_000 || checkpointOfTask(t, record).Phase != generationRetry {
			t.Fatalf("task state = %s", jsonText(t, record.State))
		}
		if record.Restarts != 0 {
			t.Fatalf("restarts = %d, want none", record.Restarts)
		}
		want := &LiveGeneration{Attempt: 1, Retry: &RetryStatus{At: now + 60_000, Error: "The host stopped while the model was answering, on attempt 1"}}
		if generation := liveSnapshot(t, harness, root).Generation; !reflect.DeepEqual(generation, want) {
			t.Fatalf("generation = %s, want %s", jsonText(t, generation), jsonText(t, want))
		}
		at := now + 60_000
		if wake := harness.Wake(); wake.Active || wake.At == nil || *wake.At != at {
			t.Fatalf("wake = %+v, want idle until %v", wake, at)
		}
		closeHarness(t, harness)
	})

	t.Run("ends the run interrupted once no retry is left, keeping every partial", func(t *testing.T) {
		run := &restartRun{t: t, storage: newControlledStorage(), setup: streamingSetup(t)}
		retryPolicy(run.setup, true, 1, 1)
		id, taskId := run.crashWhileStreaming()
		// Attempt 2 is interrupted too.
		run.setup.Faux.SetResponses([]ai.FauxResponseStep{fauxAnswer(strings.Repeat("z", 400))})
		run.crashAgain(taskId)

		run.setup.Faux.SetResponses(nil)
		harness, root := run.open()
		harness.Resume()
		text := "The host stopped while the model was answering, on attempt 2"
		expectUnanswered(t, settledSubmission(t, harness, id), "interrupted", text)
		expectRunFailure(t, liveSnapshot(t, harness, root), RunFailure{TaskId: taskId, Inputs: []durable.SubmissionId{id}, Reason: RunInterrupted, Detail: &text})
		outcome := must(harness.WaitForTask(testContext, taskId)).State.Outcome
		want := &durable.TaskOutcome[durable.JsonValue]{Status: durable.OutcomeFailed, Error: &durable.TaskOutcomeError{Message: text, Detail: durable.JsonObject{"reason": "interrupted"}}}
		if jsonText(t, outcome) != jsonText(t, want) {
			t.Fatalf("outcome = %s, want %s", jsonText(t, outcome), jsonText(t, want))
		}
		entries := allEntries(t, root)
		expectEqualJSON(t, entryKinds(entries), jsonText(t, []string{"pi.user", "pi.assistant", "pi.assistant"}))
		if got := entryStopReasons(entries); !reflect.DeepEqual(got, []ai.StopReason{ai.StopReasonAborted, ai.StopReasonAborted}) {
			t.Fatalf("stop reasons = %v, want two aborted partials", got)
		}
		// No request was sent after the second crash.
		if calls := run.setup.Faux.CallCount(); calls != 2 {
			t.Fatalf("model calls = %d, want only the two interrupted requests", calls)
		}
		closeHarness(t, harness)
	})

	t.Run("ends the run at once when retries are disabled", func(t *testing.T) {
		run := &restartRun{t: t, storage: newControlledStorage(), setup: streamingSetup(t)}
		id, _ := run.crashWhileStreaming()
		retryPolicy(run.setup, false, 0, 0)
		harness, _ := run.open()
		harness.Resume()
		expectUnanswered(t, settledSubmission(t, harness, id), "interrupted", "")
		closeHarness(t, harness)
	})

	t.Run("resends after a clean close: a close is not a crash", func(t *testing.T) {
		run := &restartRun{t: t, storage: newControlledStorage(), setup: streamingSetup(t)}
		retryPolicy(run.setup, false, 0, 0)
		harness, root := run.open()
		harness.Resume()
		id := submitInput(t, root, "hi").Id()
		waitFor(t, func() bool {
			live := liveSnapshot(t, harness, root)
			return live.Generation != nil && live.Generation.Message != nil
		})
		taskId := liveSnapshot(t, harness, root).Run.TaskId
		closeHarness(t, harness)

		run.setup.Faux.SetResponses([]ai.FauxResponseStep{fauxAnswer("answer")})
		reopened, _ := run.open()
		if record := must(reopened.GetTask(testContext, taskId)); record.Restarts != 0 {
			t.Fatalf("restarts = %d, want none", record.Restarts)
		}
		reopened.Resume()
		if settled := settledSubmission(t, reopened, id); settled.Status != durable.SubmissionDone {
			t.Fatalf("settled = %s", jsonText(t, settled))
		}
		closeHarness(t, reopened)
	})

	t.Run("does not charge a generation that sleeps in a retry or waits for its tools", func(t *testing.T) {
		run := &restartRun{t: t, storage: newControlledStorage(), setup: chatSetup(t)}
		setup := run.setup
		retryPolicy(setup, true, 1, 60_000)
		gate, started := deferred(), deferred()
		addTool(t, setup.Registry, DefineTool(durable.ToolRegistration{
			ToolSchema: ai.ToolSchema{Name: "hold", Description: "Waits", Parameters: emptyObjectSchema()},
			Replay:     durable.ToolReplaySafe,
			Execute: func(ctx context.Context, _ any, _ durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
				started.resolve()
				if err := gate.wait(ctx); err != nil {
					return durable.ToolExecutionResult{}, err
				}
				return durable.ToolExecutionResult{}, nil
			},
		}))
		setup.Faux.SetResponses([]ai.FauxResponseStep{ai.FauxStaticStep(ai.FauxResponse{StopReason: "error", ErrorMessage: "503 Service Unavailable"})})
		harness, root := run.open()
		harness.Resume()
		id := submitInput(t, root, "hi").Id()
		taskId := liveSnapshot(t, harness, root).Run.TaskId
		waitFor(t, func() bool { return must(harness.GetTask(testContext, taskId)).State.Status == durable.TaskSleeping })
		crash(t, harness)

		// Asleep in its retry: not running, so not cut off.
		setup.SetNow(func() float64 { return float64(time.Now().UnixMilli()) + 120_000 })
		setup.Faux.SetResponses([]ai.FauxResponseStep{
			toolCallStep(ai.FauxToolCall("hold", map[string]any{}, &ai.FauxToolCallOptions{ID: "c1"})),
			fauxAnswer("done"),
		})
		reopened, _ := run.open()
		if record := must(reopened.GetTask(testContext, taskId)); record.Restarts != 0 {
			t.Fatalf("restarts = %d, want none", record.Restarts)
		}
		reopened.Resume()
		// Waiting for its tool round: not running either.
		<-started.done
		waitFor(t, func() bool { return must(reopened.GetTask(testContext, taskId)).State.Status == durable.TaskWaiting })
		if phase := checkpointOfTask(t, must(reopened.GetTask(testContext, taskId))).Phase; phase != generationTools {
			t.Fatalf("phase = %s, want tools", phase)
		}
		crash(t, reopened)

		again, _ := run.open()
		if record := must(again.GetTask(testContext, taskId)); record.Restarts != 0 {
			t.Fatalf("restarts = %d, want none", record.Restarts)
		}
		gate.resolve()
		again.Resume()
		if settled := settledSubmission(t, again, id); settled.Status != durable.SubmissionDone {
			t.Fatalf("settled = %s", jsonText(t, settled))
		}
		closeHarness(t, again)
	})

	t.Run("ends a run whose generation is cut off restartLimit times outside a request", func(t *testing.T) {
		run := &restartRun{t: t, storage: newControlledStorage(), setup: chatSetup(t)}
		setup := run.setup
		setup.SetSettings(func(settings *HarnessSettings) { settings.RestartLimit = new(1.0) })
		reached := deferred()
		// Preparation never finishes, as one that exhausts the host's memory would not.
		addSection(t, setup.Registry, "preamble", func(ctx context.Context, _ durable.PromptInput) (*string, error) {
			reached.resolve()
			return nil, abortedBy(ctx)
		})
		harness, root := run.open()
		harness.Resume()
		id := submitInput(t, root, "hi").Id()
		<-reached.done
		taskId := liveSnapshot(t, harness, root).Run.TaskId
		crash(t, harness)

		reopened, reopenedRoot := run.open()
		reopened.Resume()
		text := "The generation was interrupted once in its prepare phase"
		expectUnanswered(t, settledSubmission(t, reopened, id), "restart_limit", text)
		expectRunFailure(t, liveSnapshot(t, reopened, reopenedRoot), RunFailure{TaskId: taskId, Inputs: []durable.SubmissionId{id}, Reason: RunRestartLimit, Detail: &text})
		waitFor(t, func() bool { return len(setup.Reports.allInfos()) > 0 })
		expectInfo(t, setup.Reports.allInfos(), func(info durable.ReportInfo) bool {
			return info.Type == durable.ReportTaskInterrupted && info.Reason == "restart_limit" && info.Restarts == 1
		})
		closeHarness(t, reopened)
	})
}

func expectInfo(t *testing.T, infos []durable.ReportInfo, match func(durable.ReportInfo) bool) {
	t.Helper()
	for _, info := range infos {
		if match(info) {
			return
		}
	}
	t.Fatalf("report infos = %s, want a matching one", jsonText(t, infos))
}

func TestOtherTasksCutOffRestartLimitTimesInARow(t *testing.T) {
	// #11139
	t.Run("ends a tool call with an error result instead of running it again", func(t *testing.T) {
		run := &restartRun{t: t, storage: newControlledStorage(), setup: chatSetup(t)}
		setup := run.setup
		setup.SetSettings(func(settings *HarnessSettings) {
			settings.RestartLimit = new(2.0)
			settings.Progress = &ProgressPolicyPatch{OutputIntervalMs: new(0.0), PersistWithinMs: new(0.0)}
		})
		var mu sync.Mutex
		runs := 0
		running := deferred()
		addTool(t, setup.Registry, DefineTool(durable.ToolRegistration{
			ToolSchema: ai.ToolSchema{Name: "heavy", Description: "Never finishes", Parameters: emptyObjectSchema()},
			Replay:     durable.ToolReplaySafe,
			Execute: func(ctx context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
				mu.Lock()
				runs++
				current := running
				mu.Unlock()
				api.Output("partial\n")
				current.resolve()
				return durable.ToolExecutionResult{}, abortedBy(ctx)
			},
		}))
		setup.Faux.SetResponses([]ai.FauxResponseStep{
			toolCallStep(ai.FauxToolCall("heavy", map[string]any{}, &ai.FauxToolCallOptions{ID: "c1"})),
			ai.FauxFactoryStep(func(request ai.TranscriptContext, _ ai.StreamOptions, _ *ai.FauxProviderState, _ *ai.Model) (ai.AssistantMessage, error) {
				result := request.Messages[len(request.Messages)-1].(ai.ToolResultMessage)
				text, _ := textOf(result)
				return ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("saw: " + text)}}.AssistantMessage(), nil
			}),
		})
		harness, root := run.open()
		harness.Resume()
		id := submitInput(t, root, "go").Id()
		for range 2 {
			mu.Lock()
			current := running
			mu.Unlock()
			<-current.done
			mu.Lock()
			running = deferred()
			mu.Unlock()
			stored()
			crash(t, harness)
			harness, root = run.open()
			harness.Resume()
		}
		if settled := settledSubmission(t, harness, id); settled.Status != durable.SubmissionDone {
			t.Fatalf("settled = %s", jsonText(t, settled))
		}
		mu.Lock()
		if runs != 2 {
			t.Fatalf("the tool ran %d times, want 2", runs)
		}
		mu.Unlock()
		var result durable.EntryRecord
		for _, entry := range allEntries(t, root) {
			if entry.Kind == durable.ToolResultEntry.Kind {
				result = entry
			}
		}
		message := result.Model[0].(ai.ToolResultMessage)
		if !message.IsError {
			t.Fatal("the result is not an error")
		}
		wantDiagnostics := `[{"severity":"error","code":"restart_limit","message":"Tool heavy was interrupted 2 times in a row; it is not run again"}]`
		if got := jsonText(t, result.Data.(durable.JsonObject).Must("diagnostics")); !strings.HasPrefix(got, wantDiagnostics[:len(wantDiagnostics)-1]) {
			t.Fatalf("diagnostics = %s, want %s", got, wantDiagnostics)
		}
		// The slot's partial output is kept, as for an interrupted call.
		if text, _ := textOf(message); !strings.Contains(text, "partial") {
			t.Fatalf("result text %q lacks the partial output", text)
		}
		kind := "pi.tool"
		toolTask := must(durable.Commit(testContext, harness, func(tx durable.Tx) (durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue], error) {
			page, err := tx.ScanTasks(durable.TaskQuery{Kind: &kind}, 10, nil)
			return page.Items[0], err
		}))
		outcome := toolTask.State.Outcome
		if toolTask.State.Status != durable.TaskTerminal || outcome.Status != durable.OutcomeFailed || jsonText(t, outcome.Error.Detail) != `{"reason":"restart_limit"}` {
			t.Fatalf("tool task state = %s", jsonText(t, toolTask.State))
		}
		want := durable.ReportInfo{Type: durable.ReportTaskInterrupted, Task: &durable.ReportTask{Id: toolTask.Id, Kind: "pi.tool", ConversationId: root.Id()}, Reason: "restart_limit", Restarts: 2}
		expectInfo(t, setup.Reports.allInfos(), func(info durable.ReportInfo) bool { return reflect.DeepEqual(info, want) })
		closeHarness(t, harness)
	})

	t.Run("fails a compaction into compactionFailure", func(t *testing.T) {
		run := &restartRun{t: t, storage: newControlledStorage(), setup: chatSetup(t)}
		setup := run.setup
		setup.SetSettings(func(settings *HarnessSettings) {
			settings.RestartLimit = new(1.0)
			// Little recent context is kept, so the first turn is summarized.
			settings.Compaction = &CompactionPolicyPatch{Enabled: new(false), KeepRecentTokens: new(10)}
		})
		setup.Faux.SetResponses([]ai.FauxResponseStep{fauxAnswer("a1"), fauxAnswer("a2")})
		harness, root := run.open()
		harness.Resume()
		must(submitInput(t, root, strings.Repeat("u1 ", 200)).Wait(testContext))
		must(submitInput(t, root, strings.Repeat("u2 ", 200)).Wait(testContext))
		// The summary request never returns.
		hanging := unanswered()
		setup.Faux.SetResponses([]ai.FauxResponseStep{hanging.step})
		compaction := must(root.Compact(testContext, nil))
		<-hanging.reached
		crash(t, harness)

		reopened, reopenedRoot := run.open()
		reopened.Resume()
		message := "Compaction was interrupted once"
		outcome := must(reopened.WaitForTask(testContext, compaction)).State.Outcome
		want := &durable.TaskOutcome[durable.JsonValue]{Status: durable.OutcomeFailed, Error: &durable.TaskOutcomeError{Message: message, Detail: durable.JsonObject{"reason": "restart_limit"}}}
		if jsonText(t, outcome) != jsonText(t, want) {
			t.Fatalf("outcome = %s, want %s", jsonText(t, outcome), jsonText(t, want))
		}
		live := liveSnapshot(t, reopened, reopenedRoot)
		if live.CompactionFailure == nil || *live.CompactionFailure != (CompactionFailure{TaskId: compaction, Reason: durable.CompactionManual, Error: message}) || live.Compactions != nil {
			t.Fatalf("pi.live = %s", jsonText(t, live))
		}
		waitFor(t, func() bool { return len(setup.Reports.allInfos()) > 0 })
		wantInfo := durable.ReportInfo{Type: durable.ReportTaskInterrupted, Task: &durable.ReportTask{Id: compaction, Kind: "pi.compaction", ConversationId: reopenedRoot.Id()}, Reason: "restart_limit", Restarts: 1}
		expectInfo(t, setup.Reports.allInfos(), func(info durable.ReportInfo) bool { return reflect.DeepEqual(info, wantInfo) })
		closeHarness(t, reopened)
	})
}

// deferredRestartSetup is a setup whose model answers deferred, and whose first fetch of each deferred response is in
// flight until the Harness stops; later fetches answer. fetching resolves when the first fetch began.
func deferredRestartSetup(t *testing.T, pollAfterMs int64) (setup *chatState, fetching *deferredGate, fetches func() int) {
	t.Helper()
	faux := ai.NewFauxProvider(ai.FauxConfig{Deferred: &ai.FauxDeferredConfig{PollAfterMS: &pollAfterMs}})
	provider := faux.Provider()
	fetching = deferred()
	var mu sync.Mutex
	count := 0
	fetch := provider.FetchDeferred
	provider.FetchDeferred = func(ctx context.Context, model *ai.Model, handle ai.DeferredHandle, options ai.DeferredFetchOptions) (*ai.AssistantMessageEventStream, error) {
		mu.Lock()
		count++
		first := count == 1
		mu.Unlock()
		if !first {
			return fetch(ctx, model, handle, options)
		}
		fetching.resolve()
		return nil, abortedBy(options.Signal)
	}
	models := ai.CreateModels()
	models.SetProvider(provider)
	setup = &chatState{Faux: faux, Models: models, Registry: CreateRegistry(), Reports: &reportLog{}, now: func() float64 { return float64(time.Now().UnixMilli()) }}
	setup.SetSettings(func(settings *HarnessSettings) {
		settings.Stream = &durable.ConversationStreamOptions{Deferred: &ai.DeferredOption{Enabled: true}}
	})
	setup.Faux.SetResponses([]ai.FauxResponseStep{fauxAnswer("deferred answer")})
	return setup, fetching, func() int {
		mu.Lock()
		defer mu.Unlock()
		return count
	}
}

// crashWhilePolling submits to a fresh chat, lets its deferred response be fetched, and crashes during that fetch.
func (run *restartRun) crashWhilePolling(fetching *deferredGate) (durable.SubmissionId, durable.TaskId) {
	t := run.t
	t.Helper()
	harness, root := run.open()
	harness.Resume()
	id := submitInput(t, root, "hi").Id()
	<-fetching.done
	taskId := liveSnapshot(t, harness, root).Run.TaskId
	record := must(harness.GetTask(testContext, taskId))
	if record.State.Status != durable.TaskRunning || checkpointOfTask(t, record).Phase != generationPoll {
		t.Fatalf("task state = %s, want running in poll", jsonText(t, record.State))
	}
	crash(t, harness)
	return id, taskId
}

func cancelledDeferred(setup *chatState) []ai.DeferredHandle {
	return setup.Faux.(*ai.FauxProviderHandle).State().CancelledDeferred()
}

func TestCrashEdges(t *testing.T) {
	// #11139
	t.Run("charges a crash during a poll's fetch, then polls the same deferred response again", func(t *testing.T) {
		setup, fetching, fetches := deferredRestartSetup(t, 10)
		run := &restartRun{t: t, storage: newControlledStorage(), setup: setup}
		retryPolicy(setup, true, 2, 1)
		id, taskId := run.crashWhilePolling(fetching)

		harness, root := run.open()
		values := observeLive(t, harness)
		if record := must(harness.GetTask(testContext, taskId)); record.Restarts != 1 {
			t.Fatalf("restarts = %d, want 1", record.Restarts)
		}
		harness.Resume()
		if settled := settledSubmission(t, harness, id); settled.Status != durable.SubmissionDone {
			t.Fatalf("settled = %s", jsonText(t, settled))
		}
		retry := hasRetry(values.all())
		if retry == nil || retry.Attempt != 1 || retry.Retry.Error != "The host stopped while the deferred answer was fetched, on attempt 1" {
			t.Fatalf("retry generation = %s", jsonText(t, retry))
		}
		polled := false
		for _, value := range values.all() {
			polled = polled || (value.Generation != nil && value.Generation.Attempt == 2 && value.Generation.Deferred != nil)
		}
		if !polled {
			t.Fatal("no pi.live value shows attempt 2 polling its deferred response")
		}
		// One request, polled twice: the provider's job is neither dropped nor submitted again.
		if calls := setup.Faux.CallCount(); calls != 1 {
			t.Fatalf("model calls = %d, want 1", calls)
		}
		if got := fetches(); got != 2 {
			t.Fatalf("fetches = %d, want 2", got)
		}
		if cancelled := cancelledDeferred(setup); len(cancelled) != 0 {
			t.Fatalf("cancelled = %v, want none", cancelled)
		}
		entries := allEntries(t, root)
		if got, _ := textOf(entries[len(entries)-1].Model[0]); got != "deferred answer" {
			t.Fatalf("answer = %q", got)
		}
		closeHarness(t, harness)
	})

	t.Run("cancels the deferred response when a crash during its poll ends the run", func(t *testing.T) {
		setup, fetching, _ := deferredRestartSetup(t, 10)
		run := &restartRun{t: t, storage: newControlledStorage(), setup: setup}
		retryPolicy(setup, false, 0, 0)
		id, taskId := run.crashWhilePolling(fetching)
		harness, root := run.open()
		harness.Resume()
		text := "The host stopped while the deferred answer was fetched, on attempt 1"
		expectUnanswered(t, settledSubmission(t, harness, id), "interrupted", text)
		expectRunFailure(t, liveSnapshot(t, harness, root), RunFailure{TaskId: taskId, Inputs: []durable.SubmissionId{id}, Reason: RunInterrupted, Detail: &text})
		if cancelled := cancelledDeferred(setup); len(cancelled) != 1 {
			t.Fatalf("cancelled = %v, want one", cancelled)
		}
		closeHarness(t, harness)
	})

	t.Run("charges an interrupted poll even when the clock moved backwards", func(t *testing.T) {
		setup, fetching, _ := deferredRestartSetup(t, 10)
		run := &restartRun{t: t, storage: newControlledStorage(), setup: setup}
		var mu sync.Mutex
		now := 1000.0
		setNow := func(value float64) {
			mu.Lock()
			defer mu.Unlock()
			now = value
		}
		setup.SetNow(func() float64 {
			mu.Lock()
			defer mu.Unlock()
			return now
		})
		retryPolicy(setup, false, 0, 0)
		first, root := run.open()
		first.Resume()
		id := submitInput(t, root, "hi").Id()
		waitFor(t, func() bool {
			live := liveSnapshot(t, first, root)
			return live.Generation != nil && live.Generation.Deferred != nil
		})
		setNow(1010)
		first.Resume()
		<-fetching.done
		crash(t, first)
		// A replacement host whose clock is behind: the poll looks not due yet, but its fetch was cut off.
		setNow(1000)
		harness, _ := run.open()
		harness.Resume()
		expectUnanswered(t, settledSubmission(t, harness, id), "interrupted", "")
		closeHarness(t, harness)
	})

	t.Run("charges a request once when a crash is followed by a clean close before the charge", func(t *testing.T) {
		run := &restartRun{t: t, storage: newControlledStorage(), setup: streamingSetup(t)}
		retryPolicy(run.setup, true, 3, 1)
		id, taskId := run.crashWhileStreaming()
		run.setup.Faux.SetResponses([]ai.FauxResponseStep{fauxAnswer("answer")})
		harness, _ := run.open()
		closeHarness(t, harness)
		harness, root := run.open()
		if record := must(harness.GetTask(testContext, taskId)); record.Restarts != 1 {
			t.Fatalf("restarts = %d, want 1", record.Restarts)
		}
		values := observeLive(t, harness)
		harness.Resume()
		if settled := settledSubmission(t, harness, id); settled.Status != durable.SubmissionDone {
			t.Fatalf("settled = %s", jsonText(t, settled))
		}
		var charged []int
		for _, value := range values.all() {
			if value.Generation != nil && value.Generation.Retry != nil {
				charged = append(charged, value.Generation.Attempt)
			}
		}
		if !reflect.DeepEqual(charged, []int{1}) {
			t.Fatalf("charged attempts = %v, want [1]", charged)
		}
		expectEqualJSON(t, entryKinds(allEntries(t, root)), jsonText(t, []string{"pi.user", "pi.assistant", "pi.assistant"}))
		closeHarness(t, harness)
	})

	t.Run("ends a run restart_limit when charging the interrupted request keeps being cut off", func(t *testing.T) {
		run := &restartRun{t: t, storage: newControlledStorage(), setup: streamingSetup(t)}
		run.setup.SetSettings(func(settings *HarnessSettings) { settings.RestartLimit = new(1.0) })
		id, taskId := run.crashWhileStreaming()
		// At the limit: the partial is still kept.
		run.setup.Faux.SetResponses(nil)
		harness, root := run.open()
		partial, _ := textOf(*liveSnapshot(t, harness, root).Generation.Message)
		harness.Resume()
		text := "The generation was interrupted once in its request phase"
		expectUnanswered(t, settledSubmission(t, harness, id), "restart_limit", text)
		expectRunFailure(t, liveSnapshot(t, harness, root), RunFailure{TaskId: taskId, Inputs: []durable.SubmissionId{id}, Reason: RunRestartLimit, Detail: &text})
		entries := allEntries(t, root)
		expectEqualJSON(t, entryKinds(entries), jsonText(t, []string{"pi.user", "pi.assistant"}))
		if got, _ := textOf(entries[1].Model[0]); got != partial {
			t.Fatalf("kept text %q, want the stored partial %q", got, partial)
		}
		if calls := run.setup.Faux.CallCount(); calls != 1 {
			t.Fatalf("model calls = %d, want only the interrupted request", calls)
		}
		closeHarness(t, harness)
	})

	t.Run("drops the partial past the limit, where converting it may be what keeps failing", func(t *testing.T) {
		run := &restartRun{t: t, storage: newControlledStorage(), setup: streamingSetup(t)}
		id, taskId := run.crashWhileStreaming()
		// As if the charge had been cut off twice before: the open after this crash counts 3, past the limit of 2.
		run.storage.Reopen()
		record := must(run.storage.Task(testContext, taskId))
		record.Restarts = 2
		must(run.storage.Commit(testContext, []durable.StorageWrite{durable.TaskWrite{Value: *record}}, func(durable.Seq) {}))
		run.setup.SetSettings(func(settings *HarnessSettings) { settings.RestartLimit = new(2.0) })
		run.setup.Faux.SetResponses(nil)
		harness, root := run.open()
		harness.Resume()
		expectUnanswered(t, settledSubmission(t, harness, id), "restart_limit", "The generation was interrupted 3 times in a row in its request phase")
		expectEqualJSON(t, entryKinds(allEntries(t, root)), jsonText(t, []string{"pi.user"}))
		if generation := liveSnapshot(t, harness, root).Generation; generation != nil {
			t.Fatalf("generation = %s, want none", jsonText(t, generation))
		}
		closeHarness(t, harness)
	})

	t.Run("ends a run someone aborted as aborted when its abort handler is cut off restartLimit times", func(t *testing.T) {
		pollAfter := int64(3_600_000)
		faux := ai.NewFauxProvider(ai.FauxConfig{Deferred: &ai.FauxDeferredConfig{PollAfterMS: &pollAfter}})
		provider := faux.Provider()
		var mu sync.Mutex
		cancels := 0
		// The abort handler of a polling generation cancels its response: make that hang.
		provider.CancelDeferred = func(ctx context.Context, _ *ai.Model, _ ai.DeferredHandle, _ ai.DeferredCancelOptions) error {
			mu.Lock()
			cancels++
			mu.Unlock()
			<-ctx.Done()
			return context.Cause(ctx)
		}
		models := ai.CreateModels()
		models.SetProvider(provider)
		setup := &chatState{Faux: faux, Models: models, Registry: CreateRegistry(), Reports: &reportLog{}, now: func() float64 { return float64(time.Now().UnixMilli()) }}
		setup.SetSettings(func(settings *HarnessSettings) {
			settings.RestartLimit = new(2.0)
			settings.Stream = &durable.ConversationStreamOptions{Deferred: &ai.DeferredOption{Enabled: true}}
		})
		setup.Faux.SetResponses([]ai.FauxResponseStep{fauxAnswer("never")})
		run := &restartRun{t: t, storage: newControlledStorage(), setup: setup}
		harness, root := run.open()
		harness.Resume()
		id := submitInput(t, root, "hi").Id()
		taskId := liveSnapshot(t, harness, root).Run.TaskId
		waitFor(t, func() bool { return must(harness.GetTask(testContext, taskId)).State.Status == durable.TaskSleeping })
		go func() { _ = root.Abort(context.Background()) }()
		for crashes := 1; crashes <= 2; crashes++ {
			waitFor(t, func() bool {
				mu.Lock()
				defer mu.Unlock()
				return cancels == crashes
			})
			crash(t, harness)
			harness, root = run.open()
			if record := must(harness.GetTask(testContext, taskId)); record.Restarts != crashes {
				t.Fatalf("restarts = %d, want %d", record.Restarts, crashes)
			}
			harness.Resume()
		}
		detail := fmt.Sprintf("Abort handler of task %d (pi.generation) was cut off 2 times", taskId)
		expectUnanswered(t, settledSubmission(t, harness, id), "aborted", detail)
		expectRunFailure(t, liveSnapshot(t, harness, root), RunFailure{TaskId: taskId, Inputs: []durable.SubmissionId{id}, Reason: RunAborted, Detail: &detail})
		outcome := must(harness.WaitForTask(testContext, taskId)).State.Outcome
		if outcome.Status != durable.OutcomeFaulted || jsonText(t, outcome.Error.Detail) != `{"reason":"restart_limit"}` {
			t.Fatalf("outcome = %s, want faulted with reason restart_limit", jsonText(t, outcome))
		}
		waitFor(t, func() bool {
			for _, info := range setup.Reports.allInfos() {
				if info.Type == durable.ReportTaskFaulted && info.Reason == "restart_limit" {
					return true
				}
			}
			return false
		})
		closeHarness(t, harness)
	})
}

func TestReportsOfWorkCrashesEnded(t *testing.T) {
	// #11139
	t.Run("reports task_interrupted for a run that ends interrupted and for a task that ends at the limit", func(t *testing.T) {
		run := &restartRun{t: t, storage: newControlledStorage(), setup: streamingSetup(t)}
		retryPolicy(run.setup, false, 0, 0)
		_, taskId := run.crashWhileStreaming()
		harness, root := run.open()
		harness.Resume()
		must(harness.WaitForTask(testContext, taskId))
		waitFor(t, func() bool { return len(run.setup.Reports.allInfos()) > 0 })
		want := []durable.ReportInfo{{Type: durable.ReportTaskInterrupted, Task: &durable.ReportTask{Id: taskId, Kind: "pi.generation", ConversationId: root.Id()}, Reason: "interrupted", Restarts: 1}}
		if got := run.setup.Reports.allInfos(); !reflect.DeepEqual(got, want) {
			t.Fatalf("report infos = %s, want %s", jsonText(t, got), jsonText(t, want))
		}
		if !strings.Contains(run.setup.Reports.all()[0].Error(), "The host stopped while the model was answering, on attempt 1") {
			t.Fatalf("report = %v", run.setup.Reports.all()[0])
		}
		closeHarness(t, harness)
	})
}

func TestTheRestartLimitOnTheNormalPath(t *testing.T) {
	// #11139
	t.Run("resolves the settings no more often than before: only a task that was cut off reads the limit", func(t *testing.T) {
		setup := chatSetup(t)
		addTool(t, setup.Registry, noopTool("t"))
		const rounds = 5
		var responses []ai.FauxResponseStep
		for round := 0; round <= rounds; round++ {
			responses = append(responses,
				toolCallStep(ai.FauxToolCall("t", map[string]any{}, &ai.FauxToolCallOptions{ID: fmt.Sprintf("c%d", round)})),
				fauxAnswer("done"))
		}
		setup.Faux.SetResponses(responses)
		harness, root := openChat(t, newControlledStorage(), setup)
		harness.Resume()
		must(submitInput(t, root, "warm up").Wait(testContext))
		before := setup.settingsReads.Load()
		for round := range rounds {
			must(submitInput(t, root, fmt.Sprintf("round %d", round)).Wait(testContext))
		}
		// One generation round with one tool call, as before the restart limit applied to every kind.
		if got := (setup.settingsReads.Load() - before) / rounds; got != settingsReadsPerRound {
			t.Fatalf("settings reads per round = %d, want %d", got, settingsReadsPerRound)
		}
		closeHarness(t, harness)
	})
}

// settingsReadsPerRound is how often the Harness resolves its settings for one generation round with one tool call.
const settingsReadsPerRound = 0
