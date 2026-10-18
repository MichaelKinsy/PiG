// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package driver_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/core/driver"
	"github.com/MichaelKinsy/PiG/durable/harness"
	"github.com/MichaelKinsy/PiG/durable/storage"
)

// Ports packages/durable/test/harness-failed-run-inbox.test.ts (Pi 482c1381a0) over both engines: what a run that fails with
// model_error does with the inputs queued behind it, under each settings.afterFailedRun policy.

type failedRunRig struct {
	h    harness.Harness
	root harness.Conversation
	faux *ai.FauxProviderHandle
}

// openFailedRun opens a Harness over the reference or the core with the faux model and settings.afterFailedRun set to policy
// (empty: the default).
func openFailedRun(t *testing.T, core bool, policy durable.AfterFailedRun, steps ...ai.FauxResponseStep) *failedRunRig {
	t.Helper()
	faux := ai.NewFauxProvider(ai.FauxConfig{Models: []ai.FauxModelDefinition{{ID: "faux-1"}}})
	models := ai.CreateModels()
	models.SetProvider(faux.Provider())
	faux.SetResponses(steps)
	settings := &harness.HarnessSettings{Stream: &durable.ConversationStreamOptions{CacheRetention: ai.CacheRetention("none")}, AfterFailedRun: policy}
	options := harness.HarnessOptions{Models: models, Registry: harness.CreateRegistry(), Settings: func() *harness.HarnessSettings { return settings }}
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
	root, err := h.Root(t.Context(), &harness.RootOptions{Agent: &harness.AgentChange{Model: harness.SetTo(durable.ModelRef{Provider: "faux", ModelId: "faux-1"})}})
	if err != nil {
		t.Fatal(err)
	}
	return &failedRunRig{h: h, root: root, faux: faux}
}

// failingOnce is a response that fails for good once gate opens; reached closes when it was requested.
func failingOnce() (step ai.FauxResponseStep, gate, reached chan struct{}) {
	gate, reached = make(chan struct{}), make(chan struct{})
	step = ai.FauxFactoryStep(func(ai.TranscriptContext, ai.StreamOptions, *ai.FauxProviderState, *ai.Model) (ai.AssistantMessage, error) {
		close(reached)
		<-gate
		return ai.FauxAssistantMessage(nil, ai.FauxAssistantMessageOptions{StopReason: "error", ErrorMessage: "Invalid request"}), nil
	})
	return step, gate, reached
}

func fauxText(text string) ai.FauxResponseStep {
	return ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText(text)}, StopReason: "stop"})
}

func submitInputWhenBusy(t *testing.T, conversation harness.Conversation, text string, whenBusy durable.WhenBusy) durable.Submission {
	t.Helper()
	submission, err := conversation.Submit(t.Context(), durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText(text), WhenBusy: whenBusy})
	if err != nil {
		t.Fatal(err)
	}
	return submission
}

