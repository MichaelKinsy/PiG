package coding

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// Ports .upstream/v0.99.1/packages/coding-agent/test/model-runtime-classifiers.test.ts and the three cases of
// model-runtime-images.test.ts that read ModelRuntime.classify (model_runtime_images_upstream_test.go holds the others):
// :143-185 (extension image and classifier models with their implementations) and :319-322 (composed chat-only providers
// and the typesafe classify method).

var classifiersTestContext = ai.ClassifierContext{
	State: ai.JsonObject{"text": "Looks good"},
	Questions: ai.ClassifierQuestions{{ID: "approved", Question: ai.ClassifierBoolQuestion{
		Instructions: "Does this express approval?",
		Criteria:     ai.ClassifierBoolCriteria{True: "Approval", False: "No approval"},
	}}},
}

type classifiersTestFetch func(*http.Request) (*http.Response, error)

func (f classifiersTestFetch) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func classifiersTestOKResult(model *ai.ClassifierModel) ai.ClassifierResult {
	return ai.ClassifierResult{API: model.API, Provider: model.Provider, Model: model.ID, Answers: ai.ClassifierAnswers{{ID: "approved", Answer: ai.ClassifierBoolAnswer{Probability: 0.9}}}, StopReason: ai.ClassifierStopReasonStop}
}

