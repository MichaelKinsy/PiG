package main

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const validOpenRouterImageModel = `{"id":"example/image-model","name":"Example Image Model","architecture":{"input_modalities":["text","image"],"output_modalities":["image"]},"pricing":{"prompt":"0.000001","completion":"0.000002"}}`

func loadOpenRouterFixture(t *testing.T, payload string) ([]imageModel, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "openrouter.json")
	if err := os.WriteFile(path, []byte(payload), 0o600); err != nil {
		t.Fatal(err)
	}
	return load(path)
}

func TestOpenRouterImageModelParsingUpstream(t *testing.T) {
	// .upstream/v0.87.1/packages/ai/test/image-model-data.test.ts:18
	for _, payload := range []string{`{}`, `{"data":[]}`, `{"data":"invalid"}`} {
		t.Run("rejects a missing or empty strict catalog/"+payload, func(t *testing.T) {
			_, err := loadOpenRouterFixture(t, payload)
			if err == nil || !strings.Contains(err.Error(), "missing or empty image model list") {
				t.Fatalf("error = %v", err)
			}
		})
	}
	// .upstream/v0.87.1/packages/ai/test/image-model-data.test.ts:22
	t.Run("rejects a strict catalog with no usable image models", func(t *testing.T) {
		payload := `{"data":[` + strings.Replace(validOpenRouterImageModel, `"output_modalities":["image"]`, `"output_modalities":["text"]`, 1) + `]}`
		payload = strings.Replace(payload, `"input_modalities":["text","image"]`, `"input_modalities":["text"]`, 1)
		_, err := loadOpenRouterFixture(t, payload)
		if err == nil || !strings.Contains(err.Error(), "no usable image models") {
			t.Fatalf("error = %v", err)
		}
	})
	// .upstream/v0.87.1/packages/ai/test/image-model-data.test.ts:38
	t.Run("parses a non-empty image model catalog", func(t *testing.T) {
		models, err := loadOpenRouterFixture(t, `{"data":[`+validOpenRouterImageModel+`]}`)
		if err != nil {
			t.Fatal(err)
		}
		if len(models) != 1 || models[0].ID != "example/image-model" || !reflect.DeepEqual(models[0].Input, []string{"text", "image"}) || !reflect.DeepEqual(models[0].Output, []string{"image"}) {
			t.Fatalf("models = %#v", models)
		}
		model := models[0]
		if model.Name != "Example Image Model" || model.API != "openrouter-images" || model.Provider != "openrouter" || model.BaseURL != "https://openrouter.ai/api/v1" || model.Cost.Input != 1 || model.Cost.Output != 2 || model.Cost.CacheRead != 0 || model.Cost.CacheWrite != 0 {
			t.Fatalf("metadata = %#v", model)
		}
	})
}

func TestImageModelParserNumericPrefixes(t *testing.T) {
	for _, test := range []struct {
		value string
		want  float64
	}{{"", 0}, {" 1e-6 dollars", 1}, {".000002", 2}, {"0x10", 0}, {"\ufeff3e-6", 3}, {"Infinity", math.Inf(1)}, {"n/a", math.NaN()}} {
		got := imageModelPrice(test.value)
		if got != test.want && !(math.IsNaN(got) && math.IsNaN(test.want)) {
			t.Errorf("price(%q) = %v, want %v", test.value, got, test.want)
		}
	}
}

func TestOpenRouterImageCatalogParity(t *testing.T) {
	data, err := os.ReadFile("../../test/parity/testdata/image-model-data.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Payload json.RawMessage `json:"payload"`
		Strict  bool            `json:"strict"`
	}
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	for _, test := range cases {
		models, err := parseOpenRouterImageModels(test.Payload, test.Strict)
		result := map[string]any{"models": models}
		if err != nil {
			result = map[string]any{"error": err.Error()}
		}
		encoded, err := json.Marshal(result)
		if err != nil {
			t.Fatal(err)
		}
		if os.Getenv("PIG_PARITY_PROBE") == "1" {
			fmt.Printf("IMAGE_PARSE %s\n", encoded)
		}
	}
}
