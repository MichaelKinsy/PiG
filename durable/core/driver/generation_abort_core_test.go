// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package driver_test

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/core/driver"
	"github.com/MichaelKinsy/PiG/durable/harness"
)

// harness-events.test.ts:599 over the core: a streamed assistant message starts at its first committed partial, and an abort ends
// it once with the converted entry; the run never starts a second assistant message.
func TestCoreAStreamedMessageStartsAtItsFirstPartialAndEndsOnceWithTheConvertedEntryOnAbort(t *testing.T) {
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
	var mu sync.Mutex
	var events []harness.AgentEvent
	stream, err := harness.WatchEvents(t.Context(), h, root.Id())
	if err != nil {
		t.Fatal(err)
	}
	stream.Start(func(_ context.Context, batch []harness.AgentEvent) error {
		mu.Lock()
		defer mu.Unlock()
		events = append(events, batch...)
		return nil
	})
	sub, err := root.Submit(t.Context(), durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText("hi")})
	if err != nil {
		t.Fatal(err)
	}
	assistantStarts := func() int {
		mu.Lock()
		defer mu.Unlock()
		return assistantStartsOf(events)
	}
	for deadline := time.Now().Add(20 * time.Second); assistantStarts() == 0; time.Sleep(5 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("no assistant message_start")
		}
	}
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
	assistantEnds := func() int {
		mu.Lock()
		defer mu.Unlock()
		ends := 0
		for _, event := range events {
			if end, ok := event.(harness.MessageEndEvent); ok && end.Entry.Kind == "pi.assistant" {
				ends++
			}
		}
		return ends
	}
	// Pi's drained() lets the deliveries of the commits already published run before it reads the events. The core delivers on
	// its own goroutine, and Stop ends the frames still queued, so wait for the abort's message_end to arrive before stopping.
	waitFor(t, "the assistant message_end", func() bool { return assistantEnds() > 0 })
	if _, err := stream.Stop(); err != nil {
		t.Fatal(err)
	}
	if starts, ends := assistantStarts(), assistantEnds(); starts != 1 || ends != 1 {
		t.Fatalf("assistant starts %d, ends %d, want one of each", starts, ends)
	}
}

func assistantStartsOf(events []harness.AgentEvent) int {
	count := 0
	for _, event := range events {
		if start, ok := event.(harness.MessageStartEvent); ok {
			if _, isAssistant := start.Message.(ai.AssistantMessage); isAssistant {
				count++
			}
		}
	}
	return count
}
