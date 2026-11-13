package coding

// pi: packages/coding-agent/src/core/provider-composer.ts (mergeCompat 124-146, model overrides 199-208, modelFromJson 246-249,
// extensionModelFromDefinition 299), packages/coding-agent/src/core/model-config.ts (185, 193: JSON.parse and a check, no copy) and
// packages/ai/src/api/openai-completions.ts (897-910 chat-template values, 979-980 provider, 994-997 sampling parameters).

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/orderedjson"
)

// memberOrderRequest is the body of the one request a model of services sends for a prompt, as an ordered object.
func memberOrderRequest(t *testing.T, server *httptest.Server, bodies <-chan []byte, services *AgentSessionServices, spec string, options ai.StreamOptions) *orderedjson.Object {
	t.Helper()
	model, err := BuildModel(spec, services)
	if err != nil {
		t.Fatal(err)
	}
	message := services.ModelRuntime().CompleteSimple(t.Context(), model, ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("hello")}}}, options)
	if message.StopReason != ai.StopReasonError {
		t.Fatalf("fixture error response = %+v", message)
	}
	select {
	case body := <-bodies:
		object, err := orderedjson.Parse(body)
		if err != nil {
			t.Fatalf("request body %s: %v", body, err)
		}
		return object
	default:
		t.Fatalf("no request reached %s: %+v", server.URL, message)
		return nil
	}
}

