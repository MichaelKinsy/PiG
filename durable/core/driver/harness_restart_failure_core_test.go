// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package driver_test

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/harness"
	sqlitenode "github.com/MichaelKinsy/PiG/durable/storage/sqlite/node"
)

// Ports packages/durable/test/harness-restart-failure.test.ts (Pi b3a29f11) over both engines: what a crash (a host that dies, is
// evicted, or is reset) does to work that was running. A generation's interrupted request is a failed attempt under the retry
// policy, and every other task ends visibly once it was cut off settings.restartLimit times in a row (#11139). A crash is
// crashRig.kill(): the next open reads the store as it stood, and the dead Harness is never closed against it. The two cases of
// harness-deferred-progress.test.ts that #11139 changed ("converts what was stored into the aborted entry after a crash, and
// retries the charged attempt", "never counts progress commits as progress of a task cut off again and again") follow the
// restart-failure cases.
//
// Not natively portable: TestTheRestartLimitOnTheNormalPath counts settings resolutions of a settings source the host passes to
// the reference Harness; it stays in durable/harness (harness_restart_failure_test.go).

// restartRig is a crashRig with the faux model, a settable clock and the reports of every Harness it opened.
type restartRig struct {
	*crashRig
	faux   *ai.FauxProviderHandle
	models *ai.Models
	now    atomic.Pointer[func() float64]
	infos  *infoLog
}

func newRestartRigWith(t *testing.T, core bool, faux *ai.FauxProviderHandle, models *ai.Models) *restartRig {
	t.Helper()
	return &restartRig{crashRig: newCrashRig(t, core, harness.CreateRegistry(), harness.HarnessSettings{}), faux: faux, models: models, infos: &infoLog{}}
}

func newRestartRig(t *testing.T, core bool, config ai.FauxConfig) *restartRig {
	t.Helper()
	faux := ai.NewFauxProvider(config)
	models := ai.CreateModels()
	models.SetProvider(faux.Provider())
	return newRestartRigWith(t, core, faux, models)
}

// streamingRig is a rig whose model streams a long answer slowly; partials are stored as soon as they are committed.
func streamingRig(t *testing.T, core bool) *restartRig {
	t.Helper()
	one := 1
	rig := newRestartRig(t, core, ai.FauxConfig{TokensPerSecond: 20, TokenSize: &ai.FauxTokenSize{Min: &one, Max: &one}})
	rig.faux.SetResponses([]ai.FauxResponseStep{fauxAnswer(strings.Repeat("z", 400))})
	rig.configure(func(settings *harness.HarnessSettings) {
		settings.Progress = &harness.ProgressPolicyPatch{PersistWithinMs: new(0.0)}
	})
	return rig
}

// configure changes the host's live settings: a copy of the current value is changed and stored.
func (rig *restartRig) configure(change func(settings *harness.HarnessSettings)) {
	next := *rig.settings.Load()
	change(&next)
	rig.settings.Store(&next)
}

func (rig *restartRig) setNow(now func() float64) { rig.now.Store(&now) }

func (rig *restartRig) retryPolicy(enabled bool, maxRetries, baseDelayMs int) {
	rig.configure(func(settings *harness.HarnessSettings) {
		settings.Retry = &harness.RetryPolicyPatch{Enabled: new(enabled)}
		if enabled {
			settings.Retry.MaxRetries = new(maxRetries)
			settings.Retry.BaseDelayMs = new(baseDelayMs)
		}
	})
}

// open opens a Harness over the store and returns its root, configured with the faux model.
func (rig *restartRig) open() (harness.Harness, harness.Conversation) {
	rig.t.Helper()
	infos := rig.infos
	opened := rig.crashRig.open(func(options *harness.HarnessOptions) {
		options.Models = rig.models
		if now := rig.now.Load(); now != nil {
			options.Now = *now
		}
		options.OnReport = infos.add
	})
	root, err := opened.h.Root(rig.t.Context(), &harness.RootOptions{Agent: &harness.AgentChange{Model: harness.SetTo(durable.ModelRef{Provider: "faux", ModelId: "faux-1"})}})
	if err != nil {
		rig.t.Fatal(err)
	}
	return opened.h, root
}

// setRestarts stores restarts as the count of task id in the store file, as if the task had been cut off that often.
func (rig *restartRig) setRestarts(id durable.TaskId, restarts int) {
	rig.t.Helper()
	store, err := sqlitenode.OpenNodeSqliteStorage(rig.path, sqlitenode.NodeSqliteStorageOptions{})
	if err != nil {
		rig.t.Fatal(err)
	}
	record, err := store.Task(rig.t.Context(), id)
	if err != nil || record == nil {
		rig.t.Fatalf("task %d: %v, %v", id, record, err)
	}
	record.Restarts = restarts
	if _, err := store.Commit(rig.t.Context(), []durable.StorageWrite{durable.TaskWrite{Value: *record}}, func(durable.Seq) {}); err != nil {
		rig.t.Fatal(err)
	}
	if err := store.Close(rig.t.Context()); err != nil {
		rig.t.Fatal(err)
	}
}

func restartLive(t *testing.T, h harness.Harness, root harness.Conversation) harness.LiveState {
	t.Helper()
	live, err := durable.Snapshot(t.Context(), h, harness.LiveDoc, root.Id())
	if err != nil {
		t.Fatal(err)
	}
	if live == nil {
		return harness.LiveState{}
	}
	return *live
}

func restartJSON(t *testing.T, value any) string {
	t.Helper()
	text, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(text)
}

func restartOk[T any](t *testing.T, value T, err error) T {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	return value
}

// latch is a gate that opens once.
type latch struct {
	once sync.Once
	done chan struct{}
}

func newLatch() *latch { return &latch{done: make(chan struct{})} }

func (l *latch) open() { l.once.Do(func() { close(l.done) }) }

// blockedUntil fails with the cause of ctx once it ends.
func blockedUntil(ctx context.Context) error {
	<-ctx.Done()
	return context.Cause(ctx)
}

func restartEntries(t *testing.T, root harness.Conversation) []durable.EntryRecord {
	t.Helper()
	page, err := root.Entries(t.Context(), durable.EntryQuery{}, 1000, nil)
	if err != nil {
		t.Fatal(err)
	}
	entries := slices.Clone(page.Items)
	slices.Reverse(entries)
	return entries
}

func restartKinds(entries []durable.EntryRecord) []string {
	kinds := []string{}
	for _, entry := range entries {
		kinds = append(kinds, entry.Kind)
	}
	return kinds
}

