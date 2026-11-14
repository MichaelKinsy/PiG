package coding

// pi: packages/coding-agent/src/core/model-runtime.ts

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// decodeModelObject is the model a setModel call's object decodes to (subprocess.handleSetModel): no backend, every member the object carries.
func decodeModelObject(t *testing.T, raw []byte) *ai.Model {
	t.Helper()
	decoded, known, err := ai.DecodeStoredModel(raw)
	model, _ := decoded.(*ai.Model)
	if err != nil || !known || model == nil {
		t.Fatalf("decode model object %s: known=%t err=%v", raw, known, err)
	}
	return model
}

// editedModelObject is the object of model after edit, as an extension that took the model from the registry and changed members passes it to setModel.
func editedModelObject(t *testing.T, model *ai.Model, edit func(object map[string]any)) *ai.Model {
	t.Helper()
	raw, err := extension.ModelObject(model)
	if err != nil {
		t.Fatal(err)
	}
	var object map[string]any
	if err := json.Unmarshal(raw, &object); err != nil {
		t.Fatal(err)
	}
	if edit != nil {
		edit(object)
	}
	if raw, err = json.Marshal(object); err != nil {
		t.Fatal(err)
	}
	return decodeModelObject(t, raw)
}

// handBuiltModelObject is a model object an extension wrote out by hand for a provider the host knows.
func handBuiltModelObject(t *testing.T, provider, id string, api ai.API, baseURL string) *ai.Model {
	t.Helper()
	compat := ""
	if api == ai.APIOpenAICompletions {
		compat = `,"compat":{"maxTokensField":"max_tokens"}`
	}
	return decodeModelObject(t, fmt.Appendf(nil, `{"id":%q,"name":"Hand built","api":%q,"provider":%q,"baseUrl":%q,"reasoning":false,"input":["text"],"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0},"contextWindow":54321,"maxTokens":321%s}`, id, api, provider, baseURL, compat))
}

type modelRequest struct {
	path          string
	authorization string
	apiKey        string
	body          map[string]any
}