func TestModelRuntimeClassifiersUpstream(t *testing.T) {
	// model-runtime-classifiers.test.ts:15
	t.Run("lists Jev separately and classifies with runtime-resolved auth", func(t *testing.T) {
		services, _ := nativeCompatServices(t, "", nil)
		runtime := services.ModelRuntime()
		jev, _ := runtime.GetModelOfType(ai.ModelTypeClassifier, "typesafe", "jev-latest").(*ai.ClassifierModel)
		if jev == nil || jev.ModelType() != ai.ModelTypeClassifier {
			t.Fatalf("jev = %+v", jev)
		}
		if model := runtime.GetModel("typesafe", "jev-latest"); model != nil {
			t.Fatalf("a classifier model is listed as a chat model: %+v", model)
		}

		unconfigured := runtime.Classify(t.Context(), jev, classifiersTestContext)
		if unconfigured.StopReason != ai.ClassifierStopReasonError || !strings.Contains(unconfigured.ErrorMessage, "not configured") {
			t.Fatalf("unconfigured = %+v", unconfigured)
		}

		services.Registry().SetRuntimeAPIKey("typesafe", "sk-typesafe")
		available, err := runtime.GetAvailableOfType(t.Context(), ai.ModelTypeClassifier, "typesafe")
		if err != nil || !reflect.DeepEqual(available, []ai.AnyModel{jev}) {
			t.Fatalf("available = %v, %v", available, err)
		}
		fetch := classifiersTestFetch(func(request *http.Request) (*http.Response, error) {
			if got := request.Header.Get("Authorization"); got != "Bearer sk-typesafe" {
				t.Errorf("authorization = %q", got)
			}
			return &http.Response{StatusCode: 200, Status: "OK", Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"answers":{"approved":{"type":"noul","noul":0.8}}}`))}, nil
		})
		result := runtime.Classify(t.Context(), jev, classifiersTestContext, ai.ModelsClassifierOptions{ClassifierOptions: ai.ClassifierOptions{Fetch: &http.Client{Transport: fetch}}})
		if result.StopReason != ai.ClassifierStopReasonStop || len(result.Answers) != 1 || result.Answers[0].ID != "approved" || result.Answers[0].Answer != (ai.ClassifierBoolAnswer{Probability: 0.8}) {
			t.Fatalf("result = %+v", result)
		}
	})

	// model-runtime.ts:808 (v1.1.0): assertClassifierInputSupported runs before auth, so a model without image input never
	// reaches the provider, and GPT-6 Luna takes images through the runtime-resolved API key.
	t.Run("rejects classifier images for models without image input and sends them to GPT-6 Luna", func(t *testing.T) {
		services, _ := nativeCompatServices(t, "", nil)
		runtime := services.ModelRuntime()
		withImages := classifiersTestContext
		withImages.Images = []ai.ImageContent{{Data: "aW1hZ2U=", MimeType: "image/png"}}
		// typesafe/jev-latest takes images since Pi v1.4.0; opencode/jev-1.13 is the text-only classifier.
		jev, _ := runtime.GetModelOfType(ai.ModelTypeClassifier, "opencode", "jev-1.13").(*ai.ClassifierModel)
		luna, _ := runtime.GetModelOfType(ai.ModelTypeClassifier, "openai", "gpt-6-luna").(*ai.ClassifierModel)
		if jev == nil || luna == nil {
			t.Fatalf("jev=%v luna=%v", jev, luna)
		}

		rejected := runtime.Classify(t.Context(), jev, withImages)
		if rejected.StopReason != ai.ClassifierStopReasonError || rejected.ErrorMessage != "Model opencode/jev-1.13 does not accept image input" {
			t.Fatalf("rejected = %+v", rejected)
		}

		services.Registry().SetRuntimeAPIKey("openai", "sk-openai")
		var url, authorization, input string
		fetch := classifiersTestFetch(func(request *http.Request) (*http.Response, error) {
			url, authorization = request.URL.String(), request.Header.Get("Authorization")
			body, _ := io.ReadAll(request.Body)
			input = string(body)
			return &http.Response{StatusCode: 200, Status: "OK", Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"answers":[{"type":"predicate","name":"approved","probability":0.7}]}`))}, nil
		})
		result := runtime.Classify(t.Context(), luna, withImages, ai.ModelsClassifierOptions{ClassifierOptions: ai.ClassifierOptions{Fetch: &http.Client{Transport: fetch}}})
		if result.StopReason != ai.ClassifierStopReasonStop || len(result.Answers) != 1 || result.Answers[0].Answer != (ai.ClassifierBoolAnswer{Probability: 0.7}) {
			t.Fatalf("result = %+v", result)
		}
		if url != "https://api.openai.com/v1/decisions" || authorization != "Bearer sk-openai" || !strings.Contains(input, `"image_url":"data:image/png;base64,aW1hZ2U="`) {
			t.Fatalf("url=%s authorization=%s body=%s", url, authorization, input)
		}
	})

	// model-runtime-images.test.ts:143
	t.Run("registers extension image and classifier models with their implementations", func(t *testing.T) {
		services, _ := nativeCompatServices(t, "", nil)
		runtime := services.ModelRuntime()
		type observation struct {
			APIKey  string
			Headers ai.ProviderHeaders
		}
		var observed []observation
		imageModel := imagesTestImageModel("ignored", "shared")
		imageModel.Headers = map[string]string{"X-Operation": "image"}
		classifierModel := imagesTestClassifierModel("ignored", "shared")
		classifierModel.Headers = map[string]string{"X-Operation": "classifier"}
		if err := runtime.RegisterProvider("extension-operations", ProviderConfigInput{
			APIKey: "extension-secret",
			Models: []ai.AnyModel{imageModel, classifierModel},
			Images: ai.ProviderImageAPIMap{"test-images": {GenerateImages: func(_ context.Context, model *ai.ImageModel, _ ai.ImagesContext, options ai.ImagesOptions) (ai.AssistantImages, error) {
				observed = append(observed, observation{options.APIKey, options.Headers})
				return imagesTestOKResult(model), nil
			}}},
			Classifiers: ai.ProviderClassifierMap{"test-classifier": {Classify: func(_ context.Context, model *ai.ClassifierModel, _ ai.ClassifierContext, options ai.ClassifierOptions) (ai.ClassifierResult, error) {
				observed = append(observed, observation{options.APIKey, options.Headers})
				return classifiersTestOKResult(model), nil
			}}},
		}); err != nil {
			t.Fatal(err)
		}
		image, _ := runtime.GetModelOfType(ai.ModelTypeImage, "extension-operations", "shared").(*ai.ImageModel)
		classifier, _ := runtime.GetModelOfType(ai.ModelTypeClassifier, "extension-operations", "shared").(*ai.ClassifierModel)
		if image == nil || classifier == nil {
			t.Fatalf("image = %v, classifier = %v", image, classifier)
		}
		if result := runtime.GenerateImages(t.Context(), image, imagesTestContext); result.StopReason != ai.ImagesStopReasonStop {
			t.Fatalf("image result = %+v", result)
		}
		if result := runtime.Classify(t.Context(), classifier, classifiersTestContext); result.StopReason != ai.ClassifierStopReasonStop {
			t.Fatalf("classifier result = %+v", result)
		}
		header := func(value string) ai.ProviderHeaders { return ai.ProviderHeaders{"X-Operation": &value} }
		want := []observation{{"extension-secret", header("image")}, {"extension-secret", header("classifier")}}
		if !reflect.DeepEqual(observed, want) {
			t.Fatalf("observed = %+v, want %+v", observed, want)
		}
	})

	// model-runtime-images.test.ts:319-322: composed chat-only providers get no classify; the typesafe provider has one.
	t.Run("does not add classification to composed chat-only providers", func(t *testing.T) {
		services, _ := nativeCompatServices(t, `{"providers":{"anthropic":{"headers":{"X-Title":"pi"}}}}`, nil)
		runtime := services.ModelRuntime()
		if provider := runtime.GetProvider("anthropic"); provider == nil || provider.Classify != nil {
			t.Fatalf("anthropic = %+v", provider)
		}
		if provider := runtime.GetProvider("typesafe"); provider == nil || provider.Classify == nil {
			t.Fatalf("typesafe = %+v", provider)
		}
	})
}

