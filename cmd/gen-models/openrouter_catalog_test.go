package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// The fixtures and cases mirror packages/ai/test/image-model-data.test.ts at 0.99.1.
func openRouterFixtures() (imageOnly, chatWithImages, chatOnly, decisionModel openRouterModelListItem) {
	imageOnly = openRouterModelListItem{ID: "example/image-model", Name: "Example Image Model"}
	imageOnly.Architecture = &struct {
		Modality         string   `json:"modality"`
		InputModalities  []string `json:"input_modalities"`
		OutputModalities []string `json:"output_modalities"`
	}{InputModalities: []string{"text", "image"}, OutputModalities: []string{"image"}}
	imageOnly.Pricing = &struct {
		Prompt          string `json:"prompt"`
		Completion      string `json:"completion"`
		InputCacheRead  string `json:"input_cache_read"`
		InputCacheWrite string `json:"input_cache_write"`
	}{Prompt: "0.000001", Completion: "0.000002"}

	chatWithImages = openRouterModelListItem{ID: "example/multimodal", Name: "Example Multimodal", SupportedParameters: []string{"tools"}, ContextLength: 32000}
	chatWithImages.Architecture = &struct {
		Modality         string   `json:"modality"`
		InputModalities  []string `json:"input_modalities"`
		OutputModalities []string `json:"output_modalities"`
	}{Modality: "text+image->text+image", InputModalities: []string{"text", "image"}, OutputModalities: []string{"text", "image"}}

	chatOnly = openRouterModelListItem{ID: "example/chat", Name: "Example Chat", SupportedParameters: []string{"tools"}}
	chatOnly.Architecture = &struct {
		Modality         string   `json:"modality"`
		InputModalities  []string `json:"input_modalities"`
		OutputModalities []string `json:"output_modalities"`
	}{Modality: "text->text", OutputModalities: []string{"text"}}

	decisionModel = openRouterModelListItem{ID: "typesafe/jev-1.13", Name: "TypeSafe: Jev 1.13", SupportedParameters: []string{}, ContextLength: 32000}
	decisionModel.Architecture = &struct {
		Modality         string   `json:"modality"`
		InputModalities  []string `json:"input_modalities"`
		OutputModalities []string `json:"output_modalities"`
	}{Modality: "text->decisions", InputModalities: []string{"text"}, OutputModalities: []string{"decisions"}}
	decisionModel.Pricing = &struct {
		Prompt          string `json:"prompt"`
		Completion      string `json:"completion"`
		InputCacheRead  string `json:"input_cache_read"`
		InputCacheWrite string `json:"input_cache_write"`
	}{Prompt: "0.000000042", Completion: "0"}
	decisionModel.TopProvider = &struct {
		ContextLength       int `json:"context_length"`
		MaxCompletionTokens int `json:"max_completion_tokens"`
	}{ContextLength: 32000, MaxCompletionTokens: 28800}
	return
}

func withOutputModalities(item openRouterModelListItem, output ...string) openRouterModelListItem {
	architecture := *item.Architecture
	architecture.OutputModalities = output
	item.Architecture = &architecture
	return item
}

