// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package driver_test

import (
	"context"
	"math"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/core/driver"
	"github.com/MichaelKinsy/PiG/durable/harness"
	"github.com/MichaelKinsy/PiG/durable/storage"
)

// compactionFailureRig is one Harness over the reference or the core with a faux model of a 1000-token window and the blocking
// threshold policy: a first turn of 4000 characters puts the second turn over the blocking threshold, so the second turn waits
// for a threshold compaction whose summary request is the faux provider's second step.
type compactionFailureRig struct {
	h    harness.Harness
	root harness.Conversation
	// settings is the host's live settings value: the Harness reads it at each use from its own goroutines, so a change replaces the
	// whole value (see crashRig.settings).
	settings *atomic.Pointer[harness.HarnessSettings]
	faux     *ai.FauxProviderHandle
	models   *ai.Models
}

func openCompactionFailureRig(t *testing.T, core bool, steps ...ai.FauxResponseStep) *compactionFailureRig {
	t.Helper()
	faux := ai.NewFauxProvider(ai.FauxConfig{Models: []ai.FauxModelDefinition{{ID: "faux-1", ContextWindow: 1000, MaxTokens: 900}}})
	models := ai.CreateModels()
	models.SetProvider(faux.Provider())
	faux.SetResponses(append([]ai.FauxResponseStep{
		ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("ok")}, StopReason: "stop"}),
	}, steps...))
	settings := new(atomic.Pointer[harness.HarnessSettings])
	settings.Store(&harness.HarnessSettings{Stream: &durable.ConversationStreamOptions{CacheRetention: ai.CacheRetention("none")}, Compaction: thresholdPolicy()})
	options := harness.HarnessOptions{Models: models, Registry: harness.CreateRegistry(), Settings: settings.Load}
	var h harness.Harness
	var err error
	if core {
		h, err = driver.OpenHarness(t.Context(), filepath.Join(t.TempDir(), "store.db"), options)
	} else {
		h, err = harness.OpenHarness(t.Context(), storage.NewMemoryStorage(), options)
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close(context.Background()) })
	h.Resume()
	root, err := h.Root(t.Context(), &harness.RootOptions{Agent: &harness.AgentChange{Model: harness.SetTo(durable.ModelRef{Provider: "faux", ModelId: "faux-1"})}})
	if err != nil {
		t.Fatal(err)
	}
	return &compactionFailureRig{h: h, root: root, settings: settings, faux: faux, models: models}
}

// changeSettings changes the host's live settings: a copy of the current value is changed and stored.
func (r *compactionFailureRig) changeSettings(change func(settings *harness.HarnessSettings)) {
	next := *r.settings.Load()
	change(&next)
	r.settings.Store(&next)
}

func (r *compactionFailureRig) submit(t *testing.T, text string) durable.SubmissionRecord {
	t.Helper()
	sub, err := r.root.Submit(t.Context(), durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText(text)})
	if err != nil {
		t.Fatal(err)
	}
	settled, err := sub.Wait(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return settled
}

func (r *compactionFailureRig) live(t *testing.T) *harness.LiveState {
	t.Helper()
	live, err := durable.Snapshot(t.Context(), r.h, harness.LiveDoc, r.root.Id())
	if err != nil || live == nil {
		t.Fatalf("live %+v, %v", live, err)
	}
	return live
}

func (r *compactionFailureRig) firstTurn(t *testing.T) {
	t.Helper()
	if first := r.submit(t, strings.Repeat("x", 4000)); first.Status != durable.SubmissionDone {
		t.Fatalf("first turn %+v", first)
	}
}

func expectUnansweredModelError(t *testing.T, settled durable.SubmissionRecord, detail string) {
	t.Helper()
	if settled.Status != durable.SubmissionUnanswered || settled.Reason == nil || *settled.Reason != "model_error" {
		t.Fatalf("second turn settled %+v, want unanswered model_error", settled)
	}
	if got, _ := settled.Detail.(string); got != detail {
		t.Fatalf("detail %v, want %q", settled.Detail, detail)
	}
}

func eachEngine(t *testing.T, run func(t *testing.T, core bool)) {
	for _, core := range []bool{false, true} {
		name := "reference"
		if core {
			name = "core"
		}
		t.Run(name, func(t *testing.T) { run(t, core) })
	}
}

// harness-compaction.test.ts:1128 over the core and the reference: a generation whose blocking threshold compaction failed fails with
// model_error (Compaction failed: <error>) and sends no request, and pi.live.compactionFailure keeps the failure.
func TestCoreAFailedBlockingCompactionFailsTheRunAndIsRecorded(t *testing.T) {
	eachEngine(t, func(t *testing.T, core bool) {
		r := openCompactionFailureRig(t, core, ai.FauxStaticStep(ai.FauxResponse{StopReason: "error", ErrorMessage: "bad request"}))
		r.firstTurn(t)
		expectUnansweredModelError(t, r.submit(t, strings.Repeat("y", 500)), "Compaction failed: Summarization failed: bad request")
		if pending := r.faux.CallCount(); pending != 2 {
			t.Fatalf("provider calls = %d, want 2 (first turn and summary, no request after the failed compaction)", pending)
		}
		live := r.live(t)
		if failure := live.CompactionFailure; failure == nil || failure.Reason != "threshold" || failure.Error != "Summarization failed: bad request" || live.Compactions != nil {
			t.Fatalf("live compactionFailure %+v, compactions %+v", live.CompactionFailure, live.Compactions)
		}
	})
}

