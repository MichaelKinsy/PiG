// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-FileCopyrightText: Copyright (c) 2025 Mario Zechner
// SPDX-License-Identifier: MIT

package coding

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/codingagent/compaction"
)

func suiteCompactionSeed(t *testing.T, s *Session) {
	t.Helper()
	s.services.SettingsManager().ApplyOverrides(icodingagent.Settings{Compaction: &icodingagent.CompactionSettingsJSON{KeepRecentTokens: new(1.)}})
	now := time.Now().UnixMilli()
	persistQueueMessage(t, s, queueUser("message to compact", now-1000))
	persistQueueMessage(t, s, agent.AgentMessage{Assistant: queueAssistant(s, "assistant response to compact", 100, 0, ai.StopReasonStop, now-500)})
	s.RefreshContext()
}
func suiteCompactions(t *testing.T, s *Session) []icodingagent.CompactionEntry {
	t.Helper()
	var result []icodingagent.CompactionEntry
	for _, entry := range s.inner.Entries() {
		if entry.Base.Type == "compaction" {
			var value icodingagent.CompactionEntry
			if err := json.Unmarshal(entry.Raw(), &value); err != nil {
				t.Fatal(err)
			}
			result = append(result, value)
		}
	}
	return result
}
func suiteSummaryStream(s *Session, summary string, onRequest func(string, []agent.AgentMessage, ai.StreamOptions)) func() int {
	calls := 0
	s.streamFn = func(_ context.Context, _ *ai.Model, system string, messages []agent.AgentMessage, options ai.StreamOptions) (string, *ai.Usage, error) {
		calls++
		if onRequest != nil {
			onRequest(system, messages, options)
		}
		return summary, &ai.Usage{Input: 10, TotalTokens: 10}, nil
	}
	return func() int { return calls }
}
func suiteSummaryExtension(summary string, usage *ai.Usage) extension.Extension {
	return extension.Extension{Path: "compaction-suite", Handlers: map[string][]extension.HandlerFn{"session_before_compact": {func(args ...any) (any, error) {
		prep := args[0].(extension.SessionBeforeCompactEvent).Preparation.(*compaction.CompactionPreparation)
		return extension.SessionBeforeCompactResult{Compaction: map[string]any{"summary": summary, "firstKeptEntryId": prep.FirstKeptEntryID, "tokensBefore": prep.TokensBefore, "usage": usage, "details": map[string]any{"source": "extension"}}}, nil
	}}}}
}
func suiteLastText(s *Session) string {
	if text := s.LastAssistantText(); text != nil {
		return *text
	}
	return ""
}

type suiteBearerProvider struct {
	t     *testing.T
	calls int
	auth  ai.ProviderAuth
}

func (p *suiteBearerProvider) ID() string            { return "faux" }
func (p *suiteBearerProvider) Close() error          { return nil }
func (p *suiteBearerProvider) Auth() ai.ProviderAuth { return p.auth }
func (p *suiteBearerProvider) Stream(_ context.Context, _ ai.TranscriptContext, options ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
	p.calls++
	if options.APIKey != "" || !reflect.DeepEqual(options.Headers, ai.ProviderHeadersFromStrings(map[string]string{"Authorization": "Bearer ambient-token"})) {
		p.t.Errorf("auth options=%+v", options)
	}
	message := fauxReply("summary with bearer auth", ai.StopReasonStop, 0)(nil)
	return newSessionTestStream(ai.StartEvent{Partial: message}, ai.DoneEvent{Reason: ai.StopReasonStop, Message: message}), nil
}