func TestOpenRouterCatalogParsingUpstream(t *testing.T) {
	imageOnly, chatWithImages, chatOnly, decisionModel := openRouterFixtures()
	// packages/ai/test/image-model-data.test.ts:59
	t.Run("emits image-only models from the image listing as image models", func(t *testing.T) {
		catalog := buildOpenRouterCatalog(nil, []openRouterModelListItem{imageOnly}, nil)
		if len(catalog.Chat) != 0 || len(catalog.Images) != 1 {
			t.Fatalf("catalog = %+v, want one image model and no chat models", catalog)
		}
		image := catalog.Images[0]
		if image.Type != "image" || image.ID != "example/image-model" || image.API != "openrouter-images" || !slices.Equal(image.Input, []string{"text", "image"}) || !slices.Equal(image.Output, []string{"image"}) || image.Cost.Input != 1 || image.Cost.Output != 2 {
			t.Fatalf("image model = %+v", image)
		}
	})
	// packages/ai/test/image-model-data.test.ts:75
	t.Run("emits separate chat and image entries for an id that supports both operations", func(t *testing.T) {
		catalog := buildOpenRouterCatalog([]openRouterModelListItem{chatWithImages, chatOnly}, []openRouterModelListItem{chatWithImages, imageOnly}, nil)
		var chatIDs, chatTypes, imageIDs []string
		for _, model := range catalog.Chat {
			chatIDs, chatTypes = append(chatIDs, model.ID), append(chatTypes, model.Type)
		}
		var outputs [][]string
		for _, model := range catalog.Images {
			imageIDs, outputs = append(imageIDs, model.ID), append(outputs, model.Output)
			if model.Type != "image" {
				t.Errorf("image %s type = %q", model.ID, model.Type)
			}
		}
		if !slices.Equal(chatIDs, []string{"example/multimodal", "example/chat"}) || !slices.Equal(chatTypes, []string{"chat", "chat"}) {
			t.Errorf("chat = %v %v", chatIDs, chatTypes)
		}
		if !slices.Equal(imageIDs, []string{"example/multimodal", "example/image-model"}) || !reflect.DeepEqual(outputs, [][]string{{"text", "image"}, {"image"}}) {
			t.Errorf("images = %v %v", imageIDs, outputs)
		}
	})
	// packages/ai/test/image-model-data.test.ts:85
	t.Run("ignores listed models that neither support tools nor emit images", func(t *testing.T) {
		toolless := chatOnly
		toolless.SupportedParameters = []string{}
		catalog := buildOpenRouterCatalog(
			[]openRouterModelListItem{toolless},
			[]openRouterModelListItem{withOutputModalities(imageOnly, "text")},
			[]openRouterModelListItem{withOutputModalities(decisionModel, "text")},
		)
		if len(catalog.Chat) != 0 || len(catalog.Images) != 0 || len(catalog.Classifiers) != 0 {
			t.Fatalf("catalog = %+v, want empty", catalog)
		}
	})
	// packages/ai/test/image-model-data.test.ts:96
	t.Run("emits decision models as System One classifier models", func(t *testing.T) {
		catalog := buildOpenRouterCatalog(nil, nil, []openRouterModelListItem{decisionModel, decisionModel})
		want := []openRouterClassifierModel{{Type: "classifier", ID: "typesafe/jev-1.13", Name: "TypeSafe: Jev 1.13", API: "typesafe-system-one", Provider: "openrouter", BaseURL: "https://openrouter.ai/api/v1", Input: []string{"text"}, Cost: jsonCost{Input: 0.042}, ContextWindow: 32000}}
		if len(catalog.Chat) != 0 || !reflect.DeepEqual(catalog.Classifiers, want) {
			t.Fatalf("classifiers = %+v, want %+v", catalog.Classifiers, want)
		}
	})
}

// Number(value.toFixed(6)) as evaluated by Node 24: ties round away from zero on the exact binary value.
func TestRoundCostMatchesToFixed(t *testing.T) {
	for _, row := range []struct{ in, want float64 }{{0.0078125, 0.007813}, {-0.0078125, -0.007813}, {1.0000005, 1.000001}, {0.000000042 * 1e6, 0.042}, {2.5e-7 * 1e6, 0.25}, {0, 0}} {
		if got := roundCost(row.in); got != row.want {
			t.Errorf("roundCost(%v) = %v, want %v", row.in, got, row.want)
		}
	}
}

