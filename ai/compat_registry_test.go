package ai

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// capturedCall records what a registered stream received.
type capturedCall struct {
	mu      sync.Mutex
	apiKeys []string
	simple  []bool
}

func (c *capturedCall) stream(simple bool) StreamFunction {
	return func(_ context.Context, model *Model, _ TranscriptContext, options StreamOptions) (*AssistantMessageEventStream, error) {
		c.mu.Lock()
		c.apiKeys = append(c.apiKeys, options.APIKey)
		c.simple = append(c.simple, simple)
		c.mu.Unlock()
		stream := NewAssistantMessageEventStream()
		message := &AssistantMessage{API: model.ProviderMeta.API, Provider: model.ProviderMeta.ProviderID, Model: model.ID, Content: []AssistantContentBlock{TextContent{Text: "ok"}}, StopReason: StopReasonStop}
		_ = stream.Push(DoneEvent{Reason: StopReasonStop, Message: message})
		stream.End(message)
		return stream, nil
	}
}

func compatTestModel(provider string, api API) *Model {
	return &Model{ID: "test-model", DisplayName: "Test Model", ProviderMeta: ProviderMetadata{ProviderID: provider, API: api, BaseURL: "https://example.test/v1"}, Input: []string{"text"}, Capabilities: ModelCapabilities{ContextWindow: 128000, MaxOutputTokens: 4096}}
}

var compatHello = Context{Messages: []Message{UserMessage{Content: UserText("hi")}}}

func resetCompatRegistry(t *testing.T) {
	t.Helper()
	ResetAPIProviders()
	t.Cleanup(ResetAPIProviders)
}

// compat-env.test.ts:34-53 registers an openai-responses implementation, calls complete with an explicit key, and the stream receives it. Both stream forms are covered, and the environment key fills a missing, empty or blank key but never replaces an explicit one (compat.ts withEnvApiKey).
func TestCompatCompleteDispatchesThroughTheRegistryAndFillsTheEnvironmentKey(t *testing.T) {
	cases := []struct {
		name     string
		provider string
		options  StreamOptions
		want     string
	}{
		{"explicit key", "custom-openai", StreamOptions{APIKey: "request-key"}, "request-key"},
		{"explicit key beats the environment", "openai", StreamOptions{APIKey: "request-key", Env: ProviderEnv{"OPENAI_API_KEY": "env-key"}}, "request-key"},
		{"environment key", "openai", StreamOptions{Env: ProviderEnv{"OPENAI_API_KEY": "env-key"}}, "env-key"},
		{"blank key is replaced", "openai", StreamOptions{APIKey: "  \t", Env: ProviderEnv{"OPENAI_API_KEY": "env-key"}}, "env-key"},
		{"no key and no environment", "custom-openai", StreamOptions{}, ""},
	}
	for _, simple := range []bool{false, true} {
		for _, tc := range cases {
			t.Run(tc.name+map[bool]string{false: "/complete", true: "/completeSimple"}[simple], func(t *testing.T) {
				resetCompatRegistry(t)
				call := &capturedCall{}
				RegisterAPIProvider(APIProvider{API: APIOpenAIResponses, Stream: call.stream(false), StreamSimple: call.stream(true)})
				complete := Complete
				if simple {
					complete = CompleteSimple
				}
				message, err := complete(t.Context(), compatTestModel(tc.provider, APIOpenAIResponses), compatHello, tc.options)
				if err != nil || message.StopReason != StopReasonStop {
					t.Fatalf("message = %+v, err = %v", message, err)
				}
				if !slices.Equal(call.apiKeys, []string{tc.want}) || !slices.Equal(call.simple, []bool{simple}) {
					t.Fatalf("keys = %q simple = %v, want %q and %v", call.apiKeys, call.simple, tc.want, simple)
				}
			})
		}
	}
}

