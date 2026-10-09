package coding

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

type modelExtensionHarness struct {
	session  *Session
	services *AgentSessionServices
	models   []*ai.Model
	provider *scriptedProvider
	runner   *inproc.Runner
}

func newModelExtensionHarness(t *testing.T, reasoning []bool, settings string, configured bool, ext extension.Extension, tools []agent.AgentTool, responses ...scriptedResponse) *modelExtensionHarness {
	t.Helper()
	dir := t.TempDir()
	if settings == "" {
		settings = "{}"
	}
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(settings), 0o600); err != nil {
		t.Fatal(err)
	}
	services, err := CreateAgentSessionServices(CreateAgentSessionServicesOptions{CWD: dir, AgentDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(services.Close)
	provider := &scriptedProvider{responses: responses}
	var raw []*ai.Model
	for i, value := range reasoning {
		model := nativeCompatModel(fmt.Sprintf("faux-%d", i+1), "faux", "https://faux.invalid")
		model.DisplayName = []string{"One", "Two"}[i%2]
		model.Capabilities.ContextWindow = 128000
		model.Capabilities.MaxOutputTokens = 8192
		model.ProviderMeta.Reasoning = value
		if value {
			model.Capabilities.MaxThinking = ai.ThinkingLevelHigh
		}
		raw = append(raw, model)
	}
	stream := func(ctx context.Context, _ *ai.Model, request ai.TranscriptContext, opts ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
		return provider.Stream(ctx, request, opts)
	}
	native := &ai.ModelsProvider{ID: "faux", Name: "Faux", GetModels: func() ([]*ai.Model, error) { return raw, nil }, Stream: stream, StreamSimple: stream, Auth: ai.ProviderAuth{APIKey: &ai.APIKeyAuth{Name: "Faux key", Resolve: func(context.Context, ai.APIKeyAuthInput) (*ai.AuthResult, error) {
		if !configured {
			return nil, nil
		}
		return &ai.AuthResult{Auth: ai.ModelAuth{APIKey: "faux-key"}}, nil
	}}}}
	if err := services.ModelRuntime().RegisterNativeProvider(native); err != nil {
		t.Fatal(err)
	}
	result := services.ModelRuntime().Refresh(t.Context(), ai.ModelsRefreshOptions{AllowNetwork: new(false)})
	if result.Aborted || len(result.Errors) > 0 {
		t.Fatal(result)
	}
	var models []*ai.Model
	for _, m := range raw {
		models = append(models, services.ModelRuntime().GetModel("faux", m.ID))
	}
	runner := inproc.NewRunner([]extension.Extension{ext}, dir)
	session, err := NewSession(services, SessionOptions{Model: models[0], SystemPrompt: "test", Runner: runner, Tools: tools, SkipBuiltinTools: tools != nil, existing: icodingagent.NewSession("model-extension-test", dir)})
	if err != nil {
		t.Fatal(err)
	}
	session.ReplaceRunner(runner)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range session.Events() {
		}
	}()
	t.Cleanup(func() { _ = session.Close(); <-done })
	return &modelExtensionHarness{session: session, services: services, models: models, provider: provider, runner: runner}
}

