package ai

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

// Pi types.ts:385 ClassifierFunction: a classifier API reports failures in the result instead of throwing.
// The three built-in classifier APIs (api/typesafe-system-one.ts:20, api/cloudflare-workers-ai-system-one.ts:46, api/llama-cpp-classify.ts:425) are values of that type.
func TestBuiltInClassifiersAreClassifierFunctionsAndReportFailuresInTheResult(t *testing.T) {
	for _, tc := range []struct {
		name     string
		classify ClassifierFunction
		api      ClassifierAPI
		wrongAPI ClassifierAPI
	}{
		{"typesafe", ClassifyTypesafeSystemOne, "typesafe-system-one", "cloudflare-workers-ai-system-one"},
		{"cloudflare", ClassifyCloudflareWorkersAISystemOne, "cloudflare-workers-ai-system-one", "typesafe-system-one"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			model := ClassifierModel{ID: "m", Provider: "p", API: tc.wrongAPI}
			request := ClassifierContext{State: JsonObject{}, Questions: ClassifierQuestions{{ID: "q", Question: ClassifierBoolQuestion{Instructions: "?"}}}}
			result := tc.classify(t.Context(), model, request, ClassifierOptions{APIKey: "k"})
			if result.StopReason != ClassifierStopReasonError || !strings.Contains(result.ErrorMessage, "Unsupported classifier API: "+string(tc.wrongAPI)) {
				t.Fatalf("wrong api result = %#v", result)
			}
			if result.API != tc.wrongAPI || result.Provider != "p" || result.Model != "m" || len(result.Answers) != 0 {
				t.Fatalf("result identity = %#v", result)
			}

			model.API = tc.api
			result = tc.classify(t.Context(), model, request, ClassifierOptions{})
			if result.StopReason != ClassifierStopReasonError || !strings.Contains(result.ErrorMessage, "No API key for provider: p") {
				t.Fatalf("missing key result = %#v", result)
			}

			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			result = tc.classify(ctx, model, request, ClassifierOptions{})
			if result.StopReason != ClassifierStopReasonAborted {
				t.Fatalf("cancelled context stop reason = %q, want aborted", result.StopReason)
			}
		})
	}
}

// Pi images-api-registry.ts:38-47 and utils/model-operations.ts generateImages: a registered ImagesFunction receives the model, context and options of the call and its result is returned unchanged.
func TestRegisteredImagesFunctionReceivesTheCallAndReturnsItsResult(t *testing.T) {
	const api ImageAPI = "test-images-function"
	var gotModel ImageModel
	var gotOptions ProviderImagesOptions
	var fn ImagesFunction = func(_ context.Context, model ImageModel, _ ImagesContext, options ProviderImagesOptions) AssistantImages {
		gotModel, gotOptions = model, options
		return AssistantImages{API: model.API, Provider: model.Provider, Model: model.ID, Output: []ContentBlock{}, StopReason: ImagesStopReasonStop, ResponseID: "r1"}
	}
	RegisterImagesAPIProvider(ImagesAPIProvider{API: api, GenerateImages: fn})
	t.Cleanup(func() {
		imagesAPIProviderMu.Lock()
		defer imagesAPIProviderMu.Unlock()
		delete(imagesAPIProviderRegistry, api)
	})
	maxRetryDelay := 7
	result, err := GenerateImages(t.Context(), ImageModel{ID: "img", Provider: "prov", API: api}, ImagesContext{}, ImagesOptions{APIKey: "key", MaxRetryDelayMs: &maxRetryDelay})
	if err != nil {
		t.Fatal(err)
	}
	if result.ResponseID != "r1" || result.StopReason != ImagesStopReasonStop || result.Model != "img" {
		t.Fatalf("result = %#v", result)
	}
	if gotModel.ID != "img" || gotOptions.APIKey != "key" || gotOptions.MaxRetryDelayMs == nil || *gotOptions.MaxRetryDelayMs != 7 {
		t.Fatalf("function received model=%#v options=%#v", gotModel, gotOptions)
	}
	if _, err := GenerateImages(t.Context(), ImageModel{API: "test-images-function-unregistered"}, ImagesContext{}, ImagesOptions{}); err == nil || err.Error() != "No API provider registered for api: test-images-function-unregistered" {
		t.Fatalf("unregistered api error = %v", err)
	}
}