func TestCompatDispatchErrors(t *testing.T) {
	resetCompatRegistry(t)
	if _, err := Stream(t.Context(), compatTestModel("p", "no-such-api"), compatHello); err == nil || err.Error() != "No API provider registered for api: no-such-api" {
		t.Fatalf("unregistered API error = %v", err)
	}
	// compat.ts wrapStream: a registered implementation rejects a model of another API.
	call := &capturedCall{}
	RegisterAPIProvider(APIProvider{API: "x-api", Stream: call.stream(false), StreamSimple: call.stream(true)})
	registered := GetAPIProvider("x-api")
	for name, stream := range map[string]StreamFunction{"stream": registered.Stream, "streamSimple": registered.StreamSimple} {
		if _, err := stream(t.Context(), compatTestModel("p", "y-api"), NormalizeContext(compatHello), StreamOptions{}); err == nil || err.Error() != "Mismatched api: y-api expected x-api" {
			t.Fatalf("%s error = %v", name, err)
		}
	}
	if len(call.apiKeys) != 0 {
		t.Fatal("a mismatched model reached the implementation")
	}
	if _, err := Stream(t.Context(), nil, compatHello); err == nil {
		t.Fatal("a nil model was accepted")
	}
}

// compat.ts apiProviderRegistry is a Map: re-registering an API keeps its position, unregister removes every entry of a source, reset restores the built-ins in BUILTIN_APIS order.
func TestCompatRegistryOrderReplacementAndReset(t *testing.T) {
	apis := func() []API {
		var out []API
		for _, provider := range GetAPIProviders() {
			out = append(out, provider.API)
		}
		return out
	}
	builtin := []API{APIAnthropicMessages, APIOpenAICompletions, APIOpenAIResponses, APIOpenAICodexResponses, APIAzureOpenAIResponses, APIGoogleGenerativeAI, APIGoogleVertex, APIMistralConversations, APIBedrockConverseStream, APIPiMessages}
	resetCompatRegistry(t)
	if got := apis(); !slices.Equal(got, builtin) {
		t.Fatalf("built-in order = %v", got)
	}
	call := &capturedCall{}
	provider := func(api API) APIProvider {
		return APIProvider{API: api, Stream: call.stream(false), StreamSimple: call.stream(true)}
	}
	RegisterAPIProvider(provider("a-api"), "source-1")
	RegisterAPIProvider(provider("b-api"), "source-2")
	RegisterAPIProvider(provider("c-api"))
	before := GetAPIProvider(APIOpenAIResponses)
	RegisterAPIProvider(provider(APIOpenAIResponses), "source-1")
	if after := GetAPIProvider(APIOpenAIResponses); after == before {
		t.Fatal("re-registering did not replace the implementation")
	}
	if got, want := apis(), append(slices.Clone(builtin), "a-api", "b-api", "c-api"); !slices.Equal(got, want) {
		t.Fatalf("order after register = %v", got)
	}
	UnregisterAPIProviders("source-1")
	if GetAPIProvider("a-api") != nil || GetAPIProvider(APIOpenAIResponses) != nil || GetAPIProvider("b-api") == nil || GetAPIProvider("c-api") == nil {
		t.Fatalf("unregister removed %v", apis())
	}
	UnregisterAPIProviders("")
	if GetAPIProvider("c-api") == nil {
		t.Fatal("an entry with no source id was removed by the empty source id")
	}
	ResetAPIProviders()
	if got := apis(); !slices.Equal(got, builtin) {
		t.Fatalf("order after reset = %v", got)
	}
}

// compat.ts registerBuiltInApiProviders does not replace an entry that is already registered.
func TestRegisterBuiltInAPIProvidersKeepsAnOverride(t *testing.T) {
	resetCompatRegistry(t)
	call := &capturedCall{}
	RegisterAPIProvider(APIProvider{API: APIAnthropicMessages, Stream: call.stream(false), StreamSimple: call.stream(true)})
	override := GetAPIProvider(APIAnthropicMessages)
	RegisterBuiltInAPIProviders()
	if GetAPIProvider(APIAnthropicMessages) != override {
		t.Fatal("registerBuiltInApiProviders replaced the override")
	}
	// A model of a provider the catalog does not know is served by the registry, so the override is used.
	if _, err := Complete(t.Context(), compatTestModel("custom", APIAnthropicMessages), compatHello, StreamOptions{APIKey: "k"}); err != nil || len(call.apiKeys) != 1 {
		t.Fatalf("override was not used: keys = %v, err = %v", call.apiKeys, err)
	}
}