// modelRequestServer records each request and answers 400, which ends the model request after the provider sent it.
func modelRequestServer(t *testing.T) (*httptest.Server, func() []modelRequest) {
	t.Helper()
	var mu sync.Mutex
	var seen []modelRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		request := modelRequest{path: r.URL.Path, authorization: r.Header.Get("Authorization"), apiKey: r.Header.Get("X-Api-Key")}
		_ = json.NewDecoder(r.Body).Decode(&request.body)
		mu.Lock()
		seen = append(seen, request)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"fixture request captured"}}`))
	}))
	t.Cleanup(server.Close)
	return server, func() []modelRequest {
		mu.Lock()
		defer mu.Unlock()
		return append([]modelRequest(nil), seen...)
	}
}

// requestShapeOf names what a request of api must look like for a model whose maxTokens is 321 (and, for openai-completions, whose compat names max_tokens).
func requestShapeOf(api ai.API) (pathSuffix, maxTokensMember string) {
	switch api {
	case ai.APIOpenAICompletions:
		return "/chat/completions", "max_tokens"
	case ai.APIOpenAIResponses:
		return "/responses", "max_output_tokens"
	default:
		return "/messages", "max_tokens"
	}
}

// Pi's request uses the model object the Session holds: prepareRequest requires only that the object's provider exists, resolves that provider's auth,
// and sends the object on (model-runtime.ts:667-689); setModel made the object the Session's model (agent-session.ts:2644-2652). So a registry model an
// extension edited and a model it built by hand reach their own endpoint, with their own id, API, output limit and compat, and with the provider's
// credentials, whatever the shape of the provider. PiG replaced the model with the registry's by provider and id at each request, and rebuilt it from
// the registry entry when the registry resolved no auth.
func TestRequestUsesTheModelObjectOfTheSession(t *testing.T) {
	type shape struct {
		name     string
		provider string
		listed   string   // the model the registry lists for the provider; "" when it lists none
		apis     []ai.API // the API kinds the provider is registered under, each a run
		prepare  func(t *testing.T, dir, registryURL string, api ai.API)
		register func(t *testing.T, services *AgentSessionServices, registryURL string, api ai.API)
		// credential is the header and value every request of the provider carries.
		credentialHeader, credential string
		// refused marks a provider without configured auth: setModel answers false and no request is sent.
		refused bool
	}
	extensionProvider := func(id string, models []extension.ProviderModelConfig) func(*testing.T, *AgentSessionServices, string, ai.API) {
		return func(t *testing.T, services *AgentSessionServices, registryURL string, api ai.API) {
			if err := services.Registry().RegisterExtensionProvider(id, extension.ProviderConfig{BaseURL: registryURL, API: api, APIKey: "registry-key", Models: models}); err != nil {
				t.Fatal(err)
			}
		}
	}
	openAIKinds := []ai.API{ai.APIOpenAICompletions, ai.APIOpenAIResponses}
	shapes := []shape{
		{name: "api-key", provider: "anthropic", listed: "claude-haiku-4-5", apis: []ai.API{ai.APIAnthropicMessages},
			prepare:          func(t *testing.T, _, _ string, _ ai.API) { t.Setenv("ANTHROPIC_API_KEY", "env-key") },
			credentialHeader: "X-Api-Key", credential: "env-key"},
		{name: "oauth", provider: "anthropic", listed: "claude-haiku-4-5", apis: []ai.API{ai.APIAnthropicMessages},
			prepare: func(t *testing.T, _, _ string, _ ai.API) { t.Setenv("ANTHROPIC_API_KEY", "") },
			register: func(t *testing.T, services *AgentSessionServices, _ string, _ ai.API) {
				if err := services.Auth().Set("anthropic", ai.Credential{Type: ai.CredentialOAuth, Access: "sk-ant-oat01-access", Refresh: "refresh", Expires: time.Now().Add(time.Hour).UnixMilli()}); err != nil {
					t.Fatal(err)
				}
			},
			credentialHeader: "Authorization", credential: "Bearer sk-ant-oat01-access"},
		{name: "custom-base-url-extension", provider: "acme", listed: "chat-1", apis: openAIKinds,
			register:         extensionProvider("acme", []extension.ProviderModelConfig{{ID: "chat-1", Name: "Chat 1", Input: []string{"text"}, ContextWindow: 5555, MaxTokens: 100}}),
			credentialHeader: "Authorization", credential: "Bearer registry-key"},
		{name: "custom-base-url-models-json", provider: "filed", listed: "chat-1", apis: openAIKinds,
			prepare: func(t *testing.T, dir, registryURL string, api ai.API) {
				config, err := json.Marshal(map[string]any{"providers": map[string]any{"filed": map[string]any{
					"baseUrl": registryURL, "api": api, "apiKey": "registry-key", "models": []map[string]any{{"id": "chat-1", "contextWindow": 5555, "maxTokens": 100}},
				}}})
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, "models.json"), config, 0o600); err != nil {
					t.Fatal(err)
				}
			},
			credentialHeader: "Authorization", credential: "Bearer registry-key"},
		{name: "provider-without-models", provider: "acme-empty", apis: openAIKinds,
			register:         extensionProvider("acme-empty", nil),
			credentialHeader: "Authorization", credential: "Bearer registry-key"},
		// A provider without configured auth gets no request, whatever its name: the setModel action answers false (agent-session.ts:3584) and a prompt is refused with `No API key found for ollama.` (agent-session.ts:2247-2259, auth-guidance.ts:22-25). OLLAMA_HOST configures nothing in Pi.
		{name: "no-auth-provider", provider: "ollama", apis: openAIKinds, refused: true,
			prepare: func(t *testing.T, _, registryURL string, _ ai.API) { t.Setenv("OLLAMA_HOST", registryURL) }},
	}
	for _, shape := range shapes {
		for _, api := range shape.apis {
			for _, object := range []string{"registry-model-edited", "hand-built"} {
				if object == "registry-model-edited" && shape.listed == "" {
					continue
				}
				t.Run(shape.name+"/"+string(api)+"/"+object, func(t *testing.T) {
					registryServer, registryRequests := modelRequestServer(t)
					objectServer, objectRequests := modelRequestServer(t)
					dir := t.TempDir()
					t.Setenv("PIG_HOME", t.TempDir())
					if shape.prepare != nil {
						shape.prepare(t, dir, registryServer.URL, api)
					}
					services, err := CreateAgentSessionServices(CreateAgentSessionServicesOptions{CWD: dir, AgentDir: dir})
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(services.Close)
					if shape.register != nil {
						shape.register(t, services, registryServer.URL, api)
					}
					extensionProvider("start", []extension.ProviderModelConfig{{ID: "m", Input: []string{"text"}}})(t, services, registryServer.URL, ai.APIOpenAICompletions)
					runtime := services.ModelRuntime()
					if refreshed := runtime.Refresh(t.Context(), ai.ModelsRefreshOptions{AllowNetwork: new(false)}); refreshed.Aborted {
						t.Fatalf("refresh=%v", refreshed)
					}
					session, err := NewSession(services, SessionOptions{Model: runtime.GetModel("start", "m"), NoSession: true, SkipBuiltinTools: true})
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { _ = session.Close() })

					var model *ai.Model
					wantID, wantAPI := shape.listed, api
					if object == "hand-built" {
						wantID = "handmade-9"
						model = handBuiltModelObject(t, shape.provider, wantID, api, objectServer.URL)
					} else {
						listed := runtime.GetModel(shape.provider, shape.listed)
						if listed == nil {
							t.Fatalf("the registry lists no %s/%s", shape.provider, shape.listed)
						}
						model = editedModelObject(t, listed, func(object map[string]any) {
							object["baseUrl"], object["contextWindow"], object["maxTokens"] = objectServer.URL, 54321, 321
							if api == ai.APIOpenAICompletions {
								object["compat"] = map[string]any{"maxTokensField": "max_tokens"}
							}
						})
					}
					if shape.refused {
						if switched, err := session.ExtensionSetModel(t.Context(), runtime.BindModelObject(model)); err != nil || switched {
							t.Fatalf("setModel = %t, %v, want false for a provider without configured auth", switched, err)
						}
						if got := session.Model(); got.ProviderMeta.ProviderID != "start" {
							t.Errorf("the refused setModel left the Session on %s/%s", got.ProviderMeta.ProviderID, got.ID)
						}
						if sent := len(registryRequests()) + len(objectRequests()); sent != 0 {
							t.Errorf("%d request(s) were sent for a provider without configured auth", sent)
						}
						return
					}
					if switched, err := session.ExtensionSetModel(t.Context(), runtime.BindModelObject(model)); err != nil || !switched {
						t.Fatalf("setModel = %t, %v", switched, err)
					}
					if got := session.Model(); got.ID != wantID || got.Capabilities.ContextWindow != 54321 || got.ProviderMeta.BaseURL != objectServer.URL {
						t.Fatalf("the Session's model is %s window=%d base=%s, want the object %s window=54321 base=%s", got.ID, got.Capabilities.ContextWindow, got.ProviderMeta.BaseURL, wantID, objectServer.URL)
					}
					message := session.ModelRuntime().CompleteSimple(t.Context(), session.Model(), ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("hello")}}}, ai.StreamOptions{})
					if message.StopReason != ai.StopReasonError || !strings.Contains(message.ErrorMessage, "fixture request captured") {
						t.Fatalf("the request did not reach the fixture: %+v", message)
					}
					if seen := registryRequests(); len(seen) != 0 {
						t.Errorf("the provider's registered endpoint received %d request(s), want none: the object names its own endpoint (%s)", len(seen), seen[0].path)
					}
					seen := objectRequests()
					if len(seen) != 1 {
						t.Fatalf("the object's endpoint received %d requests, want 1", len(seen))
					}
					request := seen[0]
					pathSuffix, maxTokensMember := requestShapeOf(wantAPI)
					if !strings.HasSuffix(request.path, pathSuffix) {
						t.Errorf("request path %s, want the %s API's %s", request.path, wantAPI, pathSuffix)
					}
					if request.body["model"] != wantID {
						t.Errorf("request model %v, want %s", request.body["model"], wantID)
					}
					if request.body[maxTokensMember] != float64(321) {
						t.Errorf("request %s = %v, want the object's maxTokens 321 (body %v)", maxTokensMember, request.body[maxTokensMember], request.body)
					}
					if wantAPI == ai.APIOpenAICompletions {
						if _, sent := request.body["max_completion_tokens"]; sent {
							t.Errorf("request carries max_completion_tokens, but the object's compat names max_tokens (body %v)", request.body)
						}
					}
					header := request.authorization
					if shape.credentialHeader == "X-Api-Key" {
						header = request.apiKey
					}
					if header != shape.credential {
						t.Errorf("request %s = %q, want the provider's credential %q", shape.credentialHeader, header, shape.credential)
					}
				})
			}
		}
	}
}

// A provider object's stream function receives the model object the Session holds: the edited registry model and the one built by hand for an id the
// provider does not list.
func TestProviderObjectStreamReceivesTheModelObjectOfTheSession(t *testing.T) {
	for _, object := range []string{"registry-model-edited", "hand-built"} {
		t.Run(object, func(t *testing.T) {
			services := newRegistrationServices(t)
			var received []*ai.Model
			stream := func(_ context.Context, model *ai.Model, _ ai.TranscriptContext, _ ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
				received = append(received, model)
				events := ai.NewAssistantMessageEventStream()
				events.End(&ai.AssistantMessage{StopReason: ai.StopReasonStop})
				return events, nil
			}
			listed := &ai.Model{ID: "m1", DisplayName: "M1", Input: []string{"text"}, ProviderMeta: ai.ProviderMetadata{ProviderID: "native", API: ai.APIOpenAICompletions, BaseURL: "https://registry.invalid/v1"}, Capabilities: ai.ModelCapabilities{ContextWindow: 128, MaxOutputTokens: 16}}
			if err := services.ModelRuntime().RegisterNativeProvider(&ai.ModelsProvider{ID: "native", Name: "native", GetModels: func() ([]*ai.Model, error) { return []*ai.Model{listed}, nil },
				Auth: ai.ProviderAuth{APIKey: &ai.APIKeyAuth{Resolve: func(context.Context, ai.APIKeyAuthInput) (*ai.AuthResult, error) {
					return &ai.AuthResult{Auth: ai.ModelAuth{APIKey: "test"}}, nil
				}}},
				Stream: stream, StreamSimple: stream,
			}); err != nil {
				t.Fatal(err)
			}
			runtime := services.ModelRuntime()
			if refreshed := runtime.Refresh(t.Context(), ai.ModelsRefreshOptions{AllowNetwork: new(false)}); refreshed.Aborted {
				t.Fatalf("refresh=%v", refreshed)
			}
			session, err := NewSession(services, SessionOptions{Model: runtime.GetModel("native", "m1"), NoSession: true, SkipBuiltinTools: true})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = session.Close() })
			wantID := "m1"
			var model *ai.Model
			if object == "hand-built" {
				wantID = "m2"
				model = handBuiltModelObject(t, "native", wantID, ai.APIOpenAIResponses, "https://object.invalid/v1")
			} else {
				model = editedModelObject(t, runtime.GetModel("native", "m1"), func(object map[string]any) {
					object["baseUrl"], object["contextWindow"], object["maxTokens"] = "https://object.invalid/v1", 54321, 321
				})
			}
			if switched, err := session.ExtensionSetModel(t.Context(), runtime.BindModelObject(model)); err != nil || !switched {
				t.Fatalf("setModel = %t, %v", switched, err)
			}
			if message := session.ModelRuntime().CompleteSimple(t.Context(), session.Model(), ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("hello")}}}, ai.StreamOptions{}); message.StopReason != ai.StopReasonStop {
				t.Fatalf("request failed: %+v", message)
			}
			if len(received) != 1 {
				t.Fatalf("the provider's stream function ran %d times, want 1", len(received))
			}
			got := received[0]
			if got.ID != wantID || got.ProviderMeta.BaseURL != "https://object.invalid/v1" || got.Capabilities.ContextWindow != 54321 || got.Capabilities.MaxOutputTokens != 321 {
				t.Errorf("the stream function received %s base=%s window=%d max=%d, want the object %s base=https://object.invalid/v1 window=54321 max=321", got.ID, got.ProviderMeta.BaseURL, got.Capabilities.ContextWindow, got.Capabilities.MaxOutputTokens, wantID)
			}
		})
	}
}