func messageTextOf(message ai.Message) string {
	if typed, ok := message.(ai.AssistantMessage); ok {
		for _, block := range typed.Content {
			if text, ok := block.(ai.TextContent); ok {
				return text.Text
			}
		}
	}
	return ""
}

func restartStopReasons(entries []durable.EntryRecord) []ai.StopReason {
	var reasons []ai.StopReason
	for _, entry := range entries {
		if entry.Kind == durable.AssistantEntry.Kind {
			reasons = append(reasons, entry.Model[0].(ai.AssistantMessage).StopReason)
		}
	}
	return reasons
}

func restartRole(message ai.Message) string {
	switch message.(type) {
	case ai.UserMessage:
		return "user"
	case ai.AssistantMessage:
		return "assistant"
	case ai.ToolResultMessage:
		return "toolResult"
	}
	return "other"
}

func restartCheckpoint(t *testing.T, record *durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue]) harness.GenerationCheckpoint {
	t.Helper()
	if record.State.Checkpoint == nil {
		return harness.GenerationCheckpoint{}
	}
	return restartOk(t, durable.FromJsonValue[harness.GenerationCheckpoint](*record.State.Checkpoint))
}

func restartSettled(t *testing.T, id durable.SubmissionId, h harness.Harness) durable.SubmissionRecord {
	t.Helper()
	submission, err := h.Submission(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	return restartOk(t, submission.Wait(t.Context()))
}

func expectRestartDone(t *testing.T, settled durable.SubmissionRecord) {
	t.Helper()
	if settled.Status != durable.SubmissionDone {
		t.Fatalf("settled = %s", restartJSON(t, settled))
	}
}

func expectRestartUnanswered(t *testing.T, settled durable.SubmissionRecord, reason, detail string) {
	t.Helper()
	if settled.Status != durable.SubmissionUnanswered || settled.Reason == nil || *settled.Reason != reason {
		t.Fatalf("settled = %s", restartJSON(t, settled))
	}
	if detail != "" && settled.Detail != detail {
		t.Fatalf("settled detail = %v, want %q", settled.Detail, detail)
	}
}

func expectRestartRunFailure(t *testing.T, live harness.LiveState, want harness.RunFailure) {
	t.Helper()
	if !reflect.DeepEqual(live, harness.LiveState{RunFailure: &want}) {
		t.Fatalf("pi.live = %s, want only runFailure %s", restartJSON(t, live), restartJSON(t, want))
	}
}

func expectRestartInfo(t *testing.T, infos []durable.ReportInfo, match func(durable.ReportInfo) bool) {
	t.Helper()
	for _, info := range infos {
		if match(info) {
			return
		}
	}
	t.Fatalf("report infos = %s, want a matching one", restartJSON(t, infos))
}

func hasRetrying(values []harness.LiveState) *harness.LiveGeneration {
	for _, value := range values {
		if value.Generation != nil && value.Generation.Retry != nil {
			return value.Generation
		}
	}
	return nil
}

// crashWhileStreaming submits an input to a fresh chat and crashes once its streamed partial is stored.
func (rig *restartRig) crashWhileStreaming() (durable.SubmissionId, durable.TaskId) {
	t := rig.t
	t.Helper()
	h, root := rig.open()
	h.Resume()
	id := submitInput(t, root, "hi").Id()
	pollUntil(t, "a stored partial", func() bool {
		live := restartLive(t, h, root)
		return live.Generation != nil && live.Generation.Message != nil
	})
	taskId := restartLive(t, h, root).Run.TaskId
	rig.kill()
	return id, taskId
}

// crashAgain reopens after a crash in the middle of a request, with another streaming answer, and crashes again.
func (rig *restartRig) crashAgain(taskId durable.TaskId) {
	t := rig.t
	t.Helper()
	h, root := rig.open()
	h.Resume()
	// The first attempt was charged; the second one streams.
	pollUntil(t, "attempt 2 in its request phase", func() bool {
		record := taskOf(t, h, taskId)
		if record == nil || record.State.Status != durable.TaskRunning {
			return false
		}
		checkpoint := restartCheckpoint(t, record)
		return checkpoint.Phase == "request" && checkpoint.Attempt == 2
	})
	pollUntil(t, "a stored partial", func() bool {
		live := restartLive(t, h, root)
		return live.Generation != nil && live.Generation.Message != nil
	})
	rig.kill()
}

func TestCoreAGenerationInterruptedWhileTheModelAnswers(t *testing.T) {
	// #11139
	t.Run("charges the attempt: keeps the partial, waits for the retry backoff, and answers on the next attempt", func(t *testing.T) {
		eachEngine(t, func(t *testing.T, core bool) {
			rig := streamingRig(t, core)
			rig.retryPolicy(true, 2, 1)
			id, taskId := rig.crashWhileStreaming()

			var mu sync.Mutex
			var sent [][]string
			rig.faux.SetResponses([]ai.FauxResponseStep{ai.FauxFactoryStep(func(request ai.TranscriptContext, _ ai.StreamOptions, _ *ai.FauxProviderState, _ *ai.Model) (ai.AssistantMessage, error) {
				var roles []string
				for _, message := range request.Messages() {
					roles = append(roles, restartRole(message))
				}
				mu.Lock()
				sent = append(sent, roles)
				mu.Unlock()
				return ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("answer")}}.AssistantMessage(), nil
			})})
			h, root := rig.open()
			recording := viewHistory(t, h, root.Id())
			generation := restartLive(t, h, root).Generation
			partial := messageTextOf(*generation.Message)
			if record := taskOf(t, h, taskId); record.Restarts != 1 {
				t.Fatalf("restarts = %d, want 1", record.Restarts)
			}
			h.Resume()
			expectRestartDone(t, restartSettled(t, id, h))
			// The backoff showed in pi.live with why.
			pollUntil(t, "the views to be delivered", func() bool { return hasRetrying(recording.Live()) != nil })
			retry := hasRetrying(recording.Live())
			if retry.Attempt != 1 || retry.Retry.Error != "The host stopped while the model was answering, on attempt 1" || retry.Message != nil || retry.Deferred != nil {
				t.Fatalf("retry generation = %s", restartJSON(t, retry))
			}
			attempted := false
			for _, value := range recording.Live() {
				attempted = attempted || (value.Generation != nil && value.Generation.Attempt == 2)
			}
			if !attempted {
				t.Fatal("no pi.live value shows attempt 2")
			}
			entries := restartEntries(t, root)
			if got := restartKinds(entries); !reflect.DeepEqual(got, []string{"pi.user", "pi.assistant", "pi.assistant"}) {
				t.Fatalf("entry kinds = %v", got)
			}
			converted := entries[1].Model[0].(ai.AssistantMessage)
			if converted.StopReason != ai.StopReasonAborted {
				t.Fatalf("converted stop reason = %s, want aborted", converted.StopReason)
			}
			if got := messageTextOf(converted); got != partial {
				t.Fatalf("converted text %q, want the stored partial %q", got, partial)
			}
			// The aborted partial is excluded from the next request.
			mu.Lock()
			if !reflect.DeepEqual(sent, [][]string{{"user"}}) {
				t.Fatalf("sent = %v, want one request with the user message", sent)
			}
			mu.Unlock()
			if live := restartLive(t, h, root); !reflect.DeepEqual(live, harness.LiveState{}) {
				t.Fatalf("pi.live = %s, want empty", restartJSON(t, live))
			}
		})
	})

	t.Run("sleeps the backoff of settings.retry before the next attempt", func(t *testing.T) {
		eachEngine(t, func(t *testing.T, core bool) {
			rig := streamingRig(t, core)
			rig.retryPolicy(true, 2, 60_000)
			_, taskId := rig.crashWhileStreaming()

			rig.faux.SetResponses(nil)
			const now = 1_000_000.0
			rig.setNow(func() float64 { return now })
			h, root := rig.open()
			h.Resume()
			pollUntil(t, "the generation to sleep", func() bool { return taskOf(t, h, taskId).State.Status == durable.TaskSleeping })
			record := taskOf(t, h, taskId)
			if record.State.Until == nil || *record.State.Until != now+60_000 || restartCheckpoint(t, record).Phase != "retry" {
				t.Fatalf("task state = %s", restartJSON(t, record.State))
			}
			if record.Restarts != 0 {
				t.Fatalf("restarts = %d, want none", record.Restarts)
			}
			want := &harness.LiveGeneration{Attempt: 1, Retry: &harness.RetryStatus{At: now + 60_000, Error: "The host stopped while the model was answering, on attempt 1"}}
			if generation := restartLive(t, h, root).Generation; !reflect.DeepEqual(generation, want) {
				t.Fatalf("generation = %s, want %s", restartJSON(t, generation), restartJSON(t, want))
			}
			expectWake(t, h.Wake(), false, now+60_000)
		})
	})

	t.Run("ends the run interrupted once no retry is left, keeping every partial", func(t *testing.T) {
		eachEngine(t, func(t *testing.T, core bool) {
			rig := streamingRig(t, core)
			rig.retryPolicy(true, 1, 1)
			id, taskId := rig.crashWhileStreaming()
			// Attempt 2 is interrupted too.
			rig.faux.SetResponses([]ai.FauxResponseStep{fauxAnswer(strings.Repeat("z", 400))})
			rig.crashAgain(taskId)

			rig.faux.SetResponses(nil)
			h, root := rig.open()
			h.Resume()
			text := "The host stopped while the model was answering, on attempt 2"
			expectRestartUnanswered(t, restartSettled(t, id, h), "interrupted", text)
			expectRestartRunFailure(t, restartLive(t, h, root), harness.RunFailure{TaskId: taskId, Inputs: []durable.SubmissionId{id}, Reason: harness.RunInterrupted, Detail: &text})
			outcome := restartOk(t, h.WaitForTask(t.Context(), taskId)).State.Outcome
			want := &durable.TaskOutcome[durable.JsonValue]{Status: durable.OutcomeFailed, Error: &durable.TaskOutcomeError{Message: text, Detail: map[string]any{"reason": "interrupted"}}}
			if restartJSON(t, outcome) != restartJSON(t, want) {
				t.Fatalf("outcome = %s, want %s", restartJSON(t, outcome), restartJSON(t, want))
			}
			entries := restartEntries(t, root)
			if got := restartKinds(entries); !reflect.DeepEqual(got, []string{"pi.user", "pi.assistant", "pi.assistant"}) {
				t.Fatalf("entry kinds = %v", got)
			}
			if got := restartStopReasons(entries); !reflect.DeepEqual(got, []ai.StopReason{ai.StopReasonAborted, ai.StopReasonAborted}) {
				t.Fatalf("stop reasons = %v, want two aborted partials", got)
			}
			// No request was sent after the second crash.
			if calls := rig.faux.CallCount(); calls != 2 {
				t.Fatalf("model calls = %d, want only the two interrupted requests", calls)
			}
		})
	})

	t.Run("ends the run at once when retries are disabled", func(t *testing.T) {
		eachEngine(t, func(t *testing.T, core bool) {
			rig := streamingRig(t, core)
			id, _ := rig.crashWhileStreaming()
			rig.retryPolicy(false, 0, 0)
			h, _ := rig.open()
			h.Resume()
			expectRestartUnanswered(t, restartSettled(t, id, h), "interrupted", "")
		})
	})

	t.Run("resends after a clean close: a close is not a crash", func(t *testing.T) {
		eachEngine(t, func(t *testing.T, core bool) {
			rig := streamingRig(t, core)
			rig.retryPolicy(false, 0, 0)
			h, root := rig.open()
			h.Resume()
			id := submitInput(t, root, "hi").Id()
			pollUntil(t, "a stored partial", func() bool {
				live := restartLive(t, h, root)
				return live.Generation != nil && live.Generation.Message != nil
			})
			taskId := restartLive(t, h, root).Run.TaskId
			if err := h.Close(t.Context()); err != nil {
				t.Fatal(err)
			}

			rig.faux.SetResponses([]ai.FauxResponseStep{fauxAnswer("answer")})
			reopened, _ := rig.open()
			if record := taskOf(t, reopened, taskId); record.Restarts != 0 {
				t.Fatalf("restarts = %d, want none", record.Restarts)
			}
			reopened.Resume()
			expectRestartDone(t, restartSettled(t, id, reopened))
		})
	})

	t.Run("does not charge a generation that sleeps in a retry or waits for its tools", func(t *testing.T) {
		eachEngine(t, func(t *testing.T, core bool) {
			rig := newRestartRig(t, core, ai.FauxConfig{})
			rig.retryPolicy(true, 1, 60_000)
			gate, started := newLatch(), newLatch()
			installTools(t, rig.registry, harness.DefineTool(durable.ToolRegistration{
				ToolSchema: ai.ToolSchema{Name: "hold", Description: "Waits", Parameters: map[string]any{"type": "object", "properties": map[string]any{}}},
				Replay:     durable.ToolReplay("safe"),
				Execute: func(ctx context.Context, _ any, _ durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
					started.open()
					select {
					case <-gate.done:
					case <-ctx.Done():
						return durable.ToolExecutionResult{}, context.Cause(ctx)
					}
					return durable.ToolExecutionResult{}, nil
				},
			}))
			rig.faux.SetResponses([]ai.FauxResponseStep{ai.FauxStaticStep(ai.FauxResponse{StopReason: "error", ErrorMessage: "503 Service Unavailable"})})
			h, root := rig.open()
			h.Resume()
			id := submitInput(t, root, "hi").Id()
			pollUntil(t, "the run", func() bool { return restartLive(t, h, root).Run != nil })
			taskId := restartLive(t, h, root).Run.TaskId
			pollUntil(t, "the generation to sleep", func() bool { return taskOf(t, h, taskId).State.Status == durable.TaskSleeping })
			rig.kill()

			// Asleep in its retry: not running, so not cut off.
			rig.setNow(func() float64 { return float64(time.Now().UnixMilli()) + 120_000 })
			rig.faux.SetResponses([]ai.FauxResponseStep{
				toolCallStep(ai.FauxToolCall("hold", map[string]any{}, &ai.FauxToolCallOptions{ID: "c1"})),
				fauxAnswer("done"),
			})
			reopened, _ := rig.open()
			if record := taskOf(t, reopened, taskId); record.Restarts != 0 {
				t.Fatalf("restarts = %d, want none", record.Restarts)
			}
			reopened.Resume()
			// Waiting for its tool round: not running either.
			receiveWithin(t, started.done, "the tool to start")
			pollUntil(t, "the generation to wait", func() bool { return taskOf(t, reopened, taskId).State.Status == durable.TaskWaiting })
			if phase := restartCheckpoint(t, taskOf(t, reopened, taskId)).Phase; phase != "tools" {
				t.Fatalf("phase = %s, want tools", phase)
			}
			rig.kill()

			again, _ := rig.open()
			if record := taskOf(t, again, taskId); record.Restarts != 0 {
				t.Fatalf("restarts = %d, want none", record.Restarts)
			}
			gate.open()
			again.Resume()
			expectRestartDone(t, restartSettled(t, id, again))
		})
	})

	t.Run("ends a run whose generation is cut off restartLimit times outside a request", func(t *testing.T) {
		eachEngine(t, func(t *testing.T, core bool) {
			rig := newRestartRig(t, core, ai.FauxConfig{})
			rig.configure(func(settings *harness.HarnessSettings) { settings.RestartLimit = new(1.0) })
			reached := newLatch()
			// Preparation never finishes, as one that exhausts the host's memory would not.
			if err := rig.registry.Install(&durable.Extension{Name: "section:preamble", Sections: []*durable.PromptSection{harness.Section("preamble", func(ctx context.Context, _ durable.PromptInput) (*string, error) {
				reached.open()
				return nil, blockedUntil(ctx)
			})}}); err != nil {
				t.Fatal(err)
			}
			h, root := rig.open()
			h.Resume()
			id := submitInput(t, root, "hi").Id()
			receiveWithin(t, reached.done, "the section to render")
			taskId := restartLive(t, h, root).Run.TaskId
			rig.kill()

			reopened, reopenedRoot := rig.open()
			reopened.Resume()
			text := "The generation was interrupted once in its prepare phase"
			expectRestartUnanswered(t, restartSettled(t, id, reopened), "restart_limit", text)
			expectRestartRunFailure(t, restartLive(t, reopened, reopenedRoot), harness.RunFailure{TaskId: taskId, Inputs: []durable.SubmissionId{id}, Reason: harness.RunRestartLimit, Detail: &text})
			pollUntil(t, "the report", func() bool { return len(rig.infos.infos()) > 0 })
			expectRestartInfo(t, rig.infos.infos(), func(info durable.ReportInfo) bool {
				return info.Type == durable.ReportTaskInterrupted && info.Reason == "restart_limit" && info.Restarts == 1
			})
		})
	})
}