// Pi faux.ts FauxResponseFactory: the factory receives the request, options, provider state and model, is called once per request, and its error terminates that stream.
func TestFauxResponseFactoryReceivesRequestStateAndModel(t *testing.T) {
	faux := NewFauxProvider(FauxConfig{})
	models := CreateModels(CreateModelsOptions{})
	defer models.Close()
	models.SetProvider(faux.Provider())
	model := faux.GetModel()
	var calls int
	var seenModel *Model
	var seenMessages int
	var seenAPIKey string
	var factory FauxResponseFactory = func(request TranscriptContext, options StreamOptions, state *FauxProviderState, m *Model) (AssistantMessage, error) {
		calls++
		seenModel, seenMessages, seenAPIKey = m, len(request.Messages()), options.APIKey
		if state == nil {
			t.Fatal("factory received nil state")
		}
		return FauxAssistantMessage(FauxContentBlocks{FauxText("from factory")}, FauxAssistantMessageOptions{}), nil
	}
	faux.SetResponses([]FauxResponseStep{FauxFactoryStep(factory)})
	result := models.CompleteSimple(t.Context(), model, Context{Messages: []Message{UserMessage{Content: UserText("hi")}}}, StreamOptions{APIKey: "k"})
	if result.StopReason != StopReasonStop || contentBlocksText(result.Content, "") != "from factory" {
		t.Fatalf("result = %#v", result)
	}
	if calls != 1 || seenModel == nil || seenModel.ID != model.ID || seenMessages != 1 || seenAPIKey != "k" {
		t.Fatalf("factory calls=%d model=%v messages=%d apiKey=%q", calls, seenModel, seenMessages, seenAPIKey)
	}
}