// Pi: packages/coding-agent/src/core/agent-session.ts:1653 (Session.setScopedModels); packages/coding-agent/src/core/agent-session.ts:2599 (Session.getAvailableThinkingLevels); packages/coding-agent/src/core/agent-session.ts:2627 (Session.cycleThinkingLevel).
func TestSessionModelExtensionCyclesUpstream(t *testing.T) {
	// .upstream/v0.87.1/packages/coding-agent/test/suite/agent-session-model-extension.test.ts:91
	t.Run("cycleModel and cycleThinkingLevel are session-only by default", func(t *testing.T) {
		h := newModelExtensionHarness(t, []bool{true, true}, `{"defaultProvider":"faux","defaultModel":"faux-1","defaultThinkingLevel":"low"}`, true, extension.Extension{}, nil)
		if _, err := h.session.CycleModel("forward"); err != nil {
			t.Fatal(err)
		}
		if h.session.Model().ID != "faux-2" || h.services.Settings().DefaultModel != "faux-1" {
			t.Fatal("model cycle rewrote defaults or failed")
		}
		if err := h.session.SetThinkingLevel(ai.ThinkingOff); err != nil {
			t.Fatal(err)
		}
		level, err := h.session.CycleThinkingLevel()
		if err != nil || level != ai.ThinkingMinimal || h.services.Settings().DefaultThinkingLevel != "low" {
			t.Fatalf("cycle=%s err=%v", level, err)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/suite/agent-session-model-extension.test.ts:175
	t.Run("cycles through scoped models and preserves the scoped thinking preference", func(t *testing.T) {
		h := newModelExtensionHarness(t, []bool{true, false}, "", true, extension.Extension{}, nil)
		h.session.SetScopedModels([]ScopedModel{{Model: h.models[0], ThinkingLevel: ai.ThinkingHigh}, {Model: h.models[1]}})
		if err := h.session.SetThinkingLevel(ai.ThinkingHigh); err != nil {
			t.Fatal(err)
		}
		if _, err := h.session.CycleModel("forward"); err != nil {
			t.Fatal(err)
		}
		if h.session.Model().ID != "faux-2" || h.session.ThinkingLevel() != ai.ThinkingOff {
			t.Fatal("non-reasoning cycle was not clamped")
		}
		if _, err := h.session.CycleModel("forward"); err != nil {
			t.Fatal(err)
		}
		if h.session.Model().ID != "faux-1" || h.session.ThinkingLevel() != ai.ThinkingHigh {
			t.Fatal("scoped thinking preference was lost")
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/suite/agent-session-model-extension.test.ts:200
	t.Run("clamps thinking levels to model capabilities and cycles available levels", func(t *testing.T) {
		h := newModelExtensionHarness(t, []bool{false}, "", true, extension.Extension{}, nil)
		if err := h.session.SetThinkingLevel(ai.ThinkingHigh); err != nil {
			t.Fatal(err)
		}
		level, err := h.session.CycleThinkingLevel()
		if err != nil || level != "" || h.session.ThinkingLevel() != ai.ThinkingOff {
			t.Fatalf("level=%s err=%v", level, err)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/suite/agent-session-model-extension.test.ts:209
	t.Run("cycles xhigh before max when both are supported", func(t *testing.T) {
		h := newModelExtensionHarness(t, []bool{true}, "", true, extension.Extension{}, nil)
		h.models[0].ThinkingLevelMap = ai.ThinkingLevelMap{ai.ThinkingXHigh: new("xhigh"), ai.ThinkingMax: new("max")}
		want := []ai.ModelThinkingLevel{ai.ThinkingOff, ai.ThinkingMinimal, ai.ThinkingLow, ai.ThinkingMedium, ai.ThinkingHigh, ai.ThinkingXHigh, ai.ThinkingMax}
		if got := h.session.AvailableThinkingLevels(); !reflect.DeepEqual(got, want) {
			t.Fatalf("levels=%v", got)
		}
		if err := h.session.SetThinkingLevel(ai.ThinkingHigh); err != nil {
			t.Fatal(err)
		}
		for _, wanted := range []ai.ModelThinkingLevel{ai.ThinkingXHigh, ai.ThinkingMax, ai.ThinkingOff} {
			got, err := h.session.CycleThinkingLevel()
			if err != nil || got != wanted {
				t.Fatalf("cycle=%s, want %s err=%v", got, wanted, err)
			}
		}
	})
}

func TestSessionModelExtensionExistingUpstream(t *testing.T) {
	// .upstream/v0.87.1/packages/coding-agent/test/suite/agent-session-model-extension.test.ts:24
	t.Run("setModel saves the model to the session and emits model_select", func(t *testing.T) {
		var events []string
		ext := extension.Extension{Handlers: map[string][]extension.HandlerFn{"model_select": {func(args ...any) (any, error) {
			event := args[0].(extension.ModelSelectEvent)
			previous := event.PreviousModel
			model := event.Model
			events = append(events, previous.ID+"->"+model.ID+":"+event.Source)
			return nil, nil
		}}}}
		h := newModelExtensionHarness(t, []bool{true, true}, "", true, ext, nil)
		if err := h.session.SetModel(h.models[1]); err != nil {
			t.Fatal(err)
		}
		if h.session.Model().ID != "faux-2" || !reflect.DeepEqual(events, []string{"faux-1->faux-2:set"}) {
			t.Fatalf("model=%s events=%v", h.session.Model().ID, events)
		}
		var changes []string
		for _, entry := range h.session.Inner().GetEntries() {
			if entry.Base().Type == "model_change" {
				var change struct {
					Provider string `json:"provider"`
					ModelID  string `json:"modelId"`
				}
				if err := json.Unmarshal(entry.Raw(), &change); err != nil {
					t.Fatal(err)
				}
				changes = append(changes, change.Provider+"/"+change.ModelID)
			}
		}
		if !reflect.DeepEqual(changes, []string{"faux/faux-2"}) {
			t.Fatalf("model changes=%v", changes)
		}
		if h.services.Settings().DefaultProvider != "" || h.services.Settings().DefaultModel != "" {
			t.Fatal("session switch persisted defaults")
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/suite/agent-session-model-extension.test.ts:56
	t.Run("only persists model and thinking defaults when requested", func(t *testing.T) {
		h := newModelExtensionHarness(t, []bool{true, true}, "", true, extension.Extension{}, nil)
		if err := h.session.SetModel(h.models[1]); err != nil {
			t.Fatal(err)
		}
		if h.services.Settings().DefaultProvider != "" || h.services.Settings().DefaultModel != "" {
			t.Fatal("model defaults changed")
		}
		if err := h.session.SetThinkingLevel(ai.ThinkingLow); err != nil {
			t.Fatal(err)
		}
		if h.services.Settings().DefaultThinkingLevel != "" {
			t.Fatal("thinking default changed")
		}
		if err := h.session.SetModel(h.models[1], ModelMutationOptions{Persist: true}); err != nil {
			t.Fatal(err)
		}
		if h.services.Settings().DefaultProvider != "faux" || h.services.Settings().DefaultModel != "faux-2" {
			t.Fatal("requested model defaults missing")
		}
		if err := h.session.SetThinkingLevel(ai.ThinkingHigh, ModelMutationOptions{Persist: true}); err != nil {
			t.Fatal(err)
		}
		if h.services.Settings().DefaultThinkingLevel != "high" {
			t.Fatal("requested thinking default missing")
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/suite/agent-session-model-extension.test.ts:81
	t.Run("persists the requested default thinking level even when the current model clamps it", func(t *testing.T) {
		h := newModelExtensionHarness(t, []bool{true}, "", true, extension.Extension{}, nil)
		if err := h.session.SetThinkingLevel(ai.ThinkingMax, ModelMutationOptions{Persist: true}); err != nil {
			t.Fatal(err)
		}
		if h.session.ThinkingLevel() != ai.ThinkingHigh || h.services.Settings().DefaultThinkingLevel != "max" {
			t.Fatalf("effective=%s default=%s", h.session.ThinkingLevel(), h.services.Settings().DefaultThinkingLevel)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/suite/agent-session-model-extension.test.ts:114
	t.Run("applies per-model thinking level override on model switch", func(t *testing.T) {
		h := newModelExtensionHarness(t, []bool{true, true}, `{"defaultThinkingLevel":"medium"}`, true, extension.Extension{}, nil)
		if err := h.services.SettingsManager().SetModelThinkingLevel("faux", "faux-2", "low"); err != nil {
			t.Fatal(err)
		}
		if err := h.session.SetThinkingLevel(ai.ThinkingHigh); err != nil {
			t.Fatal(err)
		}
		if h.session.ThinkingLevel() != ai.ThinkingHigh {
			t.Fatal("high not applied")
		}
		if err := h.session.SetModel(h.models[1]); err != nil {
			t.Fatal(err)
		}
		if h.session.ThinkingLevel() != ai.ThinkingLow {
			t.Fatal("per-model override lost")
		}
		if err := h.session.SetModel(h.models[0]); err != nil {
			t.Fatal(err)
		}
		if h.session.ThinkingLevel() != ai.ThinkingMedium {
			t.Fatal("global default lost")
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/suite/agent-session-model-extension.test.ts:142
	t.Run("falls back to current session thinking level when no per-model or global default is configured", func(t *testing.T) {
		h := newModelExtensionHarness(t, []bool{true, true}, "", true, extension.Extension{}, nil)
		if err := h.session.SetThinkingLevel(ai.ThinkingHigh); err != nil {
			t.Fatal(err)
		}
		if err := h.session.SetModel(h.models[1]); err != nil {
			t.Fatal(err)
		}
		if h.session.ThinkingLevel() != ai.ThinkingHigh {
			t.Fatal("current thinking lost")
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/suite/agent-session-model-extension.test.ts:156
	t.Run("per-model override takes priority over global default during model switch", func(t *testing.T) {
		h := newModelExtensionHarness(t, []bool{true, true}, `{"defaultThinkingLevel":"high","modelThinkingLevels":{"faux/faux-2":"minimal"}}`, true, extension.Extension{}, nil)
		if err := h.session.SetModel(h.models[1]); err != nil {
			t.Fatal(err)
		}
		if h.session.ThinkingLevel() != ai.ThinkingMinimal {
			t.Fatal("per-model priority lost")
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/suite/agent-session-model-extension.test.ts:229
	t.Run("throws when setModel is called without configured auth", func(t *testing.T) {
		h := newModelExtensionHarness(t, []bool{true, true}, "", false, extension.Extension{}, nil)
		err := h.session.SetModel(h.models[1])
		if err == nil || !strings.Contains(err.Error(), "No API key for faux/faux-2") {
			t.Fatalf("setModel error=%v", err)
		}
	})
}