func toolCallStep(calls ...ai.FauxContentBlock) ai.FauxResponseStep {
	return ai.FauxStaticStep(ai.FauxResponse{Content: calls, StopReason: "toolUse"})
}

func installTools(t *testing.T, registry harness.Registry, tool *durable.ToolRegistration) {
	t.Helper()
	if err := registry.Install(&durable.Extension{Name: "tool:" + tool.Name, Tools: []*durable.ToolRegistration{tool}}); err != nil {
		t.Fatal(err)
	}
}

func TestCoreOtherTasksCutOffRestartLimitTimesInARow(t *testing.T) {
	// #11139
	t.Run("ends a tool call with an error result instead of running it again", func(t *testing.T) {
		eachEngine(t, func(t *testing.T, core bool) {
			rig := newRestartRig(t, core, ai.FauxConfig{})
			rig.configure(func(settings *harness.HarnessSettings) {
				settings.RestartLimit = new(2.0)
				settings.Progress = &harness.ProgressPolicyPatch{OutputIntervalMs: new(0.0), PersistWithinMs: new(0.0)}
			})
			var mu sync.Mutex
			runs := 0
			running := newLatch()
			installTools(t, rig.registry, harness.DefineTool(durable.ToolRegistration{
				ToolSchema: ai.ToolSchema{Name: "heavy", Description: "Never finishes", Parameters: map[string]any{"type": "object", "properties": map[string]any{}}},
				Replay:     durable.ToolReplay("safe"),
				Execute: func(ctx context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
					mu.Lock()
					runs++
					current := running
					mu.Unlock()
					api.Output("partial\n")
					current.open()
					return durable.ToolExecutionResult{}, blockedUntil(ctx)
				},
			}))
			rig.faux.SetResponses([]ai.FauxResponseStep{
				toolCallStep(ai.FauxToolCall("heavy", map[string]any{}, &ai.FauxToolCallOptions{ID: "c1"})),
				ai.FauxFactoryStep(func(request ai.TranscriptContext, _ ai.StreamOptions, _ *ai.FauxProviderState, _ *ai.Model) (ai.AssistantMessage, error) {
					result := request.Messages()[len(request.Messages())-1].(ai.ToolResultMessage)
					text := ""
					for _, block := range result.Content {
						if content, ok := block.(ai.TextContent); ok {
							text = content.Text
							break
						}
					}
					return ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("saw: " + text)}}.AssistantMessage(), nil
				}),
			})
			h, root := rig.open()
			h.Resume()
			id := submitInput(t, root, "go").Id()
			for range 2 {
				mu.Lock()
				current := running
				mu.Unlock()
				receiveWithin(t, current.done, "the tool to run")
				mu.Lock()
				running = newLatch()
				mu.Unlock()
				// The tool's output is stored before the crash.
				pollUntil(t, "the stored output", func() bool {
					live := restartLive(t, h, root)
					return len(live.Tools) > 0 && live.Tools[0].Output != nil && strings.Contains(*live.Tools[0].Output, "partial")
				})
				rig.kill()
				h, root = rig.open()
				h.Resume()
			}
			expectRestartDone(t, restartSettled(t, id, h))
			mu.Lock()
			if runs != 2 {
				t.Fatalf("the tool ran %d times, want 2", runs)
			}
			mu.Unlock()
			var result durable.EntryRecord
			for _, entry := range restartEntries(t, root) {
				if entry.Kind == durable.ToolResultEntry.Kind {
					result = entry
				}
			}
			message := result.Model[0].(ai.ToolResultMessage)
			if !message.IsError {
				t.Fatal("the result is not an error")
			}
			diagnostics := restartJSON(t, result.Data)
			for _, want := range []string{`"code":"restart_limit"`, `"severity":"error"`, `"message":"Tool heavy was interrupted 2 times in a row; it is not run again"`} {
				if !strings.Contains(diagnostics, want) {
					t.Fatalf("data = %s, want %s", diagnostics, want)
				}
			}
			// The slot's partial output is kept, as for an interrupted call.
			text := ""
			for _, block := range message.Content {
				if content, ok := block.(ai.TextContent); ok {
					text = content.Text
					break
				}
			}
			if !strings.Contains(text, "partial") {
				t.Fatalf("result text %q lacks the partial output", text)
			}
			kind := "pi.tool"
			toolTask := restartOk(t, durable.Commit(t.Context(), h, func(tx durable.Tx) (durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue], error) {
				page, err := tx.ScanTasks(durable.TaskQuery{Kind: &kind}, 10, nil)
				return page.Items[0], err
			}))
			outcome := toolTask.State.Outcome
			if toolTask.State.Status != durable.TaskTerminal || outcome.Status != durable.OutcomeFailed || restartJSON(t, outcome.Error.Detail) != `{"reason":"restart_limit"}` {
				t.Fatalf("tool task state = %s", restartJSON(t, toolTask.State))
			}
			want := durable.ReportInfo{Type: durable.ReportTaskInterrupted, Task: &durable.ReportTask{Id: toolTask.Id, Kind: "pi.tool", ConversationId: root.Id()}, Reason: "restart_limit", Restarts: 2}
			pollUntil(t, "the report", func() bool { return len(rig.infos.infos()) > 0 })
			expectRestartInfo(t, rig.infos.infos(), func(info durable.ReportInfo) bool { return reflect.DeepEqual(info, want) })
		})
	})

	t.Run("fails a compaction into compactionFailure", func(t *testing.T) {
		eachEngine(t, func(t *testing.T, core bool) {
			rig := newRestartRig(t, core, ai.FauxConfig{})
			rig.configure(func(settings *harness.HarnessSettings) {
				settings.RestartLimit = new(1.0)
				// Little recent context is kept, so the first turn is summarized.
				settings.Compaction = &harness.CompactionPolicyPatch{Enabled: new(false), KeepRecentTokens: new(10)}
			})
			rig.faux.SetResponses([]ai.FauxResponseStep{fauxAnswer("a1"), fauxAnswer("a2")})
			h, root := rig.open()
			h.Resume()
			restartOk(t, submitInput(t, root, strings.Repeat("u1 ", 200)).Wait(t.Context()))
			restartOk(t, submitInput(t, root, strings.Repeat("u2 ", 200)).Wait(t.Context()))
			// The summary request never returns.
			hanging := unanswered()
			rig.faux.SetResponses([]ai.FauxResponseStep{hanging.step})
			compaction := restartOk(t, root.Compact(t.Context(), nil))
			receiveWithin(t, hanging.reached, "the summary request")
			rig.kill()

			reopened, reopenedRoot := rig.open()
			reopened.Resume()
			message := "Compaction was interrupted once"
			outcome := restartOk(t, reopened.WaitForTask(t.Context(), compaction)).State.Outcome
			want := &durable.TaskOutcome[durable.JsonValue]{Status: durable.OutcomeFailed, Error: &durable.TaskOutcomeError{Message: message, Detail: map[string]any{"reason": "restart_limit"}}}
			if restartJSON(t, outcome) != restartJSON(t, want) {
				t.Fatalf("outcome = %s, want %s", restartJSON(t, outcome), restartJSON(t, want))
			}
			live := restartLive(t, reopened, reopenedRoot)
			if live.CompactionFailure == nil || *live.CompactionFailure != (harness.CompactionFailure{TaskId: compaction, Reason: durable.CompactionManual, Error: message}) || live.Compactions != nil {
				t.Fatalf("pi.live = %s", restartJSON(t, live))
			}
			pollUntil(t, "the report", func() bool { return len(rig.infos.infos()) > 0 })
			wantInfo := durable.ReportInfo{Type: durable.ReportTaskInterrupted, Task: &durable.ReportTask{Id: compaction, Kind: "pi.compaction", ConversationId: reopenedRoot.Id()}, Reason: "restart_limit", Restarts: 1}
			expectRestartInfo(t, rig.infos.infos(), func(info durable.ReportInfo) bool { return reflect.DeepEqual(info, wantInfo) })
		})
	})
}