// generate-models.ts:1306-1327 fetches the default, image and decision OpenRouter listings and writes chat, image and
// classifier models under the provider in models.all.json and providers/openrouter.all.json, chat only in providers/openrouter.json.
func TestOpenRouterStageWritesTypedCatalog(t *testing.T) {
	root := t.TempDir()
	output := filepath.Join(root, "catalog")
	listings := map[string]string{
		"":                             `{"data":[{"id":"example/chat","name":"Example Chat","supported_parameters":["tools","reasoning"],"architecture":{"modality":"text+image->text"},"pricing":{"prompt":"0.000003","completion":"0.000015"},"context_length":200000,"top_provider":{"max_completion_tokens":8000}},{"id":"anthropic/claude-x","name":"Claude X","supported_parameters":["tools"]},{"id":"example/chat","name":"Duplicate Chat","supported_parameters":["tools"]}]}`,
		"?output_modalities=image":     `{"data":[{"id":"example/image","name":"Example Image","architecture":{"input_modalities":["text"],"output_modalities":["image"]},"pricing":{"prompt":"0.000001"}}]}`,
		"?output_modalities=decisions": `{"data":[{"id":"typesafe/jev","name":"Jev","architecture":{"input_modalities":["text"],"output_modalities":["decisions"]},"pricing":{"prompt":"0.000000042"},"context_length":32000}]}`,
	}
	status, stdout, stderr := runGeneratorSubprocessWithOpenRouter(t, root, map[string]any{"fireworks-ai": map[string]any{"models": map[string]any{}}}, listings, "--json-only", "--json-output", output)
	if status != 0 || stderr != "" {
		t.Fatalf("generator status=%d stderr=%q stdout=%s", status, stderr, stdout)
	}
	read := func(name string) string {
		data, err := os.ReadFile(filepath.Join(output, name))
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	var chat map[string]map[string]any
	if err := json.Unmarshal([]byte(read("providers/openrouter.json")), &chat); err != nil {
		t.Fatal(err)
	}
	if len(chat) != 2 || chat["example/chat"]["name"] != "Example Chat" || chat["anthropic/claude-x"]["api"] != "anthropic-messages" || chat["anthropic/claude-x"]["baseUrl"] != "https://openrouter.ai/api" || chat["example/chat"]["type"] != "chat" || chat["example/chat"]["reasoning"] != true || chat["example/chat"]["maxTokens"] != float64(8000) {
		t.Fatalf("providers/openrouter.json = %v", chat)
	}
	var all []map[string]any
	if err := json.Unmarshal([]byte(read("providers/openrouter.all.json")), &all); err != nil {
		t.Fatal(err)
	}
	var kinds []string
	for _, model := range all {
		kinds = append(kinds, model["type"].(string)+":"+model["id"].(string))
	}
	if want := []string{"chat:anthropic/claude-x", "chat:example/chat", "image:example/image", "classifier:typesafe/jev"}; !slices.Equal(kinds, want) {
		t.Fatalf("providers/openrouter.all.json = %v, want %v", kinds, want)
	}
	if !strings.Contains(read("providers.json"), `"openrouter"`) || !strings.Contains(read("models.all.json"), `"typesafe/jev"`) {
		t.Fatalf("aggregate files omit the OpenRouter stage: %s / %s", read("providers.json"), read("models.all.json"))
	}
}

// TestOpenRouterCatalogParity prints the catalog for every case of the parity oracle input. The
// 16-image-model-data scenario compares these lines with buildOpenRouterCatalog from the pinned upstream script.
func TestOpenRouterCatalogParity(t *testing.T) {
	data, err := os.ReadFile("../../test/parity/testdata/openrouter-catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Listed         []openRouterModelListItem `json:"listed"`
		ImageListed    []openRouterModelListItem `json:"imageListed"`
		DecisionListed []openRouterModelListItem `json:"decisionListed"`
	}
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	for _, test := range cases {
		catalog := buildOpenRouterCatalog(test.Listed, test.ImageListed, test.DecisionListed)
		encoded, err := json.Marshal(map[string]any{"chat": catalog.Chat, "images": catalog.Images, "classifiers": catalog.Classifiers})
		if err != nil {
			t.Fatal(err)
		}
		// Round-trip through a generic value: encoding/json sorts map keys, matching the oracle's canonical key order.
		var generic any
		if err := json.Unmarshal(encoded, &generic); err != nil {
			t.Fatal(err)
		}
		if encoded, err = json.Marshal(generic); err != nil {
			t.Fatal(err)
		}
		if os.Getenv("PIG_PARITY_PROBE") == "1" {
			fmt.Printf("OPENROUTER_CATALOG %s\n", encoded)
		}
	}
}

// openrouter-catalog.ts modalities(): only text and image survive, each once, in order; no input modality falls back to text.
func TestOpenRouterModalitiesFilterAndDeduplicate(t *testing.T) {
	image := openRouterModelListItem{ID: "fallback", Name: "Fallback"}
	image.Architecture = &struct {
		Modality         string   `json:"modality"`
		InputModalities  []string `json:"input_modalities"`
		OutputModalities []string `json:"output_modalities"`
	}{InputModalities: []string{"audio"}, OutputModalities: []string{"image", "image", "text", "video", "text"}}
	catalog := buildOpenRouterCatalog(nil, []openRouterModelListItem{image}, nil)
	if len(catalog.Images) != 1 || !slices.Equal(catalog.Images[0].Input, []string{"text"}) || !slices.Equal(catalog.Images[0].Output, []string{"image", "text"}) {
		t.Fatalf("images = %+v", catalog.Images)
	}
}

// JSON.stringify writes negative zero as 0. Node 24 evaluates buildOpenRouterCatalog for these prices to
// {"input":0,"output":0,"cacheRead":0,"cacheWrite":0}: "-0" rounds to +0 and "-1e-13" to -0 (toFixed keeps the sign).
func TestOpenRouterCostSerializesNegativeZeroAsZero(t *testing.T) {
	model := openRouterModelListItem{ID: "vendor/negative", Name: "Negative", SupportedParameters: []string{"tools"}}
	model.Pricing = &struct {
		Prompt          string `json:"prompt"`
		Completion      string `json:"completion"`
		InputCacheRead  string `json:"input_cache_read"`
		InputCacheWrite string `json:"input_cache_write"`
	}{Prompt: "-0", Completion: "-1e-13", InputCacheRead: "-0.0000000000001"}
	catalog := buildOpenRouterCatalog([]openRouterModelListItem{model}, nil, nil)
	encoded, err := json.Marshal(catalog.Chat[0].Cost)
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"input":0,"output":0,"cacheRead":0,"cacheWrite":0}`; string(encoded) != want {
		t.Fatalf("cost = %s, want %s", encoded, want)
	}
}
