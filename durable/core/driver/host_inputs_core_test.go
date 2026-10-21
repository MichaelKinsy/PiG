// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package driver_test

import (
	"context"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/core/driver"
	"github.com/MichaelKinsy/PiG/durable/harness"
	"github.com/MichaelKinsy/PiG/durable/storage"
)

// answering is a Models whose only provider is a faux one answering every request with text.
func answering(text string) *ai.Models {
	faux := ai.NewFauxProvider(ai.FauxConfig{})
	steps := make([]ai.FauxResponseStep, 10)
	for i := range steps {
		steps[i] = ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText(text)}, StopReason: "stop"})
	}
	faux.SetResponses(steps)
	models := ai.CreateModels()
	models.SetProvider(faux.Provider())
	return models
}

func openHostInputs(t *testing.T, core bool, options harness.HarnessOptions) (harness.Harness, harness.Conversation) {
	t.Helper()
	if options.Registry == nil {
		options.Registry = harness.CreateRegistry()
	}
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
	return h, root
}

// hostInputsTurn runs one turn and returns the text of its last entry.
func hostInputsTurn(t *testing.T, root harness.Conversation, content string) string {
	t.Helper()
	sub, err := root.Submit(t.Context(), durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText(content)})
	if err != nil {
		t.Fatal(err)
	}
	settled, err := sub.Wait(t.Context())
	if err != nil || settled.Status != durable.SubmissionDone {
		t.Fatalf("turn %q: %+v, %v", content, settled, err)
	}
	page, err := root.Entries(t.Context(), durable.EntryQuery{}, 100, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Entries scan newest first by default.
	last := page.Items[0]
	if message, ok := last.Model[0].(ai.AssistantMessage); ok {
		for _, block := range message.Content {
			if text, ok := block.(ai.TextContent); ok {
				return text.Text
			}
		}
	}
	return ""
}

// harness-host-inputs.test.ts (Pi 7ec1f648) over the core and the reference: HarnessOptions.models and settings as
// functions, read at each use.
func TestCoreHostInputsAreReadAtEachUse(t *testing.T) {
	t.Run("generation uses the current return value of a models function at each request", func(t *testing.T) {
		eachEngine(t, func(t *testing.T, core bool) {
			current := answering("first")
			_, root := openHostInputs(t, core, harness.HarnessOptions{ModelsFunc: func() durable.Models { return current }})
			if got := hostInputsTurn(t, root, "one"); got != "first" {
				t.Fatalf("first turn %q", got)
			}
			current = answering("second")
			if got := hostInputsTurn(t, root, "two"); got != "second" {
				t.Fatalf("second turn %q", got)
			}
		})
	})

	t.Run("still accepts a plain Models object", func(t *testing.T) {
		eachEngine(t, func(t *testing.T, core bool) {
			_, root := openHostInputs(t, core, harness.HarnessOptions{Models: answering("plain")})
			if got := hostInputsTurn(t, root, "one"); got != "plain" {
				t.Fatalf("turn %q", got)
			}
		})
	})

	t.Run("tools read the current models at each access of api.models", func(t *testing.T) {
		eachEngine(t, func(t *testing.T, core bool) {
			faux := ai.NewFauxProvider(ai.FauxConfig{})
			faux.SetResponses([]ai.FauxResponseStep{
				ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxToolCall("probe", map[string]any{}, &ai.FauxToolCallOptions{ID: "call-1"})}, StopReason: "toolUse"}),
				ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("done")}, StopReason: "stop"}),
			})
			first, second := ai.CreateModels(), ai.CreateModels()
			first.SetProvider(faux.Provider())
			second.SetProvider(faux.Provider())
			// Marks the second Models: tools see views of the host's Models that add the Harness identity.
			second.SetProvider(ai.NewFauxProvider(ai.FauxConfig{ProviderID: "second-only"}).Provider())
			current := first
			var seen []durable.Models
			registry := harness.CreateRegistry()
			probe := &durable.ToolRegistration{
				ToolSchema: ai.ToolSchema{Name: "probe", Description: "Records api.models before and after the host swaps it", Parameters: map[string]any{"type": "object", "properties": map[string]any{}}},
				Execute: func(_ context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
					seen = append(seen, api.Models())
					current = second
					seen = append(seen, api.Models())
					return durable.ToolExecutionResult{Output: []ai.ToolResultMessageContent{}}, nil
				},
			}
			if err := registry.Install(&durable.Extension{Name: "probe", Tools: []*durable.ToolRegistration{probe}}); err != nil {
				t.Fatal(err)
			}
			_, root := openHostInputs(t, core, harness.HarnessOptions{ModelsFunc: func() durable.Models { return current }, Registry: registry})
			if got := hostInputsTurn(t, root, "go"); got != "done" {
				t.Fatalf("turn %q", got)
			}
			listed := func(models durable.Models) int { return len(models.(ai.ModelsLister).GetModels("second-only")) }
			if len(seen) != 2 || listed(seen[0]) != 0 || listed(seen[1]) == 0 {
				t.Fatalf("api.models saw %v, want the first models, then the second", seen)
			}
		})
	})

	t.Run("generation reads the current return value of a settings function", func(t *testing.T) {
		eachEngine(t, func(t *testing.T, core bool) {
			var timeouts []int
			faux := ai.NewFauxProvider(ai.FauxConfig{})
			reply := ai.FauxFactoryStep(func(_ ai.TranscriptContext, options ai.StreamOptions, _ *ai.FauxProviderState, _ *ai.Model) (ai.AssistantMessage, error) {
				timeout := -1
				if options.TimeoutMs != nil {
					timeout = *options.TimeoutMs
				}
				timeouts = append(timeouts, timeout)
				return ai.FauxAssistantMessage(ai.FauxContentBlocks{ai.FauxText("ok")}, ai.FauxAssistantMessageOptions{}), nil
			})
			faux.SetResponses([]ai.FauxResponseStep{reply, reply})
			models := ai.CreateModels()
			models.SetProvider(faux.Provider())
			// The Harness calls the settings function from its own goroutines, some after a turn has settled (the scheduler's pass
			// that follows it), so the host publishes a change atomically: a bare variable write would race those reads.
			var settings atomic.Pointer[harness.HarnessSettings]
			settings.Store(&harness.HarnessSettings{Stream: &durable.ConversationStreamOptions{TimeoutMs: new(1000)}})
			_, root := openHostInputs(t, core, harness.HarnessOptions{Models: models, Settings: settings.Load})
			hostInputsTurn(t, root, "one")
			settings.Store(&harness.HarnessSettings{Stream: &durable.ConversationStreamOptions{TimeoutMs: new(2000)}})
			hostInputsTurn(t, root, "two")
			if len(timeouts) != 2 || timeouts[0] != 1000 || timeouts[1] != 2000 {
				t.Fatalf("timeouts %v, want [1000 2000]", timeouts)
			}
		})
	})
}
