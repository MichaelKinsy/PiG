package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"os/exec"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

type classifierImagesProbe struct {
	Kind   string   `json:"kind"`
	API    string   `json:"api"`
	Input  []string `json:"input"`
	Images int      `json:"images"`
}

type classifierImagesResult struct {
	Error        *string `json:"error"`
	StopReason   string  `json:"stopReason"`
	ErrorMessage *string `json:"errorMessage"`
}

// ClassifierContext.images (types.ts:669-677): model-operations.ts:46-54 assertClassifierInputSupported rejects images for a model whose input
// lacks "image"; system-one-shared.ts:116 and llama-cpp-classify.ts:437 reject images in their classify. The pinned pi-ai answers each probe.
func TestClassifierImagesMatchPi(t *testing.T) {
	var probes []classifierImagesProbe
	for _, input := range [][]string{{"text"}, {"text", "image"}} {
		for _, images := range []int{0, 1, 2} {
			probes = append(probes, classifierImagesProbe{Kind: "assert", API: "test-classifier", Input: input, Images: images})
		}
	}
	for _, api := range []string{"typesafe-system-one", "cloudflare-workers-ai-system-one", "llama-cpp-classify"} {
		for _, input := range [][]string{{"text"}, {"text", "image"}} {
			for _, images := range []int{1, 3} {
				probes = append(probes, classifierImagesProbe{Kind: "classify", API: api, Input: input, Images: images})
			}
		}
	}
	input, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/classifier_images.mjs", pigversion.UpstreamVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var want []classifierImagesResult
	if err := json.Unmarshal(output, &want); err != nil {
		t.Fatal(err)
	}
	classify := map[string]func(context.Context, ClassifierModel, ClassifierContext, ClassifierOptions) ClassifierResult{
		"typesafe-system-one":              ClassifyTypesafeSystemOne,
		"cloudflare-workers-ai-system-one": ClassifyCloudflareWorkersAISystemOne,
		"llama-cpp-classify":               ClassifyLlamaCpp,
	}
	for i, probe := range probes {
		model := ClassifierModel{ID: "m", Name: "M", API: ClassifierAPI(probe.API), Provider: "p", BaseURL: "http://127.0.0.1:1/", Input: probe.Input, ContextWindow: 1000}
		request := classifierTestContext()
		request.Images = make([]ImageContent, probe.Images)
		for j := range request.Images {
			request.Images[j] = ImageContent{Data: "AAAA", MimeType: "image/png"}
		}
		if probe.Kind == "assert" {
			var got *string
			if err := AssertClassifierInputSupported(&model, request); err != nil {
				message := err.Error()
				got = &message
			}
			if (got == nil) != (want[i].Error == nil) || got != nil && *got != *want[i].Error {
				t.Errorf("probe %d assert input=%v images=%d: %v, Pi %v", i, probe.Input, probe.Images, deref(got), deref(want[i].Error))
			}
			continue
		}
		result := classify[probe.API](t.Context(), model, request, ClassifierOptions{APIKey: "k"})
		if string(result.StopReason) != want[i].StopReason || result.ErrorMessage != *want[i].ErrorMessage {
			t.Errorf("probe %d %s images=%d: %q %q, Pi %q %q", i, probe.API, probe.Images, result.StopReason, result.ErrorMessage, want[i].StopReason, *want[i].ErrorMessage)
		}
	}
}

// Models.classify runs assertClassifierInputSupported before it asks the provider (models.ts:976-979): an unsupported image request is an error
// result and the provider is never called; a model that accepts images reaches the provider with the images.
func TestModelsClassifyChecksImageInputBeforeTheProvider(t *testing.T) {
	auth := ProviderAuth{APIKey: &APIKeyAuth{Name: "Test", Resolve: func(context.Context, APIKeyAuthInput) (*AuthResult, error) { return &AuthResult{}, nil }}}
	for _, test := range []struct {
		name      string
		input     []string
		wantCalls int32
		wantError string
	}{
		{"text-only model", []string{"text"}, 0, "Model test/cls does not accept image input"},
		{"image-capable model", []string{"text", "image"}, 1, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			var calls atomic.Int32
			var seen []ImageContent
			classifier := classifierTestModel("test", "cls")
			classifier.Input = test.input
			models := CreateModels()
			models.SetProvider(CreateProvider(CreateProviderOptions{ID: "test", Auth: auth, Models: []AnyModel{classifier},
				Classifiers: ProviderClassifierMap{"test-classifier": {Classify: func(_ context.Context, model *ClassifierModel, request ClassifierContext, _ ClassifierOptions) (ClassifierResult, error) {
					calls.Add(1)
					seen = request.Images
					return ClassifierResult{API: model.API, Provider: model.Provider, Model: model.ID, StopReason: "stop", Timestamp: time.Now().UnixMilli()}, nil
				}}}}))
			request := classifierTestContext()
			request.Images = []ImageContent{{Data: "AAAA", MimeType: "image/png"}}

			result := models.Classify(t.Context(), classifier, request)

			if calls.Load() != test.wantCalls || result.ErrorMessage != test.wantError {
				t.Fatalf("calls=%d result=%+v", calls.Load(), result)
			}
			if test.wantCalls == 1 && (len(seen) != 1 || seen[0] != request.Images[0]) {
				t.Fatalf("provider saw images %v", seen)
			}
		})
	}
}

// The context JSON carries images between state and questions and omits them when absent (types.ts:669-677).
func TestClassifierContextJSONCarriesImages(t *testing.T) {
	request := classifierTestContext()
	without, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	request.Images = []ImageContent{{Data: "AAAA", MimeType: "image/png"}}
	with, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	var decoded ClassifierContext
	if err := json.Unmarshal(with, &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.Images) != 1 || decoded.Images[0] != request.Images[0] || bytes.Contains(without, []byte(`"images"`)) || !bytes.Contains(with, []byte(`"images":[{"type":"image","data":"AAAA","mimeType":"image/png"}],"questions"`)) {
		t.Fatalf("without=%s with=%s decoded=%+v", without, with, decoded)
	}
}