func settledOf(t *testing.T, submission durable.Submission) durable.SettledSubmissionRecord {
	t.Helper()
	settled, err := submission.Wait(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return settled
}

func expectUnanswered(t *testing.T, settled durable.SettledSubmissionRecord, reason string) {
	t.Helper()
	if settled.Status != durable.SubmissionUnanswered || settled.Reason == nil || *settled.Reason != reason {
		t.Fatalf("settled %+v, want unanswered %s", settled, reason)
	}
}

// failWithQueuedInput runs a run that fails with an input queued behind it, under policy; it returns how the queued input settles.
func failWithQueuedInput(t *testing.T, core bool, policy durable.AfterFailedRun) (*failedRunRig, durable.Submission) {
	t.Helper()
	failing, gate, reached := failingOnce()
	rig := openFailedRun(t, core, policy, failing, fauxText("second answer"), fauxText("third answer"))
	first := submitInputWhenBusy(t, rig.root, "Analyze this report", "")
	<-reached
	second := submitInputWhenBusy(t, rig.root, "Email me the summary", durable.WhenBusyFollowUp)
	close(gate)
	expectUnanswered(t, settledOf(t, first), "model_error")
	return rig, second
}

func TestInputsQueuedBehindAFailedRun(t *testing.T) {
	// harness-failed-run-inbox.test.ts:68 "stay queued by default"
	t.Run("stay queued by default", func(t *testing.T) {
		eachEngine(t, func(t *testing.T, core bool) {
			rig, second := failWithQueuedInput(t, core, "")
			if err := rig.h.WaitForIdle(t.Context()); err != nil {
				t.Fatal(err)
			}
			status, err := second.Status(t.Context())
			if err != nil || status.Status != durable.SubmissionQueued {
				t.Fatalf("second %+v (%v), want queued", status, err)
			}
			// The next input runs the queue, as before.
			third := submitInputWhenBusy(t, rig.root, "Anything else?", "")
			if settled := settledOf(t, second); settled.Status != durable.SubmissionDone {
				t.Fatalf("second settled %+v, want done", settled)
			}
			if settled := settledOf(t, third); settled.Status != durable.SubmissionDone {
				t.Fatalf("third settled %+v, want done", settled)
			}
		})
	})

	// harness-failed-run-inbox.test.ts:80 "start the next run with continue"
	t.Run("start the next run with continue", func(t *testing.T) {
		eachEngine(t, func(t *testing.T, core bool) {
			rig, second := failWithQueuedInput(t, core, durable.AfterFailedRunContinue)
			settled := settledOf(t, second)
			if settled.Status != durable.SubmissionDone || settled.Answer == nil {
				t.Fatalf("second settled %+v, want done with an answer", settled)
			}
			answer, err := durable.Commit(t.Context(), rig.root, func(tx durable.Tx) (*durable.TypedEntry[durable.Never], error) {
				return durable.TxEntry(tx, durable.AssistantEntry, *settled.Answer)
			})
			if err != nil || answer == nil || len(answer.Model) == 0 {
				t.Fatalf("answer %+v (%v)", answer, err)
			}
			message, ok := answer.Model[0].(ai.AssistantMessage)
			if !ok || len(message.Content) == 0 {
				t.Fatalf("answer message %+v", answer.Model[0])
			}
			if text, _ := message.Content[0].(ai.TextContent); text.Text != "second answer" {
				t.Fatalf("answer text %q, want second answer", text.Text)
			}
		})
	})

	// harness-failed-run-inbox.test.ts:88 "place queued writes, and keep them with withdraw"
	t.Run("place queued writes, and keep them with withdraw", func(t *testing.T) {
		for _, policy := range []durable.AfterFailedRun{durable.AfterFailedRunContinue, durable.AfterFailedRunWithdraw} {
			eachEngine(t, func(t *testing.T, core bool) {
				failing, gate, reached := failingOnce()
				rig := openFailedRun(t, core, policy, failing, fauxText("second answer"))
				first := submitInputWhenBusy(t, rig.root, "first", "")
				<-reached
				note := ai.UserMessage{Content: ai.UserText("a note the model sees"), Timestamp: 1}
				write, err := rig.root.Submit(t.Context(), durable.SubmissionDraft{Type: durable.SubmissionTypeWrite, Entry: &durable.EntryDraft{Kind: "test.note", Model: []ai.Message{note}}})
				if err != nil {
					t.Fatal(err)
				}
				if status, err := write.Status(t.Context()); err != nil || status.Status != durable.SubmissionQueued {
					t.Fatalf("write %+v (%v), want queued", status, err)
				}
				close(gate)
				settledOf(t, first)
				if err := rig.h.WaitForIdle(t.Context()); err != nil {
					t.Fatal(err)
				}
				// `continue` places the write at the failed run's boundary; `withdraw` withdraws inputs only.
				want := durable.SubmissionQueued
				if policy == durable.AfterFailedRunContinue {
					want = durable.SubmissionDone
				}
				if status, err := write.Status(t.Context()); err != nil || status.Status != want {
					t.Fatalf("policy %s: write %+v (%v), want %s", policy, status, err, want)
				}
			})
		}
	})

	// harness-failed-run-inbox.test.ts:109 "settle unanswered with run_failed with withdraw"
	t.Run("settle unanswered with run_failed with withdraw", func(t *testing.T) {
		eachEngine(t, func(t *testing.T, core bool) {
			_, second := failWithQueuedInput(t, core, durable.AfterFailedRunWithdraw)
			expectUnanswered(t, settledOf(t, second), "run_failed")
		})
	})
}
