package coding

// pi: packages/coding-agent/src/core/agent-session.ts

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// modelObjectRefreshServices is Services whose registry lists a model through each source a registry change can come from: an extension-registered
// provider (acme/chat-1), models.json (filed/chat-1) and a stored remote catalog (openai/acme/spark:beta), beside a provider no row changes
// (bystander/m). Each model's context window is 1000.
func modelObjectRefreshServices(t *testing.T) (*AgentSessionServices, string) {
	t.Helper()
	t.Setenv("PIG_HOME", t.TempDir())
	t.Setenv("PI_OFFLINE", "")
	t.Setenv("OPENAI_API_KEY", "openai-key")
	dir := t.TempDir()
	writeFiledModel(t, dir, 1000)
	writeRemoteModel(t, dir, 1000, 1)
	services, err := CreateAgentSessionServices(CreateAgentSessionServicesOptions{CWD: dir, AgentDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(services.Close)
	registerWindowModel(t, services, "acme", "chat-1", 1000)
	registerWindowModel(t, services, "bystander", "m", 1000)
	if refreshed := services.ModelRuntime().Refresh(t.Context(), ai.ModelsRefreshOptions{AllowNetwork: new(false)}); refreshed.Aborted || len(refreshed.Errors) != 0 {
		t.Fatalf("refresh=%v", refreshed)
	}
	return services, dir
}

func registerWindowModel(t *testing.T, services *AgentSessionServices, provider, id string, window int, more ...extension.ProviderModelConfig) {
	t.Helper()
	models := append([]extension.ProviderModelConfig{{ID: id, Name: id, Input: []string{"text"}, ContextWindow: window, MaxTokens: 100}}, more...)
	if err := services.Registry().RegisterExtensionProvider(provider, extension.ProviderConfig{BaseURL: "http://127.0.0.1:9/v1", API: ai.APIOpenAICompletions, APIKey: "k", Models: models}); err != nil {
		t.Fatal(err)
	}
}

func writeFiledModel(t *testing.T, dir string, window int) {
	t.Helper()
	config, err := json.Marshal(map[string]any{"providers": map[string]any{"filed": map[string]any{
		"baseUrl": "http://127.0.0.1:9/v1", "api": "openai-completions", "apiKey": "key", "models": []map[string]any{{"id": "chat-1", "contextWindow": window}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "models.json"), config, 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeRemoteModel(t *testing.T, dir string, window int, generation float64) {
	t.Helper()
	generatedAt := ai.GetBuiltinModelDataGeneratedAt()
	if generatedAt == nil {
		t.Fatal("the generated catalogs record no generation time")
	}
	record, err := json.Marshal(map[string]any{"id": "acme/spark:beta", "name": "Spark", "api": "openai-responses", "provider": "openai", "baseUrl": "https://api.openai.com/v1", "reasoning": false, "input": []string{"text"},
		"cost": map[string]any{"input": 1, "output": 2, "cacheRead": 0.1, "cacheWrite": 0}, "contextWindow": window, "maxTokens": 64000})
	if err != nil {
		t.Fatal(err)
	}
	entry := ai.ModelsStoreEntry{Models: mustStoredModels([]json.RawMessage{record}), CheckedAt: new(generation), LastModified: new(*generatedAt + generation)}
	if err := ai.NewFileModelsStore(filepath.Join(dir, "models-store.json")).Write(t.Context(), "openai", entry); err != nil {
		t.Fatal(err)
	}
}

// Pi replaces the Session's model with the registry's model of the same provider and id when that is another object (agent-session.ts:3501-3513
// _refreshCurrentModelFromRegistry: `!refreshedModel || refreshedModel === currentModel` keeps it). A registry model an extension edited in place stays
// the registry's object, so its edit lasts until the registry composes that model again; an object of an id the registry does not list is kept. The wire
// carries no object identity, so the Session remembers the registry's model of an object an extension set and takes the registry's when that changed.
//
// A model taken from the registry unchanged, by its object or by its name, follows every registry change as it did before the Session kept objects.
func TestRegistryChangeAndTheModelObjectOfTheSession(t *testing.T) {
	type change struct {
		name, provider, id string
		// apply gives the registry's model of provider/id a context window of 7777.
		apply func(t *testing.T, services *AgentSessionServices, dir string)
	}
	changes := []change{
		{"extension-re-registers-the-provider", "acme", "chat-1", func(t *testing.T, services *AgentSessionServices, _ string) {
			registerWindowModel(t, services, "acme", "chat-1", 7777)
		}},
		{"models-json-edit", "filed", "chat-1", func(t *testing.T, services *AgentSessionServices, dir string) {
			writeFiledModel(t, dir, 7777)
			services.Registry().ModelRegistry.Refresh()
		}},
		{"remote-catalog-refresh", "openai", "acme/spark:beta", func(t *testing.T, services *AgentSessionServices, dir string) {
			writeRemoteModel(t, dir, 7777, 2)
			if refreshed := services.ModelRuntime().Refresh(t.Context(), ai.ModelsRefreshOptions{AllowNetwork: new(false)}); refreshed.Aborted || len(refreshed.Errors) != 0 {
				t.Fatalf("refresh=%v", refreshed)
			}
		}},
	}
	unrelated := func(t *testing.T, services *AgentSessionServices) {
		registerWindowModel(t, services, "bystander", "m", 2222)
	}
	type object struct {
		name string
		// build is the model object the extension passes; afterUnrelated and afterChange are the Session model's context window after a registry change
		// that leaves the model's registry entry alone and after one that changes it.
		build                       func(t *testing.T, services *AgentSessionServices, c change) *ai.Model
		id                          func(c change) string
		afterUnrelated, afterChange int
	}
	listedID := func(c change) string { return c.id }
	objects := []object{
		{"registry-model-as-is", func(t *testing.T, services *AgentSessionServices, c change) *ai.Model {
			return editedModelObject(t, services.ModelRuntime().GetModel(c.provider, c.id), nil)
		}, listedID, 1000, 7777},
		{"named-model", func(t *testing.T, services *AgentSessionServices, c change) *ai.Model {
			// The string form of an SDK's set-model: the host builds the model of the name (resolveModel) and the SDK sets its object.
			built, err := BuildModel(c.provider+"/"+c.id, services)
			if err != nil {
				t.Fatal(err)
			}
			return editedModelObject(t, built, nil)
		}, listedID, 1000, 7777},
		{"registry-model-edited", func(t *testing.T, services *AgentSessionServices, c change) *ai.Model {
			return editedModelObject(t, services.ModelRuntime().GetModel(c.provider, c.id), func(object map[string]any) { object["contextWindow"] = 4321 })
		}, listedID, 4321, 7777},
		{"hand-built-with-a-listed-id", func(t *testing.T, _ *AgentSessionServices, c change) *ai.Model {
			return handBuiltModelObject(t, c.provider, c.id, ai.APIOpenAICompletions, "https://object.invalid/v1")
		}, listedID, 54321, 7777},
		{"hand-built-with-an-unlisted-id", func(t *testing.T, _ *AgentSessionServices, c change) *ai.Model {
			return handBuiltModelObject(t, c.provider, "handmade-9", ai.APIOpenAICompletions, "https://object.invalid/v1")
		}, func(change) string { return "handmade-9" }, 54321, 54321},
	}
	for _, c := range changes {
		for _, o := range objects {
			t.Run(c.name+"/"+o.name, func(t *testing.T) {
				services, dir := modelObjectRefreshServices(t)
				runtime := services.ModelRuntime()
				if runtime.GetModel(c.provider, c.id) == nil {
					t.Fatalf("the registry lists no %s/%s", c.provider, c.id)
				}
				session, err := NewSession(services, SessionOptions{Model: runtime.GetModel("bystander", "m"), NoSession: true, SkipBuiltinTools: true})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = session.Close() })
				if switched, err := session.ExtensionSetModel(t.Context(), runtime.BindModelObject(o.build(t, services, c))); err != nil || !switched {
					t.Fatalf("setModel = %t, %v", switched, err)
				}
				window := func() int {
					model := session.Model()
					if model.ProviderMeta.ProviderID != c.provider || model.ID != o.id(c) {
						t.Fatalf("the Session's model is %s/%s, want %s/%s", model.ProviderMeta.ProviderID, model.ID, c.provider, o.id(c))
					}
					return model.Capabilities.ContextWindow
				}
				if got := window(); got != o.afterUnrelated {
					t.Fatalf("after setModel the Session's model has window %d, want %d", got, o.afterUnrelated)
				}
				unrelated(t, services)
				if got := window(); got != o.afterUnrelated {
					t.Errorf("after a registry change that leaves %s/%s alone the Session's model has window %d, want %d", c.provider, c.id, got, o.afterUnrelated)
				}
				c.apply(t, services, dir)
				if got := runtime.GetModel(c.provider, c.id).Capabilities.ContextWindow; got != 7777 {
					t.Fatalf("the registry's %s/%s has window %d after the change, want 7777", c.provider, c.id, got)
				}
				if got := window(); got != o.afterChange {
					t.Errorf("after the registry's %s/%s changed the Session's model has window %d, want %d", c.provider, c.id, got, o.afterChange)
				}
			})
		}
	}
	// The registry starts to list the id of an object built by hand: the registry's model is another object than the Session's, and Pi takes it.
	t.Run("hand-built-id-becomes-listed", func(t *testing.T) {
		services, _ := modelObjectRefreshServices(t)
		runtime := services.ModelRuntime()
		session, err := NewSession(services, SessionOptions{Model: runtime.GetModel("bystander", "m"), NoSession: true, SkipBuiltinTools: true})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = session.Close() })
		if switched, err := session.ExtensionSetModel(t.Context(), runtime.BindModelObject(handBuiltModelObject(t, "acme", "handmade-9", ai.APIOpenAICompletions, "https://object.invalid/v1"))); err != nil || !switched {
			t.Fatalf("setModel = %t, %v", switched, err)
		}
		registerWindowModel(t, services, "acme", "chat-1", 1000, extension.ProviderModelConfig{ID: "handmade-9", Name: "Listed now", Input: []string{"text"}, ContextWindow: 8888, MaxTokens: 100})
		if got := session.Model(); got.ID != "handmade-9" || got.Capabilities.ContextWindow != 8888 {
			t.Errorf("the Session's model is %s window=%d, want the registry's handmade-9 window=8888", got.ID, got.Capabilities.ContextWindow)
		}
	})
}

// Pi's extension setModel answers false, and changes nothing, when hasConfiguredAuth(model.provider) is false, and otherwise switches to the object
// and answers true (agent-session.ts:3583-3587); the switch records the provider and the id and nothing else of the object (:2652 appendModelChange).
func TestExtensionSetModelObjectAuthGate(t *testing.T) {
	t.Setenv("OLLAMA_HOST", "http://127.0.0.1:9")
	services, _ := modelObjectRefreshServices(t)
	t.Setenv("ANTHROPIC_API_KEY", "")
	runtime := services.ModelRuntime()
	session, err := NewSession(services, SessionOptions{Model: runtime.GetModel("bystander", "m"), NoSession: true, SkipBuiltinTools: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	modelChanges := func() []string {
		var changes []string
		for _, entry := range session.Inner().GetEntries() {
			if raw, err := json.Marshal(entry); err == nil && strings.Contains(string(raw), `"model_change"`) {
				changes = append(changes, string(raw))
			}
		}
		return changes
	}
	for _, row := range []struct {
		name, provider string
		want           bool
	}{
		{"provider-with-configured-auth", "acme", true},
		{"built-in-provider-without-credentials", "anthropic", false},
		{"unknown-provider", "nonesuch", false},
		// No provider is configured by its name: Pi's setModel action answers false for any provider outside the configured set (agent-session.ts:3584), and its prompt check refuses the same provider (agent-session.ts:2247-2259, `No API key found for ollama.`, auth-guidance.ts:22-25), with OLLAMA_HOST set as here.
		{"provider-named-ollama-without-credentials", "ollama", false},
	} {
		before, entries := session.Model(), len(modelChanges())
		object := handBuiltModelObject(t, row.provider, "handmade-9", ai.APIOpenAICompletions, "https://object.invalid/v1")
		switched, err := session.ExtensionSetModel(t.Context(), runtime.BindModelObject(object))
		if err != nil || switched != row.want {
			t.Errorf("%s: setModel = %t, %v, want %t", row.name, switched, err, row.want)
			continue
		}
		after := modelChanges()
		if !row.want {
			if session.Model() != before || len(after) != entries {
				t.Errorf("%s: a refused setModel changed the Session: model %s/%s, %d model_change entries (was %d)", row.name, session.Model().ProviderMeta.ProviderID, session.Model().ID, len(after), entries)
			}
			continue
		}
		if got := session.Model(); got.ProviderMeta.ProviderID != row.provider || got.ID != "handmade-9" || got.Capabilities.ContextWindow != 54321 {
			t.Errorf("%s: the Session's model is %s/%s window=%d, want the object", row.name, got.ProviderMeta.ProviderID, got.ID, got.Capabilities.ContextWindow)
		}
		if len(after) != entries+1 {
			t.Errorf("%s: %d model_change entries, want %d", row.name, len(after), entries+1)
			continue
		}
		if last := after[len(after)-1]; !strings.Contains(last, `"`+row.provider+`"`) || !strings.Contains(last, `"handmade-9"`) || strings.Contains(last, "54321") || strings.Contains(last, "object.invalid") {
			t.Errorf("%s: the model_change entry is %s, want the provider and the id and no other member of the object", row.name, last)
		}
	}
}

// A session file records a model change as provider and id (agent-session.ts:2652), so a resumed session whose saved model no catalog describes restores
// what it restored before the Session kept objects: the same model whether the saved model had been an object an extension built by hand or the model
// the host built for that name.
func TestResumeAfterAHandBuiltModelObjectRestoresByProviderAndID(t *testing.T) {
	services, _ := modelObjectRefreshServices(t)
	runtime := services.ModelRuntime()
	resume := func(set func(session *Session)) *ai.Model {
		t.Helper()
		source, err := NewSession(services, SessionOptions{Model: runtime.GetModel("bystander", "m"), SkipBuiltinTools: true})
		if err != nil {
			t.Fatal(err)
		}
		set(source)
		if _, err := source.Inner().AppendMessage(agent.AgentMessage{User: &agent.UserMessage{Role: agent.RoleUser, Content: ai.UserContentBlocks{ai.TextContent{Text: "hello"}}}}); err != nil {
			t.Fatal(err)
		}
		if _, err := source.Inner().AppendMessage(agent.AgentMessage{Assistant: &agent.AssistantMessage{Role: agent.RoleAssistant, API: ai.APIOpenAICompletions, Provider: "acme", ModelID: "handmade-9", Content: []ai.AssistantContentBlock{ai.TextContent{Text: "hi"}}, StopReason: ai.StopReasonStop}}); err != nil {
			t.Fatal(err)
		}
		path := source.Path()
		if data, err := os.ReadFile(path); err != nil || strings.Contains(string(data), "54321") || strings.Contains(string(data), "object.invalid") {
			t.Fatalf("the session file carries members of the model object (%v): %s", err, data)
		}
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
			_ = resumed.Close()
			for range resumed.Events() {
			}
		})
		return resumed.Model()
	}
	fromObject := resume(func(session *Session) {
		if switched, err := session.ExtensionSetModel(t.Context(), runtime.BindModelObject(handBuiltModelObject(t, "acme", "handmade-9", ai.APIOpenAIResponses, "https://object.invalid/v1"))); err != nil || !switched {
			t.Fatalf("setModel = %t, %v", switched, err)
		}
	})
	fromName := resume(func(session *Session) {
		built, err := BuildFallbackModel(&ai.Model{ID: "handmade-9", DisplayName: "handmade-9", ProviderMeta: ai.ProviderMetadata{ProviderID: "acme", API: ai.APIOpenAICompletions, BaseURL: "https://acme.example/v1"}}, services)
		if err != nil {
			t.Fatal(err)
		}
		if err := session.SetModel(built); err != nil {
			t.Fatal(err)
		}
	})
	if fromObject == nil || fromName == nil {
		t.Fatalf("resumed models: from the object %v, from the name %v", fromObject, fromName)
	}
	if !sameModelObject(fromObject, fromName) {
		a, _ := extension.ModelObject(fromObject)
		b, _ := extension.ModelObject(fromName)
		t.Errorf("the session resumed to %s after the object and to %s after the name, want the same model", a, b)
	}
	if fromObject.Capabilities.ContextWindow == 54321 || fromObject.ProviderMeta.BaseURL == "https://object.invalid/v1" {
		t.Errorf("the resumed model is %s window=%d base=%s: it carries members of the object, which the session file does not record", fromObject.ID, fromObject.Capabilities.ContextWindow, fromObject.ProviderMeta.BaseURL)
	}
}
