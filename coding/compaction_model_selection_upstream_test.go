package coding

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

// Pi: packages/coding-agent/src/core/settings-manager.ts:19 (CompactionModelOverride.reserveTokens); packages/coding-agent/src/core/settings-manager.ts:19 (CompactionSettingsJSON.reserveTokens); packages/coding-agent/src/core/settings-manager.ts:20 (CompactionModelOverride.keepRecentTokens); packages/coding-agent/src/core/settings-manager.ts:32 (CompactionSettingsJSON.modelOverrides); packages/coding-agent/src/core/settings-manager.ts:955 (SettingsManager.getCompactionReserveTokens).
func TestCompactionNewModelPolicyUpstream(t *testing.T) {
	// .upstream/v0.87.1/packages/coding-agent/test/suite/agent-session-compaction-model-overrides.test.ts:136
	t.Run("uses the newly selected model without changing ordinary settings", func(t *testing.T) {
		services := newTestServices(t)
		if err := services.SettingsManager().UpdateGlobal(func(settings *icodingagent.Settings) {
			settings.Compaction = &icodingagent.CompactionSettingsJSON{ReserveTokens: new(10.), ModelOverrides: map[string]icodingagent.CompactionModelOverride{"faux/big": {ReserveTokens: new(8000.), KeepRecentTokens: new(150.)}}}
		}); err != nil {
			t.Fatal(err)
		}
		smallProvider := ai.NewFauxProvider(ai.FauxConfig{ProviderID: "faux", Model: "small"})
		bigProvider := ai.NewFauxProvider(ai.FauxConfig{ProviderID: "faux", Model: "big"})
		small := &ai.Model{ID: "small", Provider: smallProvider, Capabilities: ai.ModelCapabilities{ContextWindow: 4000, MaxOutputTokens: 3000}}
		big := &ai.Model{ID: "big", Provider: bigProvider, Capabilities: ai.ModelCapabilities{ContextWindow: 10000, MaxOutputTokens: 3000}}
		session, err := NewSession(services, SessionOptions{Model: small, SkipBuiltinTools: true})
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := session.Close(); err != nil {
				t.Error(err)
			}
		}()
		session.ReplaceRunner(inproc.NewRunner([]extension.Extension{{Handlers: map[string][]extension.HandlerFn{"session_before_compact": {func(args ...any) (any, error) {
			prep := args[0].(extension.SessionBeforeCompactEvent).Preparation
			return extension.SessionBeforeCompactResult{Compaction: &extension.CompactionResult{Summary: "big model summary", FirstKeptEntryID: prep.FirstKeptEntryID, TokensBefore: prep.TokensBefore}}, nil
		}}}}}, t.TempDir()))
		seedCompactionCatalogHistory(t, session, 2500)
		smallProvider.SetResponses([]ai.FauxResponseStep{ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("small response")}, StopReason: "stop"})})
		events := runCompactionCatalogOperation(t, session, func() error { _, err := session.Send(t.Context(), "continue on small"); return err })
		for _, event := range events {
			if _, ok := event.(agent.CompactionStartEvent); ok {
				t.Fatal("small model compacted")
			}
		}
		seedCompactionCatalogHistory(t, session, 2500)
		bigProvider.SetResponses([]ai.FauxResponseStep{ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("big response")}, StopReason: "stop"})})
		events = runCompactionCatalogOperation(t, session, func() error {
			if err := session.SetModel(big); err != nil {
				return err
			}
			_, err := session.Send(t.Context(), "continue on big")
			return err
		})
		var ends []agent.CompactionEndEvent
		for _, event := range events {
			if end, ok := event.(agent.CompactionEndEvent); ok {
				ends = append(ends, end)
			}
		}
		if len(ends) != 1 || ends[0].Summary != "big model summary" {
			t.Fatalf("ends=%+v", ends)
		}
		if got, err := services.SettingsManager().GetCompactionReserveTokens(); got != 10 || err != nil {
			t.Fatalf("ordinary reserve=%d", got)
		}
		if err := session.SetModel(small); err != nil {
			t.Fatal(err)
		}
		settings, err := session.compactionSettings()
		if err != nil {
			t.Fatal(err)
		}
		if got := settings.ReserveTokens; got != 10 {
			t.Fatalf("small reserve=%d", got)
		}
	})
}

// Pi: packages/coding-agent/src/core/settings-manager.ts:19 (CompactionModelOverride.reserveTokens); packages/coding-agent/src/core/settings-manager.ts:20 (CompactionModelOverride.keepRecentTokens); packages/coding-agent/src/core/settings-manager.ts:32 (CompactionSettingsJSON.modelOverrides).
func TestCompactionCapturesModelBeforeAuthUpstream(t *testing.T) {
	// .upstream/v0.87.1/packages/coding-agent/test/suite/agent-session-compaction-model-overrides.test.ts:177
	// Go provider auth resolves inside Stream; the callback changes the live model before the request reaches HTTP.
	t.Run("captures model identity before awaiting summarization auth", func(t *testing.T) {
		services := newTestServices(t)
		if err := services.SettingsManager().UpdateGlobal(func(settings *icodingagent.Settings) {
			settings.Compaction = &icodingagent.CompactionSettingsJSON{ModelOverrides: map[string]icodingagent.CompactionModelOverride{"faux/first": {ReserveTokens: new(2000.), KeepRecentTokens: new(150.)}, "faux/second": {ReserveTokens: new(4000.), KeepRecentTokens: new(20000.)}}}
		}); err != nil {
			t.Fatal(err)
		}
		type request struct {
			ID        string `json:"model"`
			MaxTokens int    `json:"max_tokens"`
		}
		requests := make(chan request, 1)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var body request
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			requests <- body
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, "event: message_start\ndata: {\"message\":{\"id\":\"summary\",\"usage\":{}}}\n\nevent: content_block_start\ndata: {\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"summary\"}}\n\nevent: content_block_stop\ndata: {\"index\":0}\n\nevent: message_delta\ndata: {\"delta\":{\"stop_reason\":\"end_turn\"}}\n\nevent: message_stop\ndata: {}\n\n")
		}))
		defer server.Close()
		var session *Session
		second := &ai.Model{ID: "second", Provider: ai.NewFauxProvider(ai.FauxConfig{ProviderID: "faux", Model: "second"}), Capabilities: ai.ModelCapabilities{ContextWindow: 128000, MaxOutputTokens: 8192}}
		provider := ai.NewAnthropicProvider(ai.AnthropicConfig{ProviderID: "faux", Model: "first", BaseURL: server.URL, GetAPIKey: func(context.Context) (string, error) {
			if err := session.SetModel(second); err != nil {
				return "", err
			}
			return "test-key", nil
		}})
		first := &ai.Model{ID: "first", Provider: provider, Capabilities: ai.ModelCapabilities{ContextWindow: 128000, MaxOutputTokens: 8192}}
		var err error
		session, err = NewSession(services, SessionOptions{Model: first, SkipBuiltinTools: true})
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := session.Close(); err != nil {
				t.Error(err)
			}
		}()
		seedCompactionCatalogHistory(t, session, 650)
		runCompactionCatalogOperation(t, session, func() error { _, err := session.Compact(t.Context(), ""); return err })
		if session.Model().ID != "second" {
			t.Fatal("auth callback did not change the live model")
		}
		select {
		case got := <-requests:
			if got.ID != "first" || got.MaxTokens != 1600 {
				t.Fatalf("request=%+v", got)
			}
		default:
			t.Fatal("summary request missing")
		}
		if len(requests) != 0 {
			t.Fatal("unexpected additional summary request")
		}
	})
}