func TestAgentSessionCompactionSuiteUpstream(t *testing.T) {
	// .upstream/v0.87.1/packages/coding-agent/test/suite/agent-session-compaction.test.ts:136
	t.Run("manually compacts using an extension-provided summary", func(t *testing.T) {
		usage := &ai.Usage{Input: 10, Output: 20, CacheRead: 30, CacheWrite: 40, TotalTokens: 100, Cost: ai.UsageCost{Input: .1, Output: .2, CacheRead: .3, CacheWrite: .4, Total: 1}}
		h := newCompactionSuiteHarness(t, harnessOptions{settings: `{"compaction":{"keepRecentTokens":1}}`, extension: suiteSummaryExtension("summary from extension", usage)}, fauxReply("one", ai.StopReasonStop, 0), fauxReply("two", ai.StopReasonStop, 0))
		for _, prompt := range []string{"one", "two"} {
			if _, err := h.session.Send(t.Context(), prompt); err != nil {
				t.Fatal(err)
			}
		}
		before := h.session.GetSessionStats()
		result, err := h.session.CompactResult(t.Context(), "")
		if err != nil {
			t.Fatal(err)
		}
		entries := suiteCompactions(t, h.session)
		if result.Summary != "summary from extension" || !reflect.DeepEqual(result.Usage, usage) || result.EstimatedTokensAfter != estimateMessagesTokens(h.session.Messages()) || len(entries) != 1 || !reflect.DeepEqual(entries[0].Usage, usage) {
			t.Fatalf("result=%+v entries=%+v", result, entries)
		}
		after := h.session.GetSessionStats()
		if after.Tokens.Input != before.Tokens.Input+10 || after.Tokens.Output != before.Tokens.Output+20 || after.Tokens.CacheRead != before.Tokens.CacheRead+30 || after.Tokens.CacheWrite != before.Tokens.CacheWrite+40 || after.Cost != before.Cost+1 {
			t.Fatalf("before=%+v after=%+v", before, after)
		}
		messages := h.session.Messages()
		if len(messages) < 2 || messages[0].System == nil || messages[1].Custom["role"] != "compactionSummary" {
			t.Fatal(messages)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/suite/agent-session-compaction.test.ts:238
	t.Run("allows a queued prompt to start when manual compaction ends", func(t *testing.T) {
		h := newCompactionSuiteHarness(t, harnessOptions{settings: `{"compaction":{"keepRecentTokens":1}}`, extension: suiteSummaryExtension("manual compacted", nil)}, fauxReply("queued response", ai.StopReasonStop, 0))
		suiteCompactionSeed(t, h.session)
		done := make(chan error, 1)
		started := make(chan struct{})
		idleAtEnd := false
		unsubscribe := h.session.Subscribe(func(event agent.AgentEvent) {
			if end, ok := event.(agent.CompactionEndEvent); ok && end.Reason == "manual" && end.Summary != "" {
				idleAtEnd = !h.session.IsCompacting()
				if !idleAtEnd {
					t.Error("manual compaction still active")
				}
				close(started)
				go func() { _, err := h.session.Send(t.Context(), "queued after compaction"); done <- err }()
			}
		})
		defer unsubscribe()
		if _, err := h.session.CompactResult(t.Context(), ""); err != nil {
			t.Fatal(err)
		}
		beforeReturn := false
		select {
		case <-started:
			beforeReturn = true
		default:
			t.Error("compaction_end did not start the queued prompt before compact returned")
		}
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		raw, err := json.Marshal(h.session.Messages())
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(raw), "queued after compaction") || suiteLastText(h.session) != "queued response" {
			t.Fatal(string(raw))
		}
		fmt.Printf("COMPACTION_BOUNDARY queue beforeReturn=%v idle=%v text=%q\n", beforeReturn, idleAtEnd, suiteLastText(h.session))
	})
	// .upstream/v0.87.1/packages/coding-agent/test/suite/agent-session-compaction.test.ts:274
	t.Run("throws when compacting without a model", func(t *testing.T) {
		h := newRecoveryHarness(t, harnessOptions{})
		h.session.agent.SetModel(nil)
		root, rootErr := filepath.Abs("..")
		if rootErr != nil {
			t.Fatal(rootErr)
		}
		t.Setenv("PIG_HOME", root)
		_, err := h.session.CompactResult(t.Context(), "")
		if err == nil || !strings.Contains(err.Error(), "No model selected") {
			t.Fatalf("error=%v", err)
		}
		fmt.Printf("COMPACTION_BOUNDARY no-model %q\n", err.Error())
	})
	// .upstream/v0.99.1/packages/coding-agent/test/suite/agent-session-compaction.test.ts:282 (changed: the session is seeded first, because auth is resolved only when Pi summarizes itself, after checking there is something to compact)
	t.Run("throws when compacting without configured auth", func(t *testing.T) {
		t.Setenv("FAUX_API_KEY", "")
		h := newRecoveryHarness(t, harnessOptions{withConfiguredAuth: new(false)})
		suiteCompactionSeed(t, h.session)
		model, err := BuildModel("faux/faux-1", h.session.services)
		if err != nil {
			t.Fatal(err)
		}
		model.ProviderMeta.ProviderID = "faux"
		model.ID = "faux-1"
		h.session.agent.SetModel(model)
		root, rootErr := filepath.Abs("..")
		if rootErr != nil {
			t.Fatal(rootErr)
		}
		t.Setenv("PIG_HOME", root)
		_, err = h.session.CompactResult(t.Context(), "")
		if err == nil || !strings.Contains(err.Error(), "No API key found for faux.") {
			t.Fatalf("error=%v", err)
		}
		fmt.Printf("COMPACTION_BOUNDARY no-auth %q\n", err.Error())
	})
	// .upstream/v0.87.1/packages/coding-agent/test/suite/agent-session-compaction.test.ts:289
	t.Run("manually compacts with a custom streamFn when registry auth is absent", func(t *testing.T) {
		h := newRecoveryHarness(t, harnessOptions{withConfiguredAuth: new(false)})
		suiteCompactionSeed(t, h.session)
		calls := suiteSummaryStream(h.session, "summary from custom stream", nil)
		result, err := h.session.CompactResult(t.Context(), "")
		if err != nil || !strings.Contains(result.Summary, "summary from custom stream") || calls() != 1 {
			t.Fatalf("result=%+v error=%v calls=%d", result, err, calls())
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/suite/agent-session-compaction.test.ts:301
	t.Run("manually compacts with provider-resolved bearer auth", func(t *testing.T) {
		h := newRecoveryHarness(t, harnessOptions{withConfiguredAuth: new(false)})
		resolved := 0
		provider := &suiteBearerProvider{t: t, auth: ai.ProviderAuth{APIKey: &ai.APIKeyAuth{Name: "Faux bearer token", Resolve: func(context.Context, ai.APIKeyAuthInput) (*ai.AuthResult, error) {
			resolved++
			return &ai.AuthResult{Auth: ai.ModelAuth{Headers: ai.ProviderHeadersFromStrings(map[string]string{"Authorization": "Bearer ambient-token"})}, Source: "ambient bearer token"}, nil
		}}}}
		installCompactionModel(h.session, provider, 200000, 8192)
		suiteCompactionSeed(t, h.session)
		result, err := h.session.CompactResult(t.Context(), "")
		if err != nil || !strings.Contains(result.Summary, "summary with bearer auth") || provider.calls != 1 || resolved != 1 {
			t.Fatalf("result=%+v error=%v calls=%d auth=%d", result, err, provider.calls, resolved)
		}
		fmt.Printf("COMPACTION_BOUNDARY bearer requests=%d resolved=%d\n", provider.calls, resolved)
	})
	// .upstream/v0.87.1/packages/coding-agent/test/suite/agent-session-compaction.test.ts:335
	t.Run("uses the standalone compaction request context", func(t *testing.T) {
		h := newRecoveryHarness(t, harnessOptions{settings: `{"compaction":{"keepRecentTokens":1}}`})
		suiteCompactionSeed(t, h.session)
		h.session.agent.SetSessionID("active-routing-session")
		h.session.agent.SetTransport("websocket")
		transforms := 0
		h.session.agent.SetTransformContextWithContext(func(_ context.Context, messages []agent.AgentMessage) ([]agent.AgentMessage, error) {
			transforms++
			return messages, nil
		})
		requests := 0
		suiteSummaryStream(h.session, "standalone summary", func(system string, messages []agent.AgentMessage, options ai.StreamOptions) {
			requests++
			if system == h.session.agent.SystemPrompt() || options.CacheRetention != "none" || options.SessionID == "active-routing-session" || options.Transport != "" || len(ai.GetCurrentTools(agent.ConvertToLLM(messages, h.session.Model()))) != 0 {
				t.Fatalf("system=%q options=%+v", system, options)
			}
			raw, err := json.Marshal(messages)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(raw), `# Conversation\n[User]: message to compact`) {
				t.Fatal(string(raw))
			}
		})
		if _, err := h.session.CompactResult(t.Context(), ""); err != nil {
			t.Fatal(err)
		}
		if transforms != 0 || requests != 1 {
			t.Fatalf("transforms=%d requests=%d", transforms, requests)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/suite/agent-session-compaction.test.ts:364
	t.Run("persists usage from pi-generated manual compaction", func(t *testing.T) {
		h := newRecoveryHarness(t, harnessOptions{})
		suiteCompactionSeed(t, h.session)
		suiteSummaryStream(h.session, "summary from custom stream", nil)
		result, err := h.session.CompactResult(t.Context(), "")
		if err != nil {
			t.Fatal(err)
		}
		entries := suiteCompactions(t, h.session)
		want := &ai.Usage{Input: 10, TotalTokens: 10}
		if !reflect.DeepEqual(result.Usage, want) || len(entries) != 1 || !reflect.DeepEqual(entries[0].Usage, want) {
			t.Fatalf("result=%+v entries=%+v", result, entries)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/suite/agent-session-compaction.test.ts:380
	t.Run("auto-compacts with a custom streamFn when registry auth is absent", func(t *testing.T) {
		h := newRecoveryHarness(t, harnessOptions{})
		suiteCompactionSeed(t, h.session)
		calls := suiteSummaryStream(h.session, "auto summary from custom stream", nil)
		ends := make(chan agent.CompactionEndEvent, 1)
		unsubscribe := h.session.Subscribe(func(event agent.AgentEvent) {
			if end, ok := event.(agent.CompactionEndEvent); ok {
				ends <- end
			}
		})
		defer unsubscribe()
		if _, err := h.session.runAutoCompaction(t.Context(), "threshold", false); err != nil {
			t.Fatal(err)
		}
		if len(suiteCompactions(t, h.session)) != 1 || calls() != 1 {
			t.Fatal("missing compaction or wrong stream calls")
		}
		end := <-ends
		if end.EstimatedTokensAfter <= 0 {
			t.Fatal(end)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/suite/agent-session-compaction.test.ts:396
	t.Run("notifies extensions when auto-compaction fails", func(t *testing.T) {
		h := newRecoveryHarness(t, harnessOptions{})
		suiteCompactionSeed(t, h.session)
		var failed []extension.SessionCompactFailedEvent
		withTreeHandlers(h.session, t, map[string][]extension.HandlerFn{"session_compact_failed": {func(args ...any) (any, error) {
			failed = append(failed, args[0].(extension.SessionCompactFailedEvent))
			return nil, nil
		}}})
		h.session.streamFn = func(context.Context, *ai.Model, string, []agent.AgentMessage, ai.StreamOptions) (string, *ai.Usage, error) {
			return "", nil, errors.New("summary generator blew up")
		}
		var end agent.CompactionEndEvent
		unsubscribe := h.session.Subscribe(func(event agent.AgentEvent) {
			if value, ok := event.(agent.CompactionEndEvent); ok {
				end = value
			}
		})
		defer unsubscribe()
		compacted, err := h.session.runAutoCompaction(t.Context(), "threshold", false)
		if err != nil || compacted {
			t.Fatalf("compacted=%v err=%v", compacted, err)
		}
		if end.Reason != "threshold" || end.Aborted || end.WillRetry || end.ErrorMessage != "Auto-compaction failed: summary generator blew up" || len(failed) != 1 || failed[0].Reason != "threshold" || failed[0].Aborted || failed[0].WillRetry || failed[0].FromExtension || failed[0].ErrorMessage != end.ErrorMessage {
			t.Fatalf("end=%+v failed=%+v", end, failed)
		}
		fmt.Printf("COMPACTION_BOUNDARY exception %q\n", end.ErrorMessage)
	})
}

// agent-session.ts:2633-2645 (_runDefaultCompaction: "Resolve the request only when Pi summarizes itself: routing may call models or fail") and :2690-2700 (compact() prepares before it resolves auth). Go regression guards for the source change that the upstream suite covers only through the seeded auth case above.
func TestManualCompactionResolvesAuthOnlyWhenPiSummarizesItself(t *testing.T) {
	t.Run("an empty session reports there is nothing to compact instead of missing auth", func(t *testing.T) {
		t.Setenv("FAUX_API_KEY", "")
		h := newRecoveryHarness(t, harnessOptions{withConfiguredAuth: new(false)})
		model, err := BuildModel("faux/faux-1", h.session.services)
		if err != nil {
			t.Fatal(err)
		}
		model.ProviderMeta.ProviderID = "faux"
		model.ID = "faux-1"
		h.session.agent.SetModel(model)
		if _, err := h.session.CompactResult(t.Context(), ""); err == nil || !strings.Contains(err.Error(), "Nothing to compact (session too small)") {
			t.Fatalf("error=%v", err)
		}
	})
	t.Run("an extension-supplied summary needs no auth", func(t *testing.T) {
		t.Setenv("FAUX_API_KEY", "")
		h := newRecoveryHarness(t, harnessOptions{withConfiguredAuth: new(false), extension: summaryFromPreparation("extension summary")})
		model, err := BuildModel("faux/faux-1", h.session.services)
		if err != nil {
			t.Fatal(err)
		}
		model.ProviderMeta.ProviderID = "faux"
		model.ID = "faux-1"
		h.session.agent.SetModel(model)
		suiteCompactionSeed(t, h.session)
		result, err := h.session.CompactResult(t.Context(), "")
		if err != nil || result.Summary != "extension summary" {
			t.Fatalf("result=%+v error=%v", result, err)
		}
	})
}
