package coding

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// remoteCatalogRecords are models the remote catalog (models-store.json) carries: a dotted id the bundled catalog lacks, an id with a slash and a colon,
// and a window and prices that replace a bundled model's.
var remoteCatalogRecords = map[string][]string{
	"github-copilot": {
		`{"id":"claude-fable-5.2","name":"Claude Fable 5.2","api":"anthropic-messages","provider":"github-copilot","baseUrl":"https://api.individual.githubcopilot.com","reasoning":true,"input":["text","image"],"cost":{"input":10,"output":50,"cacheRead":1,"cacheWrite":12.5},"contextWindow":1000000,"maxTokens":128000}`,
		`{"id":"claude-haiku-4.5","name":"Claude Haiku 4.5","api":"openai-completions","provider":"github-copilot","baseUrl":"https://api.individual.githubcopilot.com","reasoning":false,"input":["text"],"cost":{"input":3,"output":4,"cacheRead":0.3,"cacheWrite":0},"contextWindow":123456,"maxTokens":8000}`,
	},
	"openai": {
		`{"id":"acme/spark:beta","name":"Spark","api":"openai-responses","provider":"openai","baseUrl":"https://api.openai.com/v1","reasoning":false,"input":["text"],"cost":{"input":1,"output":2,"cacheRead":0.1,"cacheWrite":0},"contextWindow":400000,"maxTokens":64000}`,
	},
}

// remoteCatalogServices is Services over an agent directory whose models-store.json holds remoteCatalogRecords and whose models.json defines two custom models, one with a contextWindow and one without.
func remoteCatalogServices(t *testing.T) *AgentSessionServices {
	t.Helper()
	t.Setenv("PIG_HOME", t.TempDir())
	t.Setenv("PI_OFFLINE", "")
	t.Setenv("OPENAI_API_KEY", "")
	dir := t.TempDir()
	generatedAt := ai.GetBuiltinModelDataGeneratedAt()
	if generatedAt == nil {
		t.Fatal("the generated catalogs record no generation time")
	}
	store := ai.NewFileModelsStore(filepath.Join(dir, "models-store.json"))
	for provider, records := range remoteCatalogRecords {
		raw := make([]json.RawMessage, 0, len(records))
		for _, record := range records {
			raw = append(raw, json.RawMessage(record))
		}
		entry := ai.ModelsStoreEntry{Models: mustStoredModels(raw), CheckedAt: new(float64(1)), LastModified: new(*generatedAt + 1)}
		if err := store.Write(t.Context(), provider, entry); err != nil {
			t.Fatal(err)
		}
	}
	custom := map[string]any{"providers": map[string]any{"custom-proxy": map[string]any{
		"baseUrl": "http://127.0.0.1:9/v1", "api": "openai-completions", "apiKey": "key",
		"models": []map[string]any{{"id": "with-window", "contextWindow": 64000}, {"id": "no-window"}},
	}}}
	data, err := json.Marshal(custom)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "models.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	services, err := CreateAgentSessionServices(CreateAgentSessionServicesOptions{CWD: dir, AgentDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(services.Close)
	return services
}

// A model built for a Session carries the window and prices of the catalog entry ModelRuntime.getModel returns, whichever catalog the entry comes from
// (sdk.ts:224-229 restores the saved model with modelRuntime.getModel; agent-session.ts:647 reads it the same way). A model only the remote catalog carries has
// no bundled record, so building it from the bundled catalog alone gave a model with no window and no prices.
func TestBuildModelCarriesTheCatalogWindowAndPrices(t *testing.T) {
	services := remoteCatalogServices(t)
	for _, tc := range []struct {
		name, spec         string
		window             int
		inputRate, outRate float64
	}{
		{"remote-only model with a dot in its id", "github-copilot/claude-fable-5.2", 1_000_000, 10, 50},
		{"remote-only model with a slash and a colon in its id", "openai/acme/spark:beta", 400_000, 1, 2},
		{"bundled model the remote catalog replaces", "github-copilot/claude-haiku-4.5", 123_456, 3, 4},
		{"bundled model", "github-copilot/claude-fable-5.1", 1_000_000, 10, 50},
		{"models.json model with a contextWindow", "custom-proxy/with-window", 64_000, 0, 0},
		{"models.json model without a contextWindow", "custom-proxy/no-window", 128_000, 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			model, err := BuildModel(tc.spec, services)
			if err != nil {
				t.Fatal(err)
			}
			if model.Capabilities.ContextWindow != tc.window {
				t.Errorf("window = %d, want %d", model.Capabilities.ContextWindow, tc.window)
			}
			if rates := model.CostRates(); rates.Input != tc.inputRate || rates.Output != tc.outRate {
				t.Errorf("rates = %v/%v, want %v/%v", rates.Input, rates.Output, tc.inputRate, tc.outRate)
			}
			if model.Provider == nil {
				t.Error("the model has no provider to stream through")
			}
			if listed := services.ModelRuntime().GetModel(model.ProviderMeta.ProviderID, model.ID); listed != nil {
				if listed.Capabilities.ContextWindow != model.Capabilities.ContextWindow || listed.CostRates().Input != model.CostRates().Input {
					t.Errorf("BuildModel differs from ModelRuntime.GetModel: window %d vs %d", model.Capabilities.ContextWindow, listed.Capabilities.ContextWindow)
				}
			}
		})
	}
}