// deferredRig is a rig whose model answers deferred, and whose first fetch of each deferred response is in flight until the
// Harness stops; later fetches answer. fetching opens when the first fetch began.
func deferredRig(t *testing.T, core bool, pollAfterMs int64) (rig *restartRig, fetching *latch, fetches func() int) {
	t.Helper()
	faux := ai.NewFauxProvider(ai.FauxConfig{Deferred: &ai.FauxDeferredConfig{PollAfterMS: &pollAfterMs}})
	provider := faux.Provider()
	fetching = newLatch()
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
		fetching.open()
		return nil, blockedUntil(options.Signal)
	}
	models := ai.CreateModels()
	models.SetProvider(provider)
	rig = newRestartRigWith(t, core, faux, models)
	rig.setNow(func() float64 { return float64(time.Now().UnixMilli()) })
	rig.configure(func(settings *harness.HarnessSettings) {
		settings.Stream = &durable.ConversationStreamOptions{Deferred: &ai.DeferredOption{Enabled: true}}
	})
	faux.SetResponses([]ai.FauxResponseStep{fauxAnswer("deferred answer")})
	return rig, fetching, func() int {
		mu.Lock()
		defer mu.Unlock()
		return count
	}
}

// crashWhilePolling submits to a fresh chat, lets its deferred response be fetched, and crashes during that fetch.
func (rig *restartRig) crashWhilePolling(fetching *latch) (durable.SubmissionId, durable.TaskId) {
	t := rig.t
	t.Helper()
	h, root := rig.open()
	h.Resume()
	id := submitInput(t, root, "hi").Id()
	receiveWithin(t, fetching.done, "the first fetch")
	taskId := restartLive(t, h, root).Run.TaskId
	record := taskOf(t, h, taskId)
	if record.State.Status != durable.TaskRunning || restartCheckpoint(t, record).Phase != "poll" {
		t.Fatalf("task state = %s, want running in poll", restartJSON(t, record.State))
	}
	rig.kill()
	return id, taskId
}

