// SPDX-License-Identifier: MIT

package coding

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

// pig additive (D238): compaction.summaryModel writes the compaction summary; any failure but a cancel falls back to the session model.
type summaryModelHarness struct {
	session *Session
	// A compaction can summarize the history and a split turn separately, so the tests compare the counts with zero. sessionCalls and summaryCalls count the summary requests that reached the session model and the configured model.
	sessionCalls, summaryCalls func() int
}

// newSummaryModelHarness builds a session whose model answers "session summary" and registers "other/summary" with the given stream.
// register is false when the configured model must stay unknown to the registry.
func newSummaryModelHarness(t *testing.T, setting string, register bool, summaryStream func(context.Context, *ai.Model) (*ai.AssistantMessageEventStream, error)) *summaryModelHarness {
	t.Helper()
	services := newTestServices(t)
	if err := services.SettingsManager().UpdateGlobal(func(settings *icodingagent.Settings) {
		settings.Compaction = &icodingagent.CompactionSettingsJSON{KeepRecentTokens: new(1.)}
		if setting != "" {
			settings.Compaction.SummaryModel = &setting
		}
	}); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var sessionCount, summaryCount int
	sessionProvider := &runtimeTestProvider{id: "sess", stream: func(context.Context, ai.TranscriptContext, ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
		mu.Lock()
		sessionCount++
		mu.Unlock()
		stream, _ := runtimeTestTextStream("sess", "fake-1", "session summary")
		return stream, nil
	}}
	if register {
		summary := nativeCompatModel("summary", "other", "https://other.invalid")
		err := services.ModelRuntime().RegisterProvider("other", ProviderConfigInput{
			API: ai.APIOpenAICompletions, BaseURL: "https://other.invalid", APIKey: "key", Models: []ai.AnyModel{summary},
			StreamSimple: func(ctx context.Context, model *ai.Model, _ ai.TranscriptContext, _ ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
				mu.Lock()
				summaryCount++
				mu.Unlock()
				return summaryStream(ctx, model)
			},
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	session, err := NewSession(services, SessionOptions{Model: fakeModelWithProvider(sessionProvider), SkipBuiltinTools: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	go func() {
		for range session.Events() { //nolint:revive // drain
		}
	}()
	for _, text := range []string{"one", "two", "three"} {
		if _, err := session.inner.AppendMessage(agent.AgentMessage{User: &agent.UserMessage{Role: agent.RoleUser, Content: ai.UserContentBlocks{ai.TextContent{Text: text + strings.Repeat(" word", 80)}}}}); err != nil {
			t.Fatal(err)
		}
		if _, err := session.inner.AppendMessage(agent.AgentMessage{Assistant: &agent.AssistantMessage{Role: agent.RoleAssistant, Content: []ai.AssistantContentBlock{ai.TextContent{Text: text + strings.Repeat(" reply", 80)}}, StopReason: ai.StopReasonStop}}); err != nil {
			t.Fatal(err)
		}
	}
	session.RefreshContext()
	count := func(n *int) func() int {
		return func() int {
			mu.Lock()
			defer mu.Unlock()
			return *n
		}
	}
	return &summaryModelHarness{session: session, sessionCalls: count(&sessionCount), summaryCalls: count(&summaryCount)}
}

func summaryText(text string) func(context.Context, *ai.Model) (*ai.AssistantMessageEventStream, error) {
	return func(context.Context, *ai.Model) (*ai.AssistantMessageEventStream, error) {
		stream, _ := runtimeTestTextStream("other", "summary", text)
		return stream, nil
	}
}

// compacted runs a manual compaction and returns its summary after checking that the compaction entry was written.
func compacted(t *testing.T, session *Session) string {
	t.Helper()
	result, err := session.Compact(t.Context(), "")
	if err != nil {
		t.Fatal(err)
	}
	entries := session.inner.GetEntries()
	if last := entries[len(entries)-1]; last.Base().Type != "compaction" {
		t.Fatalf("last entry is %s, want compaction", last.Base().Type)
	}
	return result.Summary
}

func TestCompactionSummaryModelUnsetUsesTheSessionModel(t *testing.T) {
	h := newSummaryModelHarness(t, "", true, summaryText("configured summary"))
	summary := compacted(t, h.session)
	if h.sessionCalls() == 0 || h.summaryCalls() != 0 || !strings.Contains(summary, "session summary") {
		t.Fatalf("session=%d summary=%d", h.sessionCalls(), h.summaryCalls())
	}
}

func TestCompactionSummaryModelWritesTheSummary(t *testing.T) {
	h := newSummaryModelHarness(t, "other/summary", true, summaryText("configured summary"))
	summary := compacted(t, h.session)
	if h.sessionCalls() != 0 || h.summaryCalls() == 0 || !strings.Contains(summary, "configured summary") {
		t.Fatalf("session=%d summary=%d", h.sessionCalls(), h.summaryCalls())
	}
}

func TestCompactionSummaryModelUnknownOrUnparsableFallsBack(t *testing.T) {
	for _, setting := range []string{"other/missing", "no-slash", "/summary", "other/"} {
		h := newSummaryModelHarness(t, setting, true, summaryText("configured summary"))
		summary := compacted(t, h.session)
		if h.sessionCalls() == 0 || h.summaryCalls() != 0 || !strings.Contains(summary, "session summary") {
			t.Fatalf("%q: session=%d summary=%d", setting, h.sessionCalls(), h.summaryCalls())
		}
	}
}

func TestCompactionSummaryModelProviderErrorFallsBack(t *testing.T) {
	h := newSummaryModelHarness(t, "other/summary", true, func(context.Context, *ai.Model) (*ai.AssistantMessageEventStream, error) {
		return nil, errors.New("400 invalid request")
	})
	summary := compacted(t, h.session)
	if h.summaryCalls() != 1 || h.sessionCalls() == 0 || !strings.Contains(summary, "session summary") {
		t.Fatalf("session=%d summary=%d", h.sessionCalls(), h.summaryCalls())
	}
}

func TestCompactionSummaryModelCancelDoesNotFallBack(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	h := newSummaryModelHarness(t, "other/summary", true, func(context.Context, *ai.Model) (*ai.AssistantMessageEventStream, error) {
		cancel()
		return nil, errors.New("request aborted")
	})
	_, err := h.session.Compact(ctx, "")
	if err == nil || !errors.Is(err, errCompactionCancelled) && !errors.Is(err, errOperationAborted) {
		t.Fatalf("err=%v, want a cancelled or aborted error", err)
	}
	if h.summaryCalls() != 1 || h.sessionCalls() != 0 {
		t.Fatalf("session=%d summary=%d: a cancel must not fall back", h.sessionCalls(), h.summaryCalls())
	}
}

func TestCompactionSummaryModelSettingRoundTripsAndBlankIsUnset(t *testing.T) {
	services := newTestServices(t)
	sm := services.SettingsManager()
	if got := sm.GetCompactionSummaryModel(); got != "" {
		t.Fatalf("unset=%q", got)
	}
	for setting, want := range map[string]string{"github-copilot/claude-sonnet-5.5": "github-copilot/claude-sonnet-5.5", "": "", "   ": ""} {
		if err := sm.UpdateGlobal(func(settings *icodingagent.Settings) {
			settings.Compaction = &icodingagent.CompactionSettingsJSON{SummaryModel: &setting}
		}); err != nil {
			t.Fatal(err)
		}
		if got := sm.GetCompactionSummaryModel(); got != want {
			t.Fatalf("%q: got %q want %q", setting, got, want)
		}
	}
	var reloaded icodingagent.Settings
	err := json.Unmarshal([]byte(`{"compaction":{"keepRecentTokens":5,"summaryModel":"a/b"}}`), &reloaded)
	if err != nil || reloaded.Compaction == nil || reloaded.Compaction.SummaryModel == nil || *reloaded.Compaction.SummaryModel != "a/b" {
		t.Fatalf("parse=%+v,%v", reloaded, err)
	}
	out, err := json.Marshal(reloaded.Compaction)
	if err != nil || !strings.Contains(string(out), `"summaryModel":"a/b"`) || !strings.Contains(string(out), `"keepRecentTokens":5`) {
		t.Fatalf("marshal=%s,%v", out, err)
	}
}

func TestBranchSummaryIgnoresTheCompactionSummaryModel(t *testing.T) {
	h := newSummaryModelHarness(t, "other/summary", true, summaryText("configured summary"))
	entries := h.session.inner.GetEntries()
	target := entries[0].Base().ID
	result, err := h.session.NavigateTree(t.Context(), target, NavigateTreeOptions{Summarize: true})
	if err != nil || result.Cancelled || result.SummaryEntry == nil {
		t.Fatalf("navigation=%#v,%v", result, err)
	}
	if h.summaryCalls() != 0 || h.sessionCalls() != 1 || !strings.Contains(result.SummaryEntry.Summary, "session summary") {
		t.Fatalf("session=%d summary=%d", h.sessionCalls(), h.summaryCalls())
	}
}