// model-registry.ts classify and generateImages are the runtime's: the options reach the provider through the registry, and a classification that fails because its request was cancelled settles as an aborted result, not an error.
// Pi: packages/coding-agent/src/core/model-registry.ts classify/generateImages; model-runtime.ts classify.
func TestModelRegistryClassifyForwardsOptionsAndACancelledRequestIsAborted(t *testing.T) {
	services, _ := nativeCompatServices(t, "", nil)
	services.Registry().SetRuntimeAPIKey("typesafe", "sk-typesafe")
	runtime := services.ModelRuntime()
	jev, _ := runtime.GetModelOfType(ai.ModelTypeClassifier, "typesafe", "jev-latest").(*ai.ClassifierModel)
	if jev == nil {
		t.Fatal("jev-latest is missing")
	}
	var requests int
	fetch := classifiersTestFetch(func(*http.Request) (*http.Response, error) {
		requests++
		return &http.Response{StatusCode: 200, Status: "OK", Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"answers":{"approved":{"type":"noul","noul":0.8}}}`))}, nil
	})
	options := ai.ModelsClassifierOptions{ClassifierOptions: ai.ClassifierOptions{Fetch: &http.Client{Transport: fetch}}}
	var registry *ModelRegistry = services.Registry()
	if result := registry.Classify(t.Context(), jev, classifiersTestContext, options); result.StopReason != ai.ClassifierStopReasonStop || requests != 1 {
		t.Fatalf("registry classify with options = %+v after %d requests", result, requests)
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if result := runtime.Classify(cancelled, jev, classifiersTestContext, options); result.StopReason != ai.ClassifierStopReasonAborted {
		t.Errorf("a cancelled classification = %+v, want an aborted result", result)
	}

	var apiKeys []string
	image := imagesTestImageModel("ignored", "shared")
	if err := runtime.RegisterProvider("registry-images", ProviderConfigInput{
		APIKey: "provider-key", Models: []ai.AnyModel{image},
		Images: ai.ProviderImageAPIMap{"test-images": {GenerateImages: func(_ context.Context, model *ai.ImageModel, _ ai.ImagesContext, options ai.ImagesOptions) (ai.AssistantImages, error) {
			apiKeys = append(apiKeys, options.APIKey)
			return imagesTestOKResult(model), nil
		}}},
	}); err != nil {
		t.Fatal(err)
	}
	imageModel, _ := runtime.GetModelOfType(ai.ModelTypeImage, "registry-images", "shared").(*ai.ImageModel)
	if imageModel == nil {
		t.Fatal("the registered image model is missing")
	}
	if result := registry.GenerateImages(t.Context(), imageModel, imagesTestContext, ai.ModelsImagesOptions{ImagesOptions: ai.ImagesOptions{APIKey: "caller-key"}}); result.StopReason != ai.ImagesStopReasonStop {
		t.Fatalf("registry generateImages = %+v", result)
	}
	if len(apiKeys) != 1 || apiKeys[0] != "caller-key" {
		t.Errorf("the provider saw API keys %q, want the caller's option to reach it through the registry", apiKeys)
	}
}

// Ports the two models.json image cases of packages/coding-agent/test/model-runtime-classifiers.test.ts (Pi 7f225f25).
func TestModelRuntimeClassifierImagesUpstream(t *testing.T) {
	withImage := classifiersTestContext
	withImage.Images = []ai.ImageContent{{Data: "cG5n", MimeType: "image/png"}}
	type captured struct {
		url, optIn string
		body       map[string]any
	}
	captureImageRequest := func(t *testing.T, config, provider, id string) captured {
		t.Helper()
		services, _ := nativeCompatServices(t, config, nil)
		runtime := services.ModelRuntime()
		services.Registry().SetRuntimeAPIKey(provider, "secret")
		model, _ := runtime.GetModelOfType(ai.ModelTypeClassifier, provider, id).(*ai.ClassifierModel)
		if model == nil {
			t.Fatalf("missing classifier model %s/%s", provider, id)
		}
		var requests []captured
		fetch := classifiersTestFetch(func(request *http.Request) (*http.Response, error) {
			raw, _ := io.ReadAll(request.Body)
			var body map[string]any
			if err := json.Unmarshal(raw, &body); err != nil {
				t.Errorf("body %s: %v", raw, err)
			}
			requests = append(requests, captured{url: request.URL.String(), optIn: request.Header.Get("typesafe-danger-zone-images"), body: body})
			return &http.Response{StatusCode: 200, Status: "OK", Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"answers":{"approved":{"type":"noul","noul":0.8}}}`))}, nil
		})
		result := runtime.Classify(t.Context(), model, withImage, ai.ModelsClassifierOptions{ClassifierOptions: ai.ClassifierOptions{Fetch: &http.Client{Transport: fetch}}})
		if result.StopReason != ai.ClassifierStopReasonStop || len(requests) != 1 {
			t.Fatalf("result = %+v, requests = %d", result, len(requests))
		}
		return requests[0]
	}
	wantState := []any{map[string]any{"type": "image_url", "image_url": map[string]any{"url": "data:image/png;base64,cG5n"}}, map[string]any{"text": "Looks good"}}

	// model-runtime-classifiers.test.ts:88 (Pi 7f225f25)
	t.Run("keeps OpenRouter's image form when models.json routes OpenRouter through a proxy", func(t *testing.T) {
		request := captureImageRequest(t, `{"providers":{"openrouter":{"baseUrl":"https://proxy.example.com/v1"}}}`, "openrouter", "typesafe/jev-1.14")

		// OpenRouter answers TypeSafe assets with 200 but drops the images.
		if request.url != "https://proxy.example.com/v1/systemone" || !reflect.DeepEqual(request.body["state"], wantState) || request.body["assets"] != nil || request.optIn != "" {
			t.Fatalf("request = %+v", request)
		}
	})
	// model-runtime-classifiers.test.ts:104 (Pi 7f225f25)
	t.Run("applies provider compat from models.json to classifier models", func(t *testing.T) {
		request := captureImageRequest(t, `{"providers":{"typesafe":{"baseUrl":"https://proxy.example.com/v1","compat":{"imageInput":"state-image-urls","sendImageOptInHeader":false}}}}`, "typesafe", "jev-latest")

		if request.url != "https://proxy.example.com/v1/systemone" || !reflect.DeepEqual(request.body["state"], wantState) || request.optIn != "" {
			t.Fatalf("request = %+v", request)
		}
	})
}