func TestCoreCrashEdges(t *testing.T) {
	// #11139
	t.Run("charges a crash during a poll's fetch, then polls the same deferred response again", func(t *testing.T) {
		eachEngine(t, func(t *testing.T, core bool) {
			rig, fetching, fetches := deferredRig(t, core, 10)
			rig.retryPolicy(true, 2, 1)
			id, taskId := rig.crashWhilePolling(fetching)

			h, root := rig.open()
			recording := viewHistory(t, h, root.Id())
			if record := taskOf(t, h, taskId); record.Restarts != 1 {
				t.Fatalf("restarts = %d, want 1", record.Restarts)
			}
			h.Resume()
			expectRestartDone(t, restartSettled(t, id, h))
			pollUntil(t, "the views to be delivered", func() bool { return hasRetrying(recording.Live()) != nil })
			retry := hasRetrying(recording.Live())
			if retry.Attempt != 1 || retry.Retry.Error != "The host stopped while the deferred answer was fetched, on attempt 1" {
				t.Fatalf("retry generation = %s", restartJSON(t, retry))
			}
			polled := false
			for _, value := range recording.Live() {
				polled = polled || (value.Generation != nil && value.Generation.Attempt == 2 && value.Generation.Deferred != nil)
			}
			if !polled {
				t.Fatal("no pi.live value shows attempt 2 polling its deferred response")
			}
			// One request, polled twice: the provider's job is neither dropped nor submitted again.
			if calls := rig.faux.CallCount(); calls != 1 {
				t.Fatalf("model calls = %d, want 1", calls)
			}
			if got := fetches(); got != 2 {
				t.Fatalf("fetches = %d, want 2", got)
			}
			if cancelled := rig.faux.State().CancelledDeferred(); len(cancelled) != 0 {
				t.Fatalf("cancelled = %v, want none", cancelled)
			}
			entries := restartEntries(t, root)
			if got := messageTextOf(entries[len(entries)-1].Model[0]); got != "deferred answer" {
				t.Fatalf("answer = %q", got)
			}
		})
	})

	t.Run("cancels the deferred response when a crash during its poll ends the run", func(t *testing.T) {
		eachEngine(t, func(t *testing.T, core bool) {
			rig, fetching, _ := deferredRig(t, core, 10)
			rig.retryPolicy(false, 0, 0)
			id, taskId := rig.crashWhilePolling(fetching)
			h, root := rig.open()
			h.Resume()
			text := "The host stopped while the deferred answer was fetched, on attempt 1"
			expectRestartUnanswered(t, restartSettled(t, id, h), "interrupted", text)
			expectRestartRunFailure(t, restartLive(t, h, root), harness.RunFailure{TaskId: taskId, Inputs: []durable.SubmissionId{id}, Reason: harness.RunInterrupted, Detail: &text})
			if cancelled := rig.faux.State().CancelledDeferred(); len(cancelled) != 1 {
				t.Fatalf("cancelled = %v, want one", cancelled)
			}
		})
	})

	t.Run("charges an interrupted poll even when the clock moved backwards", func(t *testing.T) {
		eachEngine(t, func(t *testing.T, core bool) {
			rig, fetching, _ := deferredRig(t, core, 10)
			var mu sync.Mutex
			now := 1000.0
			setNow := func(value float64) {
				mu.Lock()
				defer mu.Unlock()
				now = value
			}
			rig.setNow(func() float64 {
				mu.Lock()
				defer mu.Unlock()
				return now
			})
			rig.retryPolicy(false, 0, 0)
			first, root := rig.open()
			first.Resume()
			id := submitInput(t, root, "hi").Id()
			pollUntil(t, "a deferred generation", func() bool {
				live := restartLive(t, first, root)
				return live.Generation != nil && live.Generation.Deferred != nil
			})
			setNow(1010)
			first.Resume()
			receiveWithin(t, fetching.done, "the first fetch")
			rig.kill()
			// A replacement host whose clock is behind: the poll looks not due yet, but its fetch was cut off.
			setNow(1000)
			h, _ := rig.open()
			h.Resume()
			expectRestartUnanswered(t, restartSettled(t, id, h), "interrupted", "")
		})
	})

	t.Run("charges a request once when a crash is followed by a clean close before the charge", func(t *testing.T) {
		eachEngine(t, func(t *testing.T, core bool) {
			rig := streamingRig(t, core)
			rig.retryPolicy(true, 3, 1)
			id, taskId := rig.crashWhileStreaming()
			rig.faux.SetResponses([]ai.FauxResponseStep{fauxAnswer("answer")})
			h, _ := rig.open()
			if err := h.Close(t.Context()); err != nil {
				t.Fatal(err)
			}
			h, root := rig.open()
			if record := taskOf(t, h, taskId); record.Restarts != 1 {
				t.Fatalf("restarts = %d, want 1", record.Restarts)
			}
			recording := viewHistory(t, h, root.Id())
			h.Resume()
			expectRestartDone(t, restartSettled(t, id, h))
			pollUntil(t, "the views to be delivered", func() bool { return hasRetrying(recording.Live()) != nil })
			var charged []int
			for _, value := range recording.Live() {
				if value.Generation != nil && value.Generation.Retry != nil {
					charged = append(charged, value.Generation.Attempt)
				}
			}
			if !reflect.DeepEqual(charged, []int{1}) {
				t.Fatalf("charged attempts = %v, want [1]", charged)
			}
			if got := restartKinds(restartEntries(t, root)); !reflect.DeepEqual(got, []string{"pi.user", "pi.assistant", "pi.assistant"}) {
				t.Fatalf("entry kinds = %v", got)
			}
		})
	})

	t.Run("ends a run restart_limit when charging the interrupted request keeps being cut off", func(t *testing.T) {
		eachEngine(t, func(t *testing.T, core bool) {
			rig := streamingRig(t, core)
			rig.configure(func(settings *harness.HarnessSettings) { settings.RestartLimit = new(1.0) })
			id, taskId := rig.crashWhileStreaming()
			// At the limit: the partial is still kept.
			rig.faux.SetResponses(nil)
			h, root := rig.open()
			partial := messageTextOf(*restartLive(t, h, root).Generation.Message)
			h.Resume()
			text := "The generation was interrupted once in its request phase"
			expectRestartUnanswered(t, restartSettled(t, id, h), "restart_limit", text)
			expectRestartRunFailure(t, restartLive(t, h, root), harness.RunFailure{TaskId: taskId, Inputs: []durable.SubmissionId{id}, Reason: harness.RunRestartLimit, Detail: &text})
			entries := restartEntries(t, root)
			if got := restartKinds(entries); !reflect.DeepEqual(got, []string{"pi.user", "pi.assistant"}) {
				t.Fatalf("entry kinds = %v", got)
			}
			if got := messageTextOf(entries[1].Model[0]); got != partial {
				t.Fatalf("kept text %q, want the stored partial %q", got, partial)
			}
			if calls := rig.faux.CallCount(); calls != 1 {
				t.Fatalf("model calls = %d, want only the interrupted request", calls)
			}
		})
	})

	t.Run("drops the partial past the limit, where converting it may be what keeps failing", func(t *testing.T) {
		eachEngine(t, func(t *testing.T, core bool) {
			rig := streamingRig(t, core)
			id, taskId := rig.crashWhileStreaming()
			// As if the charge had been cut off twice before: the open after this crash counts 3, past the limit of 2.
			rig.setRestarts(taskId, 2)
			rig.configure(func(settings *harness.HarnessSettings) { settings.RestartLimit = new(2.0) })
			rig.faux.SetResponses(nil)
			h, root := rig.open()
			h.Resume()
			expectRestartUnanswered(t, restartSettled(t, id, h), "restart_limit", "The generation was interrupted 3 times in a row in its request phase")
			if got := restartKinds(restartEntries(t, root)); !reflect.DeepEqual(got, []string{"pi.user"}) {
				t.Fatalf("entry kinds = %v", got)
			}
			if generation := restartLive(t, h, root).Generation; generation != nil {
				t.Fatalf("generation = %s, want none", restartJSON(t, generation))
			}
		})
	})

	t.Run("ends a run someone aborted as aborted when its abort handler is cut off restartLimit times", func(t *testing.T) {
		eachEngine(t, func(t *testing.T, core bool) {
			pollAfter := int64(3_600_000)
			faux := ai.NewFauxProvider(ai.FauxConfig{Deferred: &ai.FauxDeferredConfig{PollAfterMS: &pollAfter}})
			provider := faux.Provider()
			var cancels atomic.Int32
			// The abort handler of a polling generation cancels its response: make that hang.
			provider.CancelDeferred = func(ctx context.Context, _ *ai.Model, _ ai.DeferredHandle, _ ai.DeferredCancelOptions) error {
				cancels.Add(1)
				return blockedUntil(ctx)
			}
			models := ai.CreateModels()
			models.SetProvider(provider)
			rig := newRestartRigWith(t, core, faux, models)
			rig.setNow(func() float64 { return float64(time.Now().UnixMilli()) })
			rig.configure(func(settings *harness.HarnessSettings) {
				settings.RestartLimit = new(2.0)
				settings.Stream = &durable.ConversationStreamOptions{Deferred: &ai.DeferredOption{Enabled: true}}
			})
			faux.SetResponses([]ai.FauxResponseStep{fauxAnswer("never")})
			h, root := rig.open()
			h.Resume()
			id := submitInput(t, root, "hi").Id()
			pollUntil(t, "the run", func() bool { return restartLive(t, h, root).Run != nil })
			taskId := restartLive(t, h, root).Run.TaskId
			pollUntil(t, "the generation to sleep", func() bool { return taskOf(t, h, taskId).State.Status == durable.TaskSleeping })
			go func() { _ = root.Abort(context.Background(), nil) }()
			for crashes := 1; crashes <= 2; crashes++ {
				pollUntil(t, "the abort handler to cancel", func() bool { return int(cancels.Load()) == crashes })
				rig.kill()
				h, root = rig.open()
				if record := taskOf(t, h, taskId); record.Restarts != crashes {
					t.Fatalf("restarts = %d, want %d", record.Restarts, crashes)
				}
				h.Resume()
			}
			detail := fmt.Sprintf("Abort handler of task %d (pi.generation) was cut off 2 times", taskId)
			expectRestartUnanswered(t, restartSettled(t, id, h), "aborted", detail)
			expectRestartRunFailure(t, restartLive(t, h, root), harness.RunFailure{TaskId: taskId, Inputs: []durable.SubmissionId{id}, Reason: harness.RunAborted, Detail: &detail})
			outcome := restartOk(t, h.WaitForTask(t.Context(), taskId)).State.Outcome
			if outcome.Status != durable.OutcomeFaulted || restartJSON(t, outcome.Error.Detail) != `{"reason":"restart_limit"}` {
				t.Fatalf("outcome = %s, want faulted with reason restart_limit", restartJSON(t, outcome))
			}
			pollUntil(t, "the task_faulted report", func() bool {
				for _, info := range rig.infos.infos() {
					if info.Type == durable.ReportTaskFaulted && info.Reason == "restart_limit" {
						return true
					}
				}
				return false
			})
		})
	})
}

