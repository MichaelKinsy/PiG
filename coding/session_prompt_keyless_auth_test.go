package coding

import (
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

// keylessAuthFixture is a Session over a scratch agent directory whose models.json providers all point at one local listener, which counts the
// requests that reach it.
type keylessAuthFixture struct {
	services *AgentSessionServices
	runtime  *ModelRuntime
	url      string
	requests *atomic.Int64
}

// newKeylessAuthFixture writes providers (models.json provider bodies without a baseUrl) and creates the services. OLLAMA_HOST names the
// listener when ollamaHost is set and is empty otherwise.
func newKeylessAuthFixture(t *testing.T, providers map[string]map[string]any, ollamaHost bool) keylessAuthFixture {
	t.Helper()
	clearProviderEnvKeys(t)
	requests := new(atomic.Int64)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		http.Error(w, `{"error":{"message":"fixture request captured"}}`, http.StatusBadRequest)
	}))
	t.Cleanup(server.Close)
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("PIG_HOME", t.TempDir())
	t.Setenv("OLLAMA_HOST", "")
	if ollamaHost {
		t.Setenv("OLLAMA_HOST", server.URL)
	}
	if len(providers) > 0 {
		for _, provider := range providers {
			provider["baseUrl"] = server.URL
		}
		config, err := json.Marshal(map[string]any{"providers": providers})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "models.json"), config, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	services, err := CreateAgentSessionServices(CreateAgentSessionServicesOptions{CWD: dir, AgentDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(services.Close)
	return keylessAuthFixture{services: services, runtime: services.ModelRuntime(), url: server.URL, requests: requests}
}

func (f keylessAuthFixture) session(t *testing.T, model *ai.Model) *Session {
	t.Helper()
	if refreshed := f.runtime.Refresh(t.Context(), ai.ModelsRefreshOptions{AllowNetwork: new(false)}); refreshed.Aborted {
		t.Fatalf("refresh=%v", refreshed)
	}
	session, err := NewSession(f.services, SessionOptions{Model: model, NoSession: true, SkipBuiltinTools: true})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	events := session.Events()
	go func() {
		defer close(done)
		for event := range events {
			AcknowledgeEvent(event)
		}
	}()
	t.Cleanup(func() {
		if err := session.Close(); err != nil {
			t.Error(err)
		}
		<-done
	})
	return session
}

// Pi has one prompt rule for every provider, in every mode (agent-session.ts:2247-2259): the provider is in the published configured set, or a
// live checkAuth answers; else the prompt is refused with `No API key found for <provider>.` (auth-guidance.ts:22-25) and nothing is sent. No
// provider is exempt by its name: a models.json provider without an apiKey has an API-key method whose check answers undefined
// (provider-composer.ts:422-457), whatever it is called and whether or not OLLAMA_HOST is set, and Pi's own Ollama recipe writes a dummy key
// (docs/models.md:53-65). RUN against Pi 1.5.2 in print, RPC and interactive mode for each of these shapes (2026-11-13).
func TestValidatePromptModelAuthHasOneRuleForEveryProviderShape(t *testing.T) {
	completions := func(extra map[string]any) map[string]any {
		provider := map[string]any{"api": "openai-completions", "models": []map[string]any{{"id": "m1"}}}
		maps.Copy(provider, extra)
		return provider
	}
	firstOf := func(provider string) func(*testing.T, *ModelRuntime) *ai.Model {
		return func(t *testing.T, runtime *ModelRuntime) *ai.Model {
			models := runtime.GetModels(provider)
			if len(models) == 0 {
				t.Fatalf("no %s model in the catalog", provider)
			}
			return models[0]
		}
	}
	listed := func(provider string) func(*testing.T, *ModelRuntime) *ai.Model {
		return func(t *testing.T, runtime *ModelRuntime) *ai.Model {
			model := runtime.GetModel(provider, "m1")
			if model == nil {
				t.Fatalf("the registry lists no %s/m1", provider)
			}
			return model
		}
	}
	for _, tc := range []struct {
		name       string
		providers  map[string]map[string]any
		env        map[string]string
		stored     map[string]ai.Credential
		ollamaHost bool
		model      func(*testing.T, *ModelRuntime) *ai.Model
		// refused is the provider the refusal names; "" admits the prompt.
		refused string
	}{
		{name: "built-in provider with an environment key", env: map[string]string{"GROQ_API_KEY": "env-key"}, model: firstOf("groq")},
		{name: "built-in provider without a key", model: firstOf("groq"), refused: "groq"},
		{name: "built-in provider with a stored OAuth credential", model: firstOf("anthropic"),
			stored: map[string]ai.Credential{"anthropic": {Type: ai.CredentialOAuth, Access: "sk-ant-oat01-access", Refresh: "refresh", Expires: time.Now().Add(time.Hour).UnixMilli()}}},
		{name: "models.json provider with a custom base URL and a key", providers: map[string]map[string]any{"localkey": completions(map[string]any{"apiKey": "dummy"})}, model: listed("localkey")},
		{name: "models.json provider named ollama with the dummy key of the docs", providers: map[string]map[string]any{"ollama": completions(map[string]any{"apiKey": "ollama"})}, model: listed("ollama")},
		{name: "models.json provider named ollama without a key", providers: map[string]map[string]any{"ollama": completions(nil)}, model: listed("ollama"), refused: "ollama"},
		{name: "models.json provider named ollama without a key, OLLAMA_HOST set", providers: map[string]map[string]any{"ollama": completions(nil)}, ollamaHost: true, model: listed("ollama"), refused: "ollama"},
		{name: "models.json provider of another name without a key", providers: map[string]map[string]any{"mylocal": completions(nil)}, model: listed("mylocal"), refused: "mylocal"},
		{name: "models.json provider with headers and no key", providers: map[string]map[string]any{"hdronly": completions(map[string]any{"headers": map[string]string{"X-Local": "1"}})}, model: listed("hdronly"), refused: "hdronly"},
		// A Session created without a model holds Pi's Agent DEFAULT_MODEL, whose provider is "unknown" (auth-guidance.ts:4, 23).
		{name: "no model selected at start", model: func(*testing.T, *ModelRuntime) *ai.Model { return nil }, refused: "unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newKeylessAuthFixture(t, tc.providers, tc.ollamaHost)
			for name, value := range tc.env {
				t.Setenv(name, value)
			}
			for provider, credential := range tc.stored {
				if err := f.services.Auth().Set(provider, credential); err != nil {
					t.Fatal(err)
				}
			}
			session := f.session(t, tc.model(t, f.runtime))
			err := session.ValidatePromptModelAuth(t.Context())
			if tc.refused == "" {
				if err != nil {
					t.Fatalf("ValidatePromptModelAuth = %v, Pi admits the prompt", err)
				}
				return
			}
			want := icodingagent.FormatNoAPIKeyFoundMessage(tc.refused)
			if err == nil || err.Error() != want {
				t.Errorf("ValidatePromptModelAuth = %v, want Pi's %q", err, want)
			}
			if err := session.Prompt(t.Context(), "hi"); err == nil || err.Error() != want {
				t.Errorf("Prompt = %v, want Pi's %q", err, want)
			}
			if sent := f.requests.Load(); sent != 0 {
				t.Errorf("the refused prompt sent %d request(s) to the provider", sent)
			}
		})
	}
}