// memberOrderServer answers every request with an error after handing its body on.
func memberOrderServer(t *testing.T) (*httptest.Server, <-chan []byte) {
	t.Helper()
	bodies := make(chan []byte, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		bodies <- body
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"fixture request captured"}}`))
	}))
	t.Cleanup(server.Close)
	return server, bodies
}

// modelsJSONServices is the services of an agent directory whose models.json is text, byte for byte.
func modelsJSONServices(t *testing.T, text string) *AgentSessionServices {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "models.json"), []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	services, err := CreateAgentSessionServices(CreateAgentSessionServicesOptions{CWD: dir, AgentDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(services.Close)
	if message := services.Registry().GetError(); message != "" {
		t.Fatal(message)
	}
	return services
}

// samplingMembers is the request's members that are among names, in the request's order.
func samplingMembers(request *orderedjson.Object, names ...string) []string {
	var members []string
	for _, key := range request.Keys() {
		if slices.Contains(names, key) {
			members = append(members, key)
		}
	}
	return members
}

func requireMember(t *testing.T, request *orderedjson.Object, member, want string) {
	t.Helper()
	if got, _ := request.Get(member); string(got) != want {
		t.Errorf("%s is sent as\n  %s\nPi sends\n  %s", member, got, want)
	}
}

// The objects a models.json entry or a registering extension supplies reach the wire with their members in the order the source lists
// them, merged as Pi's spreads merge them: base's members in base's order with an overridden one in its place, then the override's new
// members in the override's order. Pi keeps models.json as JSON.parse built it and an extension's definition as the extension wrote it.
func TestSuppliedObjectsReachTheWireInTheirSourceOrder(t *testing.T) {
	// models.json: an API key, an OpenAI-compatible provider with its own base URL; the provider's compat, the model's and a model
	// override are merged (provider-composer.ts:249 then :208), and so are the model's sampling parameters and the override's (:201-207).
	t.Run("models.json provider, model and override", func(t *testing.T) {
		server, bodies := memberOrderServer(t)
		services := modelsJSONServices(t, `{"providers":{"custom":{"baseUrl":"`+server.URL+`/v1","api":"openai-completions","apiKey":"test-key",
			"compat":{"thinkingFormat":"chat-template","openRouterRouting":{"order":["a"],"only":["b"],"max_price":{"prompt":1,"completion":2}},"chatTemplateKwargs":{"preserve_thinking":true,"enable_thinking":{"$var":"thinking.enabled"}}},
			"models":[{"id":"model","reasoning":true,"input":["text"],
				"compat":{"openRouterRouting":{"ignore":["x"],"only":["c"]},"chatTemplateKwargs":{"budget":7,"preserve_thinking":false}},
				"samplingParams":{"top_p":0.9,"min_p":0.1},
				"samplingParamsByThinkingLevel":{"off":{"top_k":40,"min_p":0.2}}}],
			"modelOverrides":{"model":{"compat":{"openRouterRouting":{"allow_fallbacks":false,"order":["z"]}},
				"samplingParams":{"temperature":0.3,"top_p":0.8,"seed":1},
				"samplingParamsByThinkingLevel":{"off":{"mirostat":2,"top_k":50}}}}}}}`)
		request := memberOrderRequest(t, server, bodies, services, "custom/model", ai.StreamOptions{})
		requireMember(t, request, "provider", `{"order":["z"],"only":["c"],"max_price":{"prompt":1,"completion":2},"ignore":["x"],"allow_fallbacks":false}`)
		requireMember(t, request, "chat_template_kwargs", `{"preserve_thinking":false,"enable_thinking":false,"budget":7}`)
		if got, want := samplingMembers(request, "top_p", "min_p", "temperature", "seed", "top_k", "mirostat"), []string{"top_p", "min_p", "temperature", "seed", "top_k", "mirostat"}; !slices.Equal(got, want) {
			t.Errorf("the sampling parameters are sent as %v, Pi sends %v", got, want)
		}
		requireMember(t, request, "top_p", `0.8`)
		requireMember(t, request, "min_p", `0.2`)
		requireMember(t, request, "top_k", `50`)
	})
	// models.json: a model added to a built-in provider, which gives the API; the provider entry's compat is the base of the model's.
	t.Run("models.json model of a built-in provider", func(t *testing.T) {
		server, bodies := memberOrderServer(t)
		services := modelsJSONServices(t, `{"providers":{"openrouter":{"baseUrl":"`+server.URL+`/v1","apiKey":"test-key","compat":{"openRouterRouting":{"quantizations":["fp8"],"only":["b"]}},
			"models":[{"id":"fake-provider/fake-model","name":"Fake model","reasoning":false,"input":["text"],"compat":{"openRouterRouting":{"order":["a"]}},"samplingParams":{"top_p":0.9,"min_p":0.1}}]}}}`)
		request := memberOrderRequest(t, server, bodies, services, "openrouter/fake-provider/fake-model", ai.StreamOptions{})
		requireMember(t, request, "provider", `{"quantizations":["fp8"],"only":["b"],"order":["a"]}`)
		if got, want := samplingMembers(request, "top_p", "min_p"), []string{"top_p", "min_p"}; !slices.Equal(got, want) {
			t.Errorf("the sampling parameters are sent as %v, Pi sends %v", got, want)
		}
	})
	// A provider an extension registers: the config is decoded from the registerProvider frame's JSON, as the subprocess host decodes it,
	// and Pi's model is the definition spread (provider-composer.ts:299).
	extensionServices := func(t *testing.T, server *httptest.Server) *AgentSessionServices {
		t.Helper()
		var config extension.ProviderConfig
		frame := `{"api":"openai-completions","baseUrl":"` + server.URL + `/v1","apiKey":"test-key","models":[{"id":"ext-model","name":"Ext","reasoning":true,"input":["text"],"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0},"contextWindow":128000,"maxTokens":4096,
			"compat":{"thinkingFormat":"chat-template","chatTemplateKwargs":{"b":1,"a":2},"openRouterRouting":{"order":["a"],"only":["b"],"max_price":{"prompt":1,"completion":2}}},
			"samplingParams":{"top_p":0.5,"min_p":0.1},"samplingParamsByThinkingLevel":{"off":{"top_k":7,"mirostat":1}}}]}`
		if err := json.Unmarshal([]byte(frame), &config); err != nil {
			t.Fatal(err)
		}
		services := newTestServices(t)
		if err := services.Registry().RegisterExtensionProvider("ext", config); err != nil {
			t.Fatal(err)
		}
		return services
	}
	t.Run("extension-registered model", func(t *testing.T) {
		server, bodies := memberOrderServer(t)
		request := memberOrderRequest(t, server, bodies, extensionServices(t, server), "ext/ext-model", ai.StreamOptions{})
		requireMember(t, request, "provider", `{"order":["a"],"only":["b"],"max_price":{"prompt":1,"completion":2}}`)
		requireMember(t, request, "chat_template_kwargs", `{"b":1,"a":2}`)
		if got, want := samplingMembers(request, "top_p", "min_p", "top_k", "mirostat"), []string{"top_p", "min_p", "top_k", "mirostat"}; !slices.Equal(got, want) {
			t.Errorf("the sampling parameters are sent as %v, Pi sends %v", got, want)
		}
	})
	// A caller's sampling parameters follow the model's (simple-options.ts:44-46) in the order the caller records beside the map; a caller
	// that sets only the map has them sent sorted.
	for _, tc := range []struct {
		name        string
		order, want []string
	}{
		{"request sampling parameters with their order", []string{"seed", "top_p", "frequency_penalty"}, []string{"top_p", "min_p", "top_k", "mirostat", "seed", "frequency_penalty"}},
		{"request sampling parameters without an order", nil, []string{"top_p", "min_p", "top_k", "mirostat", "frequency_penalty", "seed"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server, bodies := memberOrderServer(t)
			options := ai.StreamOptions{SamplingParams: ai.SamplingParams{"seed": 1.0, "top_p": 0.7, "frequency_penalty": 0.0}, SamplingParamsOrder: tc.order}
			request := memberOrderRequest(t, server, bodies, extensionServices(t, server), "ext/ext-model", options)
			if got := samplingMembers(request, "top_p", "min_p", "top_k", "mirostat", "seed", "frequency_penalty"); !slices.Equal(got, tc.want) {
				t.Errorf("the sampling parameters are sent as %v, want %v", got, tc.want)
			}
			requireMember(t, request, "top_p", `0.7`)
		})
	}
}
