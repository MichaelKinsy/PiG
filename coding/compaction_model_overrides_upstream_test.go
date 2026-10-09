package coding

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/codingagent/compaction"
)

type compactionCatalogFaux interface {
	ai.Provider
	SetResponses([]ai.FauxResponseStep)
	PendingResponseCount() int
}

func newCompactionCatalogSession(t *testing.T, enabled bool) (*Session, *AgentSessionServices, compactionCatalogFaux) {
	t.Helper()
	services := newTestServices(t)
	if err := services.SettingsManager().UpdateGlobal(func(settings *icodingagent.Settings) {
		settings.Compaction = &icodingagent.CompactionSettingsJSON{Enabled: new(enabled), ReserveTokens: new(10.), KeepRecentTokens: new(20000.), ModelOverrides: map[string]icodingagent.CompactionModelOverride{"faux/faux-1": {ReserveTokens: new(2000.), KeepRecentTokens: new(150.)}}}
	}); err != nil {
		t.Fatal(err)
	}
	provider := ai.NewFauxProvider(ai.FauxConfig{ProviderID: "faux", Model: "faux-1"})
	model := &ai.Model{ID: "faux-1", Provider: provider, Capabilities: ai.ModelCapabilities{ContextWindow: 4000, MaxOutputTokens: 3000}}
	session, err := NewSession(services, SessionOptions{Model: model, SkipBuiltinTools: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := session.Close(); err != nil {
			t.Error(err)
		}
	})
	return session, services, provider
}

func seedCompactionCatalogHistory(t *testing.T, session *Session, totalTokens int) string {
	t.Helper()
	model := session.Model()
	recent := ""
	for _, label := range []string{"old", "recent"} {
		id, err := session.inner.AppendMessage(agent.AgentMessage{User: &agent.UserMessage{Role: "user", Content: ai.UserContentBlocks{ai.TextContent{Text: label + strings.Repeat("x", 400-len(label))}}, Timestamp: time.Now().Add(-2 * time.Second).UnixMilli()}})
		if err != nil {
			t.Fatal(err)
		}
		recent = id
		_, err = session.inner.AppendMessage(agent.AgentMessage{Assistant: &agent.AssistantMessage{Role: "assistant", Content: []ai.AssistantContentBlock{ai.TextContent{Text: label + strings.Repeat("y", 400-len(label))}}, API: "faux", Provider: model.Provider.ID(), ModelID: model.ID, Usage: &ai.Usage{Input: totalTokens, TotalTokens: totalTokens}, StopReason: ai.StopReasonStop, Timestamp: time.Now().Add(-time.Second).UnixMilli()}})
		if err != nil {
			t.Fatal(err)
		}
	}
	session.agent.SetMessages(session.inner.BuildContext(nil))
	return recent
}

func runCompactionCatalogOperation(t *testing.T, session *Session, operation func() error) []agent.AgentEvent {
	t.Helper()
	marker := &agent.AgentSettledEvent{}
	done := make(chan []agent.AgentEvent, 1)
	go func() {
		var events []agent.AgentEvent
		for event := range session.Events() {
			if event == marker {
				done <- events
				return
			}
			events = append(events, event)
		}
		done <- events
	}()
	err := operation()
	session.emitOrderedEvent(marker)
	events := <-done
	if err != nil {
		t.Fatal(err)
	}
	return events
}