func TestCoreReportsOfWorkCrashesEnded(t *testing.T) {
	// #11139
	t.Run("reports task_interrupted for a run that ends interrupted and for a task that ends at the limit", func(t *testing.T) {
		eachEngine(t, func(t *testing.T, core bool) {
			rig := streamingRig(t, core)
			rig.retryPolicy(false, 0, 0)
			_, taskId := rig.crashWhileStreaming()
			// What the crash itself reported is not what this test is about.
			rig.infos = &infoLog{}
			h, root := rig.open()
			h.Resume()
			restartOk(t, h.WaitForTask(t.Context(), taskId))
			pollUntil(t, "the report", func() bool { return len(rig.infos.infos()) > 0 })
			want := []durable.ReportInfo{{Type: durable.ReportTaskInterrupted, Task: &durable.ReportTask{Id: taskId, Kind: "pi.generation", ConversationId: root.Id()}, Reason: "interrupted", Restarts: 1}}
			if got := rig.infos.infos(); !reflect.DeepEqual(got, want) {
				t.Fatalf("report infos = %s, want %s", restartJSON(t, got), restartJSON(t, want))
			}
			if !strings.Contains(rig.infos.all()[0].err.Error(), "The host stopped while the model was answering, on attempt 1") {
				t.Fatalf("report = %v", rig.infos.all()[0].err)
			}
		})
	})
}

