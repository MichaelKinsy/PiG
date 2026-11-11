// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package driver_test

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/core/driver"
	"github.com/MichaelKinsy/PiG/durable/harness"
)

// harness-view-run.test.ts:408 over the core: an abort converts the streamed partial into one aborted answer entry, appended in the
// commit that ends the run, which has no second assistant message and says why it ended.
func TestCoreAStreamedMessageEndsOnceWithTheConvertedEntryOnAbort(t *testing.T) {
	one := 1
	faux := ai.NewFauxProvider(ai.FauxConfig{TokensPerSecond: 100, TokenSize: &ai.FauxTokenSize{Min: &one, Max: &one}})
	models := ai.CreateModels()
	models.SetProvider(faux.Provider())
	faux.SetResponses([]ai.FauxResponseStep{ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText(strings.Repeat("x", 400))}, StopReason: "stop"})})
	h, err := driver.OpenHarness(t.Context(), filepath.Join(t.TempDir(), "store.db"), harness.HarnessOptions{Models: models, Registry: harness.CreateRegistry()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close(context.Background()) })
	root, err := h.Root(t.Context(), &harness.RootOptions{Agent: &harness.AgentChange{Model: harness.SetTo(durable.ModelRef{Provider: "faux", ModelId: "faux-1"})}})
	if err != nil {
		t.Fatal(err)
	}
	h.Resume()
	history := viewHistory(t, h, root.Id())
	sub, err := root.Submit(t.Context(), durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText("hi")})
	if err != nil {
		t.Fatal(err)
	}
	pollUntil(t, "a streamed partial", func() bool {
		return slices.ContainsFunc(history.Live(), func(state harness.LiveState) bool { return state.Generation != nil && state.Generation.Message != nil })
	})
	live, err := durable.Snapshot(t.Context(), h, harness.LiveDoc, root.Id())
	if err != nil || live == nil || live.Run == nil {
		t.Fatalf("live state %+v, %v, want a run", live, err)
	}
	// The abort answers once the generation's abort invocation ran, which needs the provider's aborted stream to reach the core.
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	if _, err := h.AbortTask(ctx, live.Run.TaskId); err != nil {
		t.Fatal(err)
	}
	if _, err := sub.Wait(ctx); err != nil {
		t.Fatal(err)
	}
	if err := h.WaitForIdle(ctx); err != nil {
		t.Fatal(err)
	}
	// The core delivers on its own goroutine, so wait for the view that ends the run.
	pollUntil(t, "the run's failure", func() bool {
		states := history.Live()
		return states[len(states)-1].RunFailure != nil
	})
	history.Detach()
	states, views := history.Live(), history.All()
	ended := liveIndexAfter(states, 0, func(state harness.LiveState) bool { return state.Run == nil })
	if ended < 1 || states[ended-1].Generation == nil || states[ended-1].Generation.Message == nil {
		t.Fatalf("run ends at %d without a partial before it", ended)
	}
	appended := views[ended].Entries[len(views[ended-1].Entries):]
	if len(appended) != 1 || appended[0].Kind != "pi.assistant" {
		t.Fatalf("the ending commit appends %+v, want one pi.assistant", appended)
	}
	if message, ok := appended[0].Model[0].(ai.AssistantMessage); !ok || message.StopReason != "aborted" {
		t.Fatalf("the appended message = %+v, want an aborted assistant message", appended[0].Model[0])
	}
	if failure := states[ended].RunFailure; failure == nil || failure.Reason != harness.RunAborted {
		t.Fatalf("runFailure = %+v, want aborted", failure)
	}
}