// harness-compaction.test.ts:1007 over the core and the reference: compaction_end carries a failed compaction's error, the agent
// events' snapshot carries pi.live.compactionFailure, and a compaction that produces a summary removes it and ends without an
// error.
func TestCoreACompactionFailureIsVisibleUntilASummaryIsProduced(t *testing.T) {
	eachEngine(t, func(t *testing.T, core bool) {
		r := openCompactionFailureRig(t, core,
			ai.FauxStaticStep(ai.FauxResponse{StopReason: "error", ErrorMessage: "bad request"}),
			ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("SUMMARY")}, StopReason: "stop"}),
		)
		var mu sync.Mutex
		var events []harness.AgentEvent
		stream, err := harness.WatchEvents(t.Context(), r.h, r.root.Id())
		if err != nil {
			t.Fatal(err)
		}
		stream.Start(func(_ context.Context, batch []harness.AgentEvent) error {
			mu.Lock()
			defer mu.Unlock()
			events = append(events, batch...)
			return nil
		})
		endOf := func(taskId durable.TaskId) (harness.CompactionEndEvent, bool) {
			mu.Lock()
			defer mu.Unlock()
			for _, event := range events {
				if end, ok := event.(harness.CompactionEndEvent); ok && end.TaskId == taskId {
					return end, true
				}
			}
			return harness.CompactionEndEvent{}, false
		}
		waitEnd := func(taskId durable.TaskId) harness.CompactionEndEvent {
			t.Helper()
			for deadline := time.Now().Add(20 * time.Second); ; time.Sleep(5 * time.Millisecond) {
				if end, ok := endOf(taskId); ok {
					return end
				}
				if time.Now().After(deadline) {
					t.Fatalf("no compaction_end for task %d", taskId)
				}
			}
		}
		r.firstTurn(t)
		expectUnansweredModelError(t, r.submit(t, strings.Repeat("y", 500)), "Compaction failed: Summarization failed: bad request")
		recorded := r.live(t).CompactionFailure
		if recorded == nil || recorded.Reason != "threshold" || recorded.Error != "Summarization failed: bad request" {
			t.Fatalf("compactionFailure = %+v", recorded)
		}
		if end := waitEnd(recorded.TaskId); end.TaskId != recorded.TaskId || end.Reason != "threshold" || end.Error == nil || *end.Error != "Summarization failed: bad request" {
			t.Fatalf("compaction_end = %+v", end)
		}
		reopened, err := harness.WatchEvents(t.Context(), r.h, r.root.Id())
		if err != nil {
			t.Fatal(err)
		}
		if got := reopened.Snapshot.CompactionFailure; got == nil || *got != *recorded {
			t.Fatalf("snapshot compactionFailure = %+v, want %+v", got, recorded)
		}
		if _, err := reopened.Stop(); err != nil {
			t.Fatal(err)
		}
		manual, err := r.root.Compact(t.Context(), nil)
		if err != nil {
			t.Fatal(err)
		}
		if end := waitEnd(manual); end.Error != nil || end.Reason != "manual" {
			t.Fatalf("a produced summary ended with %+v", end)
		}
		if err := r.h.WaitForIdle(t.Context()); err != nil {
			t.Fatal(err)
		}
		if failure := r.live(t).CompactionFailure; failure != nil {
			t.Fatalf("a summary left compactionFailure = %+v", failure)
		}
		if _, err := stream.Stop(); err != nil {
			t.Fatal(err)
		}
	})
}

// harness-compaction.test.ts:1151 over the core and the reference: aborting the blocking compaction directly fails the waiting
// run with "Compaction was aborted", sends no request, and records no failure (aborting is not a failure).
func TestCoreAnAbortedBlockingCompactionFailsTheRunWithoutRecordingAFailure(t *testing.T) {
	eachEngine(t, func(t *testing.T, core bool) {
		reached := make(chan struct{})
		var once sync.Once
		r := openCompactionFailureRig(t, core, ai.FauxFactoryStep(func(_ ai.TranscriptContext, options ai.StreamOptions, _ *ai.FauxProviderState, _ *ai.Model) (ai.AssistantMessage, error) {
			once.Do(func() { close(reached) })
			<-options.Signal.Done()
			return ai.AssistantMessage{}, context.Cause(options.Signal)
		}))
		r.firstTurn(t)
		sub, err := r.root.Submit(t.Context(), durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText(strings.Repeat("y", 500))})
		if err != nil {
			t.Fatal(err)
		}
		select {
		case <-reached:
		case <-time.After(20 * time.Second):
			t.Fatal("the summary request was not made")
		}
		live := r.live(t)
		if len(live.Compactions) != 1 {
			t.Fatalf("compactions %+v, want one", live.Compactions)
		}
		if _, err := r.h.AbortTask(t.Context(), live.Compactions[0].TaskId); err != nil {
			t.Fatal(err)
		}
		settled, err := sub.Wait(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		expectUnansweredModelError(t, settled, "Compaction was aborted")
		if calls := r.faux.CallCount(); calls != 2 {
			t.Fatalf("provider calls = %d, want 2 (no request after the aborted compaction)", calls)
		}
		if failure := r.live(t).CompactionFailure; failure != nil {
			t.Fatalf("an aborted compaction recorded a failure: %+v", failure)
		}
	})
}