// harness-deferred-progress.test.ts "converts what was stored into the aborted entry after a crash, and retries the charged
// attempt" (#11139) over both engines, without the replica of SubscribeCommits (durable/harness covers it).
func TestCoreDeferredGenerationPartialsAfterACrash(t *testing.T) {
	eachEngine(t, func(t *testing.T, core bool) {
		four := 4
		rig := newRestartRig(t, core, ai.FauxConfig{TokensPerSecond: 80, TokenSize: &ai.FauxTokenSize{Min: &four, Max: &four}})
		rig.configure(func(settings *harness.HarnessSettings) {
			settings.Progress = &harness.ProgressPolicyPatch{PersistWithinMs: new(150.0)}
		})
		// A request a crash interrupted is a failed attempt (#11139): retry at once.
		rig.retryPolicy(true, 5, 1)
		rig.faux.SetResponses([]ai.FauxResponseStep{fauxAnswer(strings.Repeat("0123456789", 100))})
		h, root := rig.open()
		h.Resume()
		submission := submitInput(t, root, "hi")
		var stored string
		pollUntil(t, "a stored partial", func() bool {
			live := restartLive(t, h, root)
			if live.Generation == nil || live.Generation.Message == nil {
				return false
			}
			stored = messageTextOf(*live.Generation.Message)
			return len(stored) > 40
		})
		rig.kill()

		rig.faux.SetResponses([]ai.FauxResponseStep{fauxAnswer("answer")})
		reopened, reopenedRoot := rig.open()
		reopened.Resume()
		expectRestartDone(t, restartSettled(t, submission.Id(), reopened))
		var got []string
		for _, entry := range restartEntries(t, reopenedRoot) {
			if entry.Kind == durable.AssistantEntry.Kind {
				message := entry.Model[0].(ai.AssistantMessage)
				got = append(got, fmt.Sprintf("%s|%s", messageTextOf(message), message.StopReason))
			}
		}
		if len(got) != 2 || !strings.HasSuffix(got[0], "|"+string(ai.StopReasonAborted)) || !strings.HasPrefix(strings.TrimSuffix(got[0], "|"+string(ai.StopReasonAborted)), stored) || got[1] != "answer|"+string(ai.StopReasonStop) {
			t.Fatalf("answers = %q, want the stored partial %q aborted, then the answer", got, stored)
		}
		if live := restartLive(t, reopened, reopenedRoot); !reflect.DeepEqual(live, harness.LiveState{}) {
			t.Fatalf("pi.live = %s, want empty", restartJSON(t, live))
		}
	})
}