// compat.ts stream: a model served by the built-in implementation goes through the catalog provider with the environment key.
func TestCompatStreamUsesTheBuiltInProviderWithTheEnvironmentKey(t *testing.T) {
	resetCompatRegistry(t)
	headers := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		headers <- r.Header.Get("Authorization")
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(server.Close)
	model := compatTestModel("openai", APIOpenAIResponses)
	model.ProviderMeta.BaseURL = server.URL
	message, err := Complete(t.Context(), model, compatHello, StreamOptions{Env: ProviderEnv{"OPENAI_API_KEY": "env-key"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := <-headers; got != "Bearer env-key" {
		t.Fatalf("Authorization = %q (message %+v)", got, message)
	}
}

// legacy-api-aliases.ts and api/*.lazy.ts: each API's stream sends the options as given, while its simple stream applies the model's token limit; both check the model's API and the request authentication.
func TestAPIStreamsSendPlainAndSimpleOptions(t *testing.T) {
	bodies := make(chan string, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		bodies <- string(data)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(server.Close)
	model := compatTestModel("custom", APIOpenAICompletions)
	model.ProviderMeta.BaseURL = server.URL
	transcript := NormalizeContext(compatHello)
	hasLimit := func(body string) bool {
		return strings.Contains(body, `"max_tokens"`) || strings.Contains(body, `"max_completion_tokens"`)
	}
	for name, run := range map[string]struct {
		stream StreamFunction
		limit  bool
	}{
		"stream":       {StreamFunction(StreamOpenAICompletions), false},
		"streamSimple": {StreamFunction(StreamSimpleOpenAICompletions), true},
	} {
		t.Run(name, func(t *testing.T) {
			stream, err := run.stream(t.Context(), model, transcript, StreamOptions{APIKey: "k"})
			if err != nil {
				t.Fatal(err)
			}
			stream.Result()
			if body := <-bodies; hasLimit(body) != run.limit {
				t.Fatalf("token limit present = %v, want %v: %s", hasLimit(body), run.limit, body)
			}
			if failed, err := run.stream(t.Context(), model, transcript, StreamOptions{}); err != nil || failed.Result().ErrorMessage != "No API key for provider: custom" {
				t.Fatalf("missing key = %v, %v; want an error event", failed, err)
			}
		})
	}
	if events, err := PiMessagesAPI().Stream(t.Context(), compatTestModel("radius", APIPiMessages), transcript, StreamOptions{}); err != nil || events.Result().ErrorMessage != `No API key provided for provider "radius"` {
		t.Fatalf("pi-messages without a key: %v, %v", events, err)
	}
}

func TestGetBuiltinModelReadsOneCatalogModel(t *testing.T) {
	first := GetBuiltinModels("anthropic")[0]
	if got := GetBuiltinModel("anthropic", first.ID); got == nil || got.ID != first.ID || got.ProviderMeta.ProviderID != "anthropic" {
		t.Fatalf("GetBuiltinModel = %+v", got)
	}
	for _, missing := range [][2]string{{"anthropic", "no-such-model"}, {"no-such-provider", first.ID}, {"", first.ID}} {
		if got := GetBuiltinModel(missing[0], missing[1]); got != nil {
			t.Fatalf("GetBuiltinModel(%q, %q) = %+v", missing[0], missing[1], got)
		}
	}
}

// legacy-api-aliases.ts: streamAnthropic and its siblings are the stream and simple stream of one fixed API each. The alias has no
// model.api check (api/lazy.ts lazyApi): it builds its own API's request from the model's fields, so a model that names another api
// still gets that API's wire request.
func TestLegacyAPIAliasesSendTheirAPIsRequestWhateverTheModelsApi(t *testing.T) {
	transcript := NormalizeContext(compatHello)
	paths := make(chan string, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		paths <- r.URL.Path
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(server.Close)
	aliases := []struct {
		name         string
		suffix       string
		stream       APIStreamFunction
		streamSimple APIStreamSimpleFunction
	}{
		{"streamAnthropic", "/messages", StreamAnthropic, StreamSimpleAnthropic},
		{"streamOpenAICompletions", "/chat/completions", StreamOpenAICompletions, StreamSimpleOpenAICompletions},
		{"streamOpenAIResponses", "/responses", StreamOpenAIResponses, StreamSimpleOpenAIResponses},
	}
	for _, alias := range aliases {
		for form, stream := range map[string]StreamFunction{"stream": alias.stream, "streamSimple": alias.streamSimple} {
			model := compatTestModel("p", "other-api")
			model.ProviderMeta.BaseURL = server.URL
			events, err := stream(t.Context(), model, transcript, StreamOptions{APIKey: "k"})
			if err != nil || events == nil {
				t.Fatalf("%s %s = %v, %v", alias.name, form, events, err)
			}
			result := events.Result()
			select {
			case path := <-paths:
				if !strings.HasSuffix(path, alias.suffix) {
					t.Fatalf("%s %s requested %q, want the %s wire path", alias.name, form, path, alias.suffix)
				}
			case <-time.After(5 * time.Second):
				t.Fatalf("%s %s sent no request; the stream ended with %q", alias.name, form, result.ErrorMessage)
			}
			if strings.HasPrefix(result.ErrorMessage, "Mismatched api") {
				t.Fatalf("%s %s refused the model: %q", alias.name, form, result.ErrorMessage)
			}
		}
	}
}

// compat.ts registers the built-in implementations when the module loads (registerBuiltInApiProviders() at module scope), before
// any caller can register an override, so a caller's first registerApiProvider replaces the built-in and getBuiltinProviderForModel
// then routes even a catalog model to the override. PiG registers the built-ins lazily; a first RegisterAPIProvider must still land
// after them.
func TestFirstRegisterAPIProviderOverridesTheBuiltInForCatalogModels(t *testing.T) {
	compatRegistry.mu.Lock()
	compatBuiltInsRegistered = false
	compatRegistry.order, compatRegistry.entries, compatRegistry.builtins = nil, nil, nil
	compatRegistry.mu.Unlock()
	t.Cleanup(ResetAPIProviders)
	requests := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		requests <- struct{}{}
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(server.Close)
	call := &capturedCall{}
	RegisterAPIProvider(APIProvider{API: APIOpenAIResponses, Stream: call.stream(false), StreamSimple: call.stream(true)})
	model := compatTestModel("openai", APIOpenAIResponses)
	model.ProviderMeta.BaseURL = server.URL
	if _, err := Complete(t.Context(), model, compatHello, StreamOptions{APIKey: "k"}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-requests:
		t.Fatal("the catalog provider served the request; the first registered override was ignored")
	default:
	}
	if len(call.apiKeys) != 1 {
		t.Fatalf("override calls = %v, want one", call.apiKeys)
	}
	if order := GetAPIProviders(); len(order) != len(builtinAPIs) || order[0].API != APIAnthropicMessages {
		t.Fatalf("registry order after a first override = %d entries starting %v, want the built-in order with the override in place", len(order), order[0].API)
	}
}

// api/lazy.ts lazyApi + lazyStream (packages/ai/src/api/lazy.ts:35-70): the alias returns a stream at once and a setup failure ends
// that stream with an error event whose message is the failure's, for the plain and the simple form alike; a nil model still fails
// the call, as reading model.api does in Pi.
func TestLegacyAPIAliasesDeliverSetupFailuresOnTheStream(t *testing.T) {
	transcript := NormalizeContext(compatHello)
	t.Setenv("OPENAI_API_KEY", "")
	for form, stream := range map[string]StreamFunction{"stream": StreamOpenAICompletions, "streamSimple": StreamSimpleOpenAICompletions} {
		events, err := stream(t.Context(), compatTestModel("p", APIOpenAICompletions), transcript, StreamOptions{})
		if err != nil || events == nil {
			t.Fatalf("%s: a missing API key must not fail the call: stream %v, err %v", form, events, err)
		}
		result := events.Result()
		if result.StopReason != StopReasonError || result.ErrorMessage == "" || result.Provider != "p" || result.Model != "test-model" {
			t.Fatalf("%s: result = %+v, want an error message for provider p", form, result)
		}
		if _, err := stream(t.Context(), nil, transcript, StreamOptions{}); err == nil {
			t.Fatalf("%s: a nil model must fail the call", form)
		}
	}
}

// legacy-api-aliases.ts exports a stream and a streamSimple for each of eight APIs. Each alias returns its stream at once and the
// failure of its request (here a 401 from a local server, or a setup error such as a missing project) ends that stream with an
// error message, never a call failure.
func TestEveryLegacyAPIAliasReturnsAStreamThatEndsWithTheRequestFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(server.Close)
	transcript := NormalizeContext(compatHello)
	for _, alias := range []struct {
		name         string
		api          API
		stream       APIStreamFunction
		streamSimple APIStreamSimpleFunction
	}{
		{"streamAnthropic", APIAnthropicMessages, StreamAnthropic, StreamSimpleAnthropic},
		{"streamAzureOpenAIResponses", APIAzureOpenAIResponses, StreamAzureOpenAIResponses, StreamSimpleAzureOpenAIResponses},
		{"streamGoogle", APIGoogleGenerativeAI, StreamGoogle, StreamSimpleGoogle},
		{"streamGoogleVertex", APIGoogleVertex, StreamGoogleVertex, StreamSimpleGoogleVertex},
		{"streamMistral", APIMistralConversations, StreamMistral, StreamSimpleMistral},
		{"streamOpenAICodexResponses", APIOpenAICodexResponses, StreamOpenAICodexResponses, StreamSimpleOpenAICodexResponses},
		{"streamOpenAICompletions", APIOpenAICompletions, StreamOpenAICompletions, StreamSimpleOpenAICompletions},
		{"streamOpenAIResponses", APIOpenAIResponses, StreamOpenAIResponses, StreamSimpleOpenAIResponses},
	} {
		for form, stream := range map[string]StreamFunction{"stream": alias.stream, "streamSimple": alias.streamSimple} {
			model := compatTestModel("p", alias.api)
			model.ProviderMeta.BaseURL = server.URL
			ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
			events, err := stream(ctx, model, transcript, StreamOptions{APIKey: "k"})
			if err != nil || events == nil {
				cancel()
				t.Fatalf("%s %s: stream %v, err %v; a failing request must end the stream, not the call", alias.name, form, events, err)
			}
			result := events.Result()
			cancel()
			if result.StopReason != StopReasonError || result.ErrorMessage == "" || result.API != alias.api {
				t.Fatalf("%s %s: result = %+v, want an error stop with a message from the %s implementation", alias.name, form, result, alias.api)
			}
		}
	}
}

// compat.ts streamSimple(model, context, options): the Context's system prompt reaches the registered simple stream function
// as the transcript's initial system message, the environment key fills a missing key, and the plain stream function is not called.
func TestStreamSimpleContextDispatchesTheSimpleFunctionWithTheNormalizedContext(t *testing.T) {
	resetCompatRegistry(t)
	call := &capturedCall{}
	var transcriptSystem string
	simple := func(ctx context.Context, model *Model, transcript TranscriptContext, options StreamOptions) (*AssistantMessageEventStream, error) {
		if initial := GetInitialSystemMessage(transcript.Messages()); initial != nil {
			transcriptSystem = GetCurrentSystemPrompt([]Message{*initial})
		}
		return call.stream(true)(ctx, model, transcript, options)
	}
	RegisterAPIProvider(APIProvider{API: APIOpenAIResponses, Stream: call.stream(false), StreamSimple: simple})
	request := Context{SystemPrompt: "Be brief.", Messages: []Message{UserMessage{Content: UserText("hi")}}}
	stream, err := StreamSimpleContext(t.Context(), compatTestModel("openai", APIOpenAIResponses), request, StreamOptions{Env: ProviderEnv{"OPENAI_API_KEY": "env-key"}})
	if err != nil {
		t.Fatal(err)
	}
	if message := stream.Result(); message.StopReason != StopReasonStop {
		t.Fatalf("message = %+v", message)
	}
	if transcriptSystem != "Be brief." || !slices.Equal(call.apiKeys, []string{"env-key"}) || !slices.Equal(call.simple, []bool{true}) {
		t.Errorf("system = %q, keys = %q, simple = %v, want the system prompt, env-key and the simple function", transcriptSystem, call.apiKeys, call.simple)
	}
}