// The other gates read the same configured set, and the name "ollama" opens none of them. A model object an extension builds by hand for a
// provider nothing registers is refused at setModel (agent-session.ts:2646 `No API key for ${model.provider}/${model.id}`), at the extension's
// setModel action (agent-session.ts:3584 answers false) and at the request (agent-session.ts:574-603: no auth, no request).
func TestKeylessProviderNameOpensNoModelGate(t *testing.T) {
	for _, provider := range []string{"ollama", "nonesuch"} {
		t.Run(provider, func(t *testing.T) {
			f := newKeylessAuthFixture(t, map[string]map[string]any{"start": {"api": "openai-completions", "apiKey": "start-key", "models": []map[string]any{{"id": "m"}}}}, true)
			session := f.session(t, f.runtime.GetModel("start", "m"))
			object := handBuiltModelObject(t, provider, "handmade-9", ai.APIOpenAICompletions, f.url)
			if err := session.CheckModelAuth(object); err == nil || err.Error() != "No API key for "+provider+"/handmade-9" {
				t.Errorf("setModel's auth check = %v, want Pi's `No API key for %s/handmade-9`", err, provider)
			}
			bound := f.runtime.BindModelObject(object)
			if switched, err := session.ExtensionSetModel(t.Context(), bound); err != nil || switched {
				t.Errorf("an extension's setModel = %t, %v, want false", switched, err)
			}
			message := f.runtime.CompleteSimple(t.Context(), bound, ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("hello")}}}, ai.StreamOptions{})
			if message.StopReason != ai.StopReasonError {
				t.Errorf("a request for the object ended with %s, want an error: %+v", message.StopReason, message)
			}
			if sent := f.requests.Load(); sent != 0 {
				t.Errorf("%d request(s) reached the listener for a provider without credentials", sent)
			}
		})
	}
}