// harness-deferred-progress.test.ts "never counts progress commits as progress of a task cut off again and again" (#11139) over
// both engines: a generation's request is charged at once after a crash, so its partials never stream with a count; a
// replay-safe tool reruns and streams its output with one.
func TestCoreDeferredToolOutputIsNotProgress(t *testing.T) {
	eachEngine(t, func(t *testing.T, core bool) {
		four := 4
		rig := newRestartRig(t, core, ai.FauxConfig{TokensPerSecond: 80, TokenSize: &ai.FauxTokenSize{Min: &four, Max: &four}})
		rig.configure(func(settings *harness.HarnessSettings) {
			settings.Progress = &harness.ProgressPolicyPatch{PersistWithinMs: new(50.0)}
		})
		rig.retryPolicy(true, 5, 1)
		var runs atomic.Int32
		installTools(t, rig.registry, harness.DefineTool(durable.ToolRegistration{
			ToolSchema: ai.ToolSchema{Name: "work", Description: "work", Parameters: map[string]any{"type": "object", "properties": map[string]any{}}},
			Replay:     durable.ToolReplay("safe"),
			Execute: func(ctx context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
				run := runs.Add(1)
				for line := 0; ctx.Err() == nil; line++ {
					api.Output(fmt.Sprintf("run %d line %d\n", run, line))
					time.Sleep(20 * time.Millisecond)
				}
				return durable.ToolExecutionResult{}, context.Cause(ctx)
			},
		}))
		rig.faux.SetResponses([]ai.FauxResponseStep{toolCallStep(ai.FauxToolCall("work", map[string]any{}, &ai.FauxToolCallOptions{ID: "c1"}))})
		h, root := rig.open()
		h.Resume()
		submitInput(t, root, "go")
		for crashes := 1; crashes <= 2; crashes++ {
			// This run stored output of its own, a progress commit, before it was cut off.
			prefix := fmt.Sprintf("run %d ", crashes)
			pollUntil(t, "the stored output of the run", func() bool {
				live := restartLive(t, h, root)
				return len(live.Tools) > 0 && live.Tools[0].Output != nil && strings.HasPrefix(*live.Tools[0].Output, prefix)
			})
			rig.kill()
			h, root = rig.open()
			h.Resume()
			kind := "pi.tool"
			tool := restartOk(t, durable.Commit(t.Context(), h, func(tx durable.Tx) (durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue], error) {
				page, err := tx.ScanTasks(durable.TaskQuery{Kind: &kind}, 1, nil)
				return page.Items[0], err
			}))
			if tool.Restarts != crashes {
				t.Fatalf("after %d crashes the tool task counts %d restarts", crashes, tool.Restarts)
			}
		}
	})
}
