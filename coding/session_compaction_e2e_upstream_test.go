// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-FileCopyrightText: Copyright (c) 2025 Mario Zechner
// SPDX-License-Identifier: MIT

package coding

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

// The upstream file uses a live model, but every assertion concerns Session state,
// persistence or events, not answer quality. The lane lead approves a faux provider
// for these deterministic assertions. No case needs a live-only disposition.
func TestAgentSessionCompactionE2EUpstream(t *testing.T) {
	cases := []struct {
		name       string
		prompts    []string
		after      string
		memory     bool
		events     bool
		checkpoint bool
	}{
		// .upstream/v0.87.1/packages/coding-agent/test/agent-session-compaction.test.ts:84-107
		// The expectation at :107 disagrees with pinned Pi: .upstream/v0.87.1/packages/coding-agent/src/core/session-manager.ts:1268-1281 saves the system checkpoint and :461-463 projects it before the summary.
		// Lead-approved oracle rule; reference-original-assertion-red.log records Pi's failing original assertion. This is not a divergence.
		{"should trigger manual compaction via compact()", []string{"What is 2+2? Reply with just the number.", "What is 3+3? Reply with just the number."}, "", false, false, true},
		// .upstream/v0.87.1/packages/coding-agent/test/agent-session-compaction.test.ts:110
		{"should maintain valid session state after compaction", []string{"What is the capital of France? One word answer.", "What is the capital of Germany? One word answer."}, "What is the capital of Italy? One word answer.", false, false, false},
		// .upstream/v0.87.1/packages/coding-agent/test/agent-session-compaction.test.ts:135
		{"should persist compaction to session file", []string{"Say hello", "Say goodbye"}, "", false, false, false},
		// .upstream/v0.87.1/packages/coding-agent/test/agent-session-compaction.test.ts:163
		{"should work with no-session mode in-memory only", []string{"What is 2+2? Reply with just the number.", "What is 3+3? Reply with just the number."}, "", true, false, false},
		// .upstream/v0.87.1/packages/coding-agent/test/agent-session-compaction.test.ts:185
		{"should emit compaction events during manual compaction", []string{"Say hello"}, "", false, true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			provider := &scriptedProvider{}
			// Each stop response terminates its call. Compaction can use separate history
			// and turn-prefix calls, so all request slots return complete nonempty text.
			for range 8 {
				provider.responses = append(provider.responses, fauxReply("complete summary", ai.StopReasonStop, 0))
			}
			if tc.checkpoint {
				provider.responses[0] = fauxReply("4", ai.StopReasonStop, 0)
				provider.responses[1] = fauxReply("6", ai.StopReasonStop, 0)
			}
			model := fakeModelWithProvider(provider)
			model.Capabilities.ContextWindow = 200000
			model.Capabilities.MaxOutputTokens = 8192
			s, err := NewSession(newTestServicesSmallKeep(t), SessionOptions{Model: model, NoSession: tc.memory, SystemPrompt: "You are a helpful assistant. Be concise."})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := s.Close(); err != nil {
					t.Error(err)
				}
			})
			var events []agent.AgentEvent
			send := func(prompt string) {
				if _, err := s.Send(t.Context(), prompt); err != nil {
					t.Fatal(err)
				}
				events = append(events, drainEvents(t, s)...)
			}
			for _, prompt := range tc.prompts {
				send(prompt)
			}
			result, err := s.CompactResult(t.Context(), "")
			if err != nil {
				t.Fatal(err)
			}
			events = append(events, drainEvents(t, s)...)
			if result.Summary == "" || result.TokensBefore <= 0 {
				t.Fatalf("result=%+v", result)
			}
			messages := s.Messages()
			if len(messages) == 0 {
				t.Fatalf("messages=%+v", messages)
			}
			if tc.checkpoint {
				if len(messages) < 2 || messages[0].Role() != "system" || messages[1].Role() != agent.RoleCompactionSummary {
					t.Fatalf("expected system checkpoint followed by compactionSummary: %+v", messages)
				}
				roles := make([]string, len(messages))
				for i, message := range messages {
					roles[i] = message.Role()
				}
				trace, err := json.Marshal(struct {
					Roles          []string `json:"roles"`
					SummaryPresent bool     `json:"summaryPresent"`
					TokensPositive bool     `json:"tokensPositive"`
				}{roles, result.Summary != "", result.TokensBefore > 0})
				if err != nil {
					t.Fatal(err)
				}
				fmt.Printf("COMPACTION_E2E %s\n", trace)
			}
			entries := s.inner.Entries()
			count := 0
			for _, entry := range entries {
				if entry.Base.Type != "compaction" {
					continue
				}
				count++
				var comp icodingagent.CompactionEntry
				if err := json.Unmarshal(entry.Raw(), &comp); err != nil {
					t.Fatal(err)
				}
				if comp.Summary == "" || comp.FirstKeptEntryID == "" || comp.TokensBefore <= 0 {
					t.Fatal(comp)
				}
				if tc.checkpoint && len(comp.SystemMessage) == 0 {
					t.Fatal("compaction omitted its system checkpoint")
				}
			}
			if count != 1 {
				t.Fatalf("compactions=%d, want one manual compaction", count)
			}
			if tc.memory {
				if s.Path() != "" {
					t.Fatal("memory session persisted")
				}
			} else {
				loaded, err := icodingagent.NewSessionManagerWithDir(s.CWD(), t.TempDir()).Load(s.Path())
				if err != nil {
					t.Fatal(err)
				}
				found := false
				for _, entry := range loaded.Entries() {
					found = found || entry.Base.Type == "compaction"
				}
				if !found {
					t.Fatal("compaction not persisted")
				}
			}
			if tc.after != "" {
				send(tc.after)
				messages = s.Messages()
				assistants := 0
				for _, m := range messages {
					if m.Assistant != nil {
						assistants++
					}
				}
				if len(messages) == 0 || assistants == 0 {
					t.Fatal("session unusable after compaction")
				}
			}
			if tc.events {
				var compactions []agent.AgentEvent
				messageEnds := 0
				for _, event := range events {
					switch event.(type) {
					case agent.CompactionStartEvent, agent.CompactionEndEvent:
						compactions = append(compactions, event)
					case agent.MessageEndEvent:
						messageEnds++
					}
				}
				if len(compactions) != 2 {
					t.Fatalf("compaction events=%v", compactions)
				}
				start, ok := compactions[0].(agent.CompactionStartEvent)
				if !ok || start.Reason != "manual" {
					t.Fatal(start)
				}
				end, ok := compactions[1].(agent.CompactionEndEvent)
				if !ok || end.Reason != "manual" || end.Aborted || end.WillRetry {
					t.Fatal(end)
				}
				if messageEnds == 0 {
					t.Fatal("no message_end")
				}
			}
		})
	}
}