// The owner's session: a Copilot model only the remote catalog carries, turns that report input 2, cacheRead 294000, cacheWrite 1400, output 860. A resumed Session
// restores the saved model with its 1,000,000 window, so the footer's usage is 29.6% of it and not 0.0%/0.
func TestResumedSessionKeepsTheWindowOfARemoteOnlyModel(t *testing.T) {
	services := remoteCatalogServices(t)
	if err := services.Auth().Set("github-copilot", ai.Credential{Type: ai.CredentialOAuth, Refresh: "github-access-token", Access: "tid=test;exp=9999999999;", Expires: time.Now().Add(time.Hour).UnixMilli()}); err != nil {
		t.Fatal(err)
	}
	if refreshed := services.ModelRuntime().Refresh(t.Context(), ai.ModelsRefreshOptions{AllowNetwork: new(false)}); refreshed.Aborted || len(refreshed.Errors) != 0 {
		t.Fatalf("refresh=%v", refreshed)
	}
	saved, err := BuildModel("github-copilot/claude-fable-5.2", services)
	if err != nil {
		t.Fatal(err)
	}
	source, err := NewSession(services, SessionOptions{Model: saved, SkipBuiltinTools: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := source.Inner().AppendMessage(agent.AgentMessage{User: &agent.UserMessage{Role: agent.RoleUser, Content: ai.UserContentBlocks{ai.TextContent{Text: "hello"}}}}); err != nil {
		t.Fatal(err)
	}
	usage := &ai.Usage{Input: 2, CacheRead: 294000, CacheWrite: 1400, Output: 860}
	if _, err := source.Inner().AppendMessage(agent.AgentMessage{Assistant: &agent.AssistantMessage{Role: agent.RoleAssistant, API: ai.APIAnthropicMessages, Provider: "github-copilot", ModelID: "claude-fable-5.2", Content: []ai.AssistantContentBlock{ai.TextContent{Text: "hi"}}, StopReason: ai.StopReasonStop, Usage: usage}}); err != nil {
		t.Fatal(err)
	}
	path := source.Path()
	if err := source.Close(); err != nil {
		t.Fatal(err)
	}
	for range source.Events() {
	}
	resumed, err := NewSession(services, SessionOptions{ResumePath: path, SkipBuiltinTools: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := resumed.Close(); err != nil {
			t.Error(err)
		}
		for range resumed.Events() {
		}
	})
	model := resumed.Model()
	if model == nil || model.ID != "claude-fable-5.2" {
		t.Fatalf("resumed model = %+v", model)
	}
	if model.Capabilities.ContextWindow != 1_000_000 {
		t.Fatalf("resumed window = %d, want 1000000", model.Capabilities.ContextWindow)
	}
	got := resumed.ContextUsage()
	if got == nil || got.Tokens == nil || got.Percent == nil || *got.Tokens != 296262 || got.ContextWindow != 1_000_000 {
		t.Fatalf("context usage = %s, want 296262 of 1000000", usageString(got))
	}
	if percent := *got.Percent; percent < 29.62 || percent > 29.63 {
		t.Fatalf("percent = %v, want 29.6262", percent)
	}
}