// harness-compaction.test.ts:1954 over the core and the reference: a blocking compaction the scheduler faults fails the waiting run
// with the fault's message, and pi.live.compactionFailure records the same message (live.ts settleSchedulerOutcome). Pi makes
// completeSimple throw; here the summary carries a NaN cost, which the settling commit cannot store as strict JSON, as in the
// harness port of the same test.
func TestCoreAFaultedBlockingCompactionFailsTheRunAndIsRecorded(t *testing.T) {
	eachEngine(t, func(t *testing.T, core bool) {
		r := openCompactionFailureRig(t, core)
		r.firstTurn(t)
		provider := *r.faux.Provider()
		original := provider.StreamSimple
		provider.StreamSimple = func(ctx context.Context, model *ai.Model, request ai.TranscriptContext, options ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
			final := &ai.AssistantMessage{API: "faux", Provider: "faux", Model: "faux-1", StopReason: ai.StopReasonStop, Content: []ai.AssistantContentBlock{ai.TextContent{Text: "SUMMARY"}}, Timestamp: time.Now().UnixMilli()}
			final.Usage.Cost.Total = math.NaN()
			stream := ai.NewAssistantMessageEventStream()
			go func() {
				_ = stream.Push(ai.StartEvent{Partial: &ai.AssistantMessage{API: "faux", Provider: "faux", Model: "faux-1", StopReason: ai.StopReasonPending}})
				stream.End(final)
			}()
			return stream, nil
		}
		r.models.SetProvider(&provider)
		settled := r.submit(t, strings.Repeat("y", 500))
		restored := *r.faux.Provider()
		restored.StreamSimple = original
		r.models.SetProvider(&restored)
		live := r.live(t)
		failure := live.CompactionFailure
		if failure == nil || failure.Reason != "threshold" || failure.Error == "" || live.Compactions != nil {
			t.Fatalf("live compactionFailure %+v, compactions %+v", failure, live.Compactions)
		}
		const strictJSON = "Value contains a non-finite number and is not strict JSON"
		if failure.Error != strictJSON {
			t.Fatalf("compactionFailure.error = %q, want %q", failure.Error, strictJSON)
		}
		expectUnansweredModelError(t, settled, "Compaction failed: "+strictJSON)
		conversationID := r.root.Id()
		page, err := durable.Commit(t.Context(), r.h, func(tx durable.Tx) (durable.Page[durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue], durable.Cursor], error) {
			return tx.ScanTasks(durable.TaskQuery{ConversationId: &conversationID}, 50, nil)
		})
		if err != nil {
			t.Fatal(err)
		}
		faulted := false
		for _, task := range page.Items {
			if task.Id == failure.TaskId {
				faulted = task.State.Outcome != nil && task.State.Outcome.Status == durable.OutcomeFaulted
			}
		}
		if !faulted {
			t.Fatalf("compaction task %d did not fault: %+v", failure.TaskId, page.Items)
		}
	})
}

// harness-compaction.test.ts:2394 (Pi c4cf7957, #10942) over the core and the reference: the current model's
// compaction.modelOverrides entry sets the blocking threshold. With the Harness-wide policy disabled, only an override
// that enables compaction for faux/faux-1 makes the second turn compact first.
func TestCorePerModelCompactionOverrideSetsTheBlockingThreshold(t *testing.T) {
	eachEngine(t, func(t *testing.T, core bool) {
		for _, key := range []string{"faux/faux-1", "faux/other"} {
			r := openCompactionFailureRig(t, core,
				ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("SUMMARY")}, StopReason: "stop"}),
				ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("ok")}, StopReason: "stop"}),
			)
			policy := thresholdPolicy()
			policy.Enabled = new(false)
			policy.ModelOverrides = map[string]durable.CompactionPolicyOverride{key: {Enabled: new(true)}}
			r.changeSettings(func(settings *harness.HarnessSettings) { settings.Compaction = policy })
			r.firstTurn(t)
			if second := r.submit(t, strings.Repeat("y", 500)); second.Status != durable.SubmissionDone {
				t.Fatalf("override %s: second turn %+v", key, second)
			}
			compacted := r.live(t).Compactions != nil || r.faux.CallCount() == 3
			if want := key == "faux/faux-1"; compacted != want {
				t.Fatalf("override %s: compacted %v (provider calls %d), want %v", key, compacted, r.faux.CallCount(), want)
			}
		}
	})
}