// Pi openrouter-images.ts:76-81 passes the request's maxRetries and maxRetryDelayMs to retryProviderRequest:
// a retryable status is retried within the budget, no budget means one attempt, and a server-requested delay above the cap fails at once.
func TestOpenRouterImagesRetriesWithRequestMaxRetriesAndDelayCap(t *testing.T) {
	var attempts int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts++
		w.Header().Set("Content-Type", "application/json")
		if attempts == 1 {
			w.Header().Set("retry-after-ms", "1")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"error":{"message":"busy","code":503}}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":"resp-1","choices":[{"message":{"content":"done"}}]}`))
	}))
	defer server.Close()
	model := ImageModel{ID: "img", API: APIImagesOpenRouter, Provider: ProviderImagesOpenRouter, BaseURL: server.URL, Output: []string{"image"}}
	request := ImagesContext{Input: []ContentBlock{TextContent{Text: "draw"}}}

	result := GenerateImagesOpenRouter(t.Context(), model, request, ImagesOptions{APIKey: "k"})
	if result.StopReason != ImagesStopReasonError || attempts != 1 {
		t.Fatalf("no retry budget: stop=%s attempts=%d err=%q", result.StopReason, attempts, result.ErrorMessage)
	}

	attempts = 0
	result = GenerateImagesOpenRouter(t.Context(), model, request, ImagesOptions{APIKey: "k", MaxRetries: new(1)})
	if result.StopReason != ImagesStopReasonStop || result.ResponseID != "resp-1" || attempts != 2 {
		t.Fatalf("one retry: stop=%s id=%q attempts=%d err=%q", result.StopReason, result.ResponseID, attempts, result.ErrorMessage)
	}

	attempts = 0
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts++
		w.Header().Set("retry-after", "120")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"message":"slow down","code":429}}`))
	}))
	defer slow.Close()
	model.BaseURL = slow.URL
	result = GenerateImagesOpenRouter(t.Context(), model, request, ImagesOptions{APIKey: "k", MaxRetries: new(3), MaxRetryDelayMs: new(1000)})
	if result.StopReason != ImagesStopReasonError || attempts != 1 || !strings.Contains(result.ErrorMessage, "Server requested 120s retry delay (max: 1s)") {
		t.Fatalf("delay cap: stop=%s attempts=%d err=%q", result.StopReason, attempts, result.ErrorMessage)
	}
}

// Pi models.ts:1086-1087 createProvider carries the input's baseUrl and headers onto the Provider unchanged (and leaves them unset when omitted).
func TestCreateProviderCarriesBaseURLAndHeaders(t *testing.T) {
	api := &ProviderStreams{Stream: func(context.Context, *Model, TranscriptContext, StreamOptions) (*AssistantMessageEventStream, error) {
		return nil, nil
	}}
	headers := ProviderHeaders{"x-one": new("1"), "x-removed": nil}
	provider := CreateProvider(CreateProviderOptions{ID: "carry", BaseURL: "https://carry.example/v1", Headers: headers, API: api})
	if provider.BaseURL != "https://carry.example/v1" || !reflect.DeepEqual(provider.Headers, headers) {
		t.Fatalf("provider baseUrl=%q headers=%v", provider.BaseURL, provider.Headers)
	}
	bare := CreateProvider(CreateProviderOptions{ID: "bare", API: api})
	if bare.BaseURL != "" || bare.Headers != nil {
		t.Fatalf("omitted baseUrl/headers = %q %v", bare.BaseURL, bare.Headers)
	}
}

// Pi faux.ts:104-106,447: FauxProviderState.deferredFetchCount counts fetchDeferred calls and is visible to a response factory through its state argument.
func TestFauxProviderStateCountsDeferredFetches(t *testing.T) {
	faux := NewFauxProvider(FauxConfig{})
	models := CreateModels(CreateModelsOptions{})
	defer models.Close()
	models.SetProvider(faux.Provider())
	var state *FauxProviderState
	faux.SetResponses([]FauxResponseStep{FauxFactoryStep(func(_ TranscriptContext, _ StreamOptions, current *FauxProviderState, _ *Model) (AssistantMessage, error) {
		state = current
		return FauxResponse{Content: []FauxContentBlock{FauxText("ready")}, StopReason: "stop"}.AssistantMessage(), nil
	})})
	model := faux.GetModel()
	submission := models.CompleteSimple(t.Context(), model, Context{Messages: []Message{UserMessage{Content: UserText("hi")}}}, StreamOptions{Deferred: &DeferredOption{Enabled: true}})
	if submission.Deferred == nil {
		t.Fatalf("submission=%#v", submission)
	}
	for want := int64(1); want <= 2; want++ {
		models.FetchDeferred(t.Context(), model, *submission.Deferred, DeferredFetchOptions{Wait: new(0.0)})
		if state == nil {
			t.Fatal("the factory runs when the deferred response is fetched and receives the provider state")
		}
		if got := int64(state.DeferredFetchCount()); got != want {
			t.Fatalf("after fetch %d the state counts %d", want, got)
		}
	}
}

// images-api-registry.ts:38-47: a registration stores the optional sourceId on its entry, and a later registration of the same api replaces the entry, source included.
func TestRegisterImagesAPIProviderStoresTheSourceID(t *testing.T) {
	const api ImageAPI = "images-source-test"
	t.Cleanup(func() {
		imagesAPIProviderMu.Lock()
		delete(imagesAPIProviderRegistry, api)
		imagesAPIProviderMu.Unlock()
	})
	noop := func(context.Context, ImageModel, ImagesContext, ProviderImagesOptions) AssistantImages {
		return AssistantImages{}
	}
	RegisterImagesAPIProvider(ImagesAPIProvider{API: api, GenerateImages: noop}, "extension-a")
	imagesAPIProviderMu.RLock()
	entry := imagesAPIProviderRegistry[api]
	imagesAPIProviderMu.RUnlock()
	if entry.sourceID != "extension-a" || entry.provider.API != api {
		t.Fatalf("entry=%+v", entry)
	}
	RegisterImagesAPIProvider(ImagesAPIProvider{API: api, GenerateImages: noop})
	imagesAPIProviderMu.RLock()
	entry = imagesAPIProviderRegistry[api]
	imagesAPIProviderMu.RUnlock()
	if entry.sourceID != "" {
		t.Fatalf("a registration without a source replaces the stored source: %+v", entry)
	}
	if _, ok := GetImagesAPIProvider(api); !ok {
		t.Fatal("the provider stays registered")
	}
}