func TestCompactionModelOverridesPreparationUpstream(t *testing.T) {
	// .upstream/v0.87.1/packages/coding-agent/test/suite/agent-session-compaction-model-overrides.test.ts:36
	for _, path := range []string{"manual", "pre-prompt", "post-run", "overflow"} {
		t.Run("uses model token settings for "+path+" compaction and extension preparation", func(t *testing.T) {
			session, _, provider := newCompactionCatalogSession(t, path != "manual")
			var preparations []extension.SessionBeforeCompactEvent
			session.ReplaceRunner(inproc.NewRunner([]extension.Extension{{Handlers: map[string][]extension.HandlerFn{"session_before_compact": {func(args ...any) (any, error) {
				event := args[0].(extension.SessionBeforeCompactEvent)
				preparations = append(preparations, event)
				prep := event.Preparation
				return extension.SessionBeforeCompactResult{Compaction: &extension.CompactionResult{Summary: "compacted history", FirstKeptEntryID: prep.FirstKeptEntryID, TokensBefore: prep.TokensBefore}}, nil
			}}}}}, t.TempDir()))
			tokens := 650
			if path == "pre-prompt" {
				tokens = 2500
			}
			recent := seedCompactionCatalogHistory(t, session, tokens)
			events := runCompactionCatalogOperation(t, session, func() error {
				if path == "manual" {
					_, err := session.Compact(t.Context(), "")
					return err
				} else {
					text := "done"
					if path == "post-run" {
						text = strings.Repeat("z", 8000)
					}
					responses := []ai.FauxResponseStep{ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText(text)}, StopReason: "stop"})}
					if path == "overflow" {
						responses = []ai.FauxResponseStep{ai.FauxStaticStep(ai.FauxResponse{StopReason: "error", ErrorMessage: "prompt is too long"}), ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("recovered")}, StopReason: "stop"})}
					}
					provider.SetResponses(responses)
					_, err := session.Send(t.Context(), "continue")
					return err
				}
			})
			if len(preparations) != 1 {
				t.Fatalf("preparations=%d", len(preparations))
			}
			prep := preparations[0].Preparation
			want := compaction.CompactionSettings{Enabled: path != "manual", ReserveTokens: 2000, KeepRecentTokens: 150}
			if prep.Settings != want {
				t.Fatalf("settings=%+v, want %+v", prep.Settings, want)
			}
			reason := "threshold"
			if path == "manual" || path == "overflow" {
				reason = path
			}
			if preparations[0].Reason != reason {
				t.Fatalf("reason=%s", preparations[0].Reason)
			}
			if (path == "manual" || path == "pre-prompt") && prep.FirstKeptEntryID != recent {
				t.Fatalf("first kept=%s, want %s", prep.FirstKeptEntryID, recent)
			}
			var ends []agent.CompactionEndEvent
			for _, event := range events {
				if end, ok := event.(agent.CompactionEndEvent); ok {
					ends = append(ends, end)
				}
			}
			if len(ends) != 1 || ends[0].Aborted || ends[0].WillRetry != (path == "overflow") || ends[0].Summary != "compacted history" {
				t.Fatalf("ends=%+v", ends)
			}
			if provider.PendingResponseCount() != 0 {
				t.Fatal("responses left pending")
			}
		})
	}
}

// Pi: packages/coding-agent/src/core/session-manager.ts:94 (CompactionEntry.firstKeptEntryId).
// Pi: packages/coding-agent/src/core/session-manager.ts:93 (CompactionEntry.summary).
func TestCompactionModelOverridesBuiltinBudgetsUpstream(t *testing.T) {
	// .upstream/v0.87.1/packages/coding-agent/test/suite/agent-session-compaction-model-overrides.test.ts:105
	for _, path := range []string{"manual", "automatic"} {
		t.Run("passes resolved budgets to built-in "+path+" summarization", func(t *testing.T) {
			session, _, provider := newCompactionCatalogSession(t, true)
			recent := seedCompactionCatalogHistory(t, session, 2500)
			var budgets []int
			responses := []ai.FauxResponseStep{ai.FauxFactoryStep(func(_ ai.TranscriptContext, options ai.StreamOptions, _ *ai.FauxProviderState, _ *ai.Model) (ai.AssistantMessage, error) {
				budgets = append(budgets, options.MaxTokens)
				return ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("built-in summary")}, StopReason: "stop"}.AssistantMessage(), nil
			})}
			if path == "automatic" {
				responses = append(responses, ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("done")}, StopReason: "stop"}))
			}
			provider.SetResponses(responses)
			events := runCompactionCatalogOperation(t, session, func() error {
				if path == "manual" {
					_, err := session.Compact(t.Context(), "")
					return err
				}
				_, err := session.Send(t.Context(), "continue")
				return err
			})
			if len(budgets) != 1 || budgets[0] != 1600 {
				t.Fatalf("budgets=%v", budgets)
			}
			found := false
			for _, event := range events {
				if end, ok := event.(agent.CompactionEndEvent); ok {
					found = true
					if end.FirstKeptEntryID != recent || end.Summary != "built-in summary" {
						t.Fatalf("compaction=%+v", end)
					}
				}
			}
			if !found {
				t.Fatal("compaction missing")
			}
			persisted := false
			for _, entry := range session.currentBranch() {
				if entry.Base().Type != "compaction" {
					continue
				}
				var stored icodingagent.CompactionEntry
				if err := json.Unmarshal(entry.Raw(), &stored); err != nil {
					t.Fatal(err)
				}
				persisted = true
				if stored.FirstKeptEntryID != recent || stored.Summary != "built-in summary" {
					t.Fatalf("stored compaction=%+v", stored)
				}
			}
			if !persisted {
				t.Fatal("compaction entry not persisted")
			}
			if provider.PendingResponseCount() != 0 {
				t.Fatal("responses left pending")
			}
		})
	}
}
