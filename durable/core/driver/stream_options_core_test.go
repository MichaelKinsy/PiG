// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package driver_test

import (
	"sync"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/harness"
)

// harness-generation.test.ts "forwards stream options and the thinking level" (Pi c23b9ec1: websocketConnectTimeoutMs and
// thinkingBudgets join timeoutMs and headers) over the core and the reference: the stream settings and the thinking level
// are read at each preparation and reach the model call's options, and a setting that is gone is absent from the next one.
func TestCoreStreamSettingsReachTheModelCall(t *testing.T) {
	eachEngine(t, func(t *testing.T, core bool) {
		var mu sync.Mutex
		var seen []ai.StreamOptions
		record := ai.FauxFactoryStep(func(_ ai.TranscriptContext, options ai.StreamOptions, _ *ai.FauxProviderState, _ *ai.Model) (ai.AssistantMessage, error) {
			mu.Lock()
			seen = append(seen, options)
			mu.Unlock()
			return ai.FauxAssistantMessage(ai.FauxContentBlocks{ai.FauxText("ok")}, ai.FauxAssistantMessageOptions{}), nil
		})
		faux := ai.NewFauxProvider(ai.FauxConfig{})
		faux.SetResponses([]ai.FauxResponseStep{record, record})
		models := ai.CreateModels()
		models.SetProvider(faux.Provider())
		var settings atomic.Pointer[harness.HarnessSettings]
		settings.Store(&harness.HarnessSettings{Stream: &durable.ConversationStreamOptions{
			TimeoutMs: new(1234), Headers: map[string]string{"x-test": "1"}, WebsocketConnectTimeoutMs: new(5678),
			ThinkingBudgets: ai.NewThinkingBudgets(new(128), new(512), new(2048), new(8192)),
		}})
		_, root := openHostInputs(t, core, harness.HarnessOptions{Models: models, Settings: settings.Load})
		if err := root.Configure(t.Context(), harness.AgentChange{ThinkingLevel: harness.SetTo(ai.ModelThinkingLevel("high"))}); err != nil {
			t.Fatal(err)
		}
		hostInputsTurn(t, root, "one")
		// Both are read at the next preparation: the thinking level from pi.agent, the stream options live from settings.
		if err := root.Configure(t.Context(), harness.AgentChange{ThinkingLevel: harness.Cleared[ai.ModelThinkingLevel]()}); err != nil {
			t.Fatal(err)
		}
		settings.Store(&harness.HarnessSettings{Stream: &durable.ConversationStreamOptions{TimeoutMs: new(99)}})
		hostInputsTurn(t, root, "two")
		mu.Lock()
		defer mu.Unlock()
		if len(seen) != 2 {
			t.Fatalf("%d model calls, want 2", len(seen))
		}
		first, second := seen[0], seen[1]
		if first.TimeoutMs == nil || *first.TimeoutMs != 1234 || first.Headers["x-test"] == nil || *first.Headers["x-test"] != "1" || first.Thinking != "high" {
			t.Fatalf("first options = %+v, want timeoutMs 1234, header x-test 1, reasoning high", first)
		}
		if first.WebSocketConnectTimeoutMs == nil || *first.WebSocketConnectTimeoutMs != 5678 {
			t.Fatalf("first websocketConnectTimeoutMs = %v, want 5678", first.WebSocketConnectTimeoutMs)
		}
		if b := first.ThinkingBudgets; b == nil || b.Minimal != 128 || b.Low != 512 || b.Medium != 2048 || b.High != 8192 {
			t.Fatalf("first thinkingBudgets = %+v, want 128/512/2048/8192", b)
		}
		if second.Thinking != "" {
			t.Fatalf("second reasoning = %q, want undefined", second.Thinking)
		}
		if second.TimeoutMs == nil || *second.TimeoutMs != 99 {
			t.Fatalf("second timeoutMs = %v, want 99", second.TimeoutMs)
		}
		if second.Headers != nil {
			t.Fatalf("second headers = %v, want undefined", second.Headers)
		}
		if second.WebSocketConnectTimeoutMs != nil || second.ThinkingBudgets != nil {
			t.Fatalf("second websocketConnectTimeoutMs = %v, thinkingBudgets = %+v, want undefined", second.WebSocketConnectTimeoutMs, second.ThinkingBudgets)
		}
	})
}
