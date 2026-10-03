package extension_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// Upstream types.ts:1929-1991 (ProviderChatModelConfig, ProviderImageModelConfig, ProviderClassifierModelConfig, ProviderModelConfig): an entry round-trips with exactly its variant's fields. `type` is kept as the extension wrote it (omitted stays omitted; the registry normalizes it to "chat").
func TestProviderModelConfigRoundTripsEachVariantWithItsOwnFields(t *testing.T) {
	cost := `"cost":{"input":1,"output":2,"cacheRead":0.1,"cacheWrite":1.25}`
	for name, input := range map[string]string{
		"chat without a type":          `{"id":"chat-1","name":"Chat","reasoning":true,"input":["text"],` + cost + `,"contextWindow":128000,"maxTokens":4096}`,
		"chat with a type":             `{"id":"chat-2","name":"Chat","type":"chat","reasoning":false,"input":["text","image"],` + cost + `,"contextWindow":1000,"maxTokens":100,"samplingParams":{"top_k":5}}`,
		"image":                        `{"id":"img-1","name":"Image","type":"image","api":"openrouter-images","baseUrl":"https://images.test","input":["text"],` + cost + `,"output":["image","text"],"headers":{"x-a":"b"}}`,
		"classifier":                   `{"id":"cls-1","name":"Classifier","type":"classifier","api":"llama-cpp-classify","input":["text"],` + cost + `,"contextWindow":8192}`,
		"image with image output only": `{"id":"img-2","name":"Image","type":"image","input":["text"],` + cost + `,"output":["image"]}`,
	} {
		t.Run(name, func(t *testing.T) {
			var model extension.ProviderModelConfig
			if err := json.Unmarshal([]byte(input), &model); err != nil {
				t.Fatal(err)
			}
			out, err := json.Marshal(model)
			if err != nil {
				t.Fatal(err)
			}
			var got, want map[string]any
			if err := json.Unmarshal(out, &got); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal([]byte(input), &want); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("round trip\n got  %s\n want %s", out, input)
			}
		})
	}
}

// Upstream types.ts:1953-1991: the discriminator and the variant fields decode into the typed struct.
func TestProviderModelConfigDecodesTypeAndVariantFields(t *testing.T) {
	var image extension.ProviderModelConfig
	if err := json.Unmarshal([]byte(`{"id":"img","name":"Image","type":"image","api":"openrouter-images","input":["text"],"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0},"output":["image","text"]}`), &image); err != nil {
		t.Fatal(err)
	}
	if image.Type != ai.ModelTypeImage || image.API != "openrouter-images" || !reflect.DeepEqual(image.Output, []string{"image", "text"}) {
		t.Fatalf("image = %+v", image)
	}
	var classifier extension.ProviderModelConfig
	if err := json.Unmarshal([]byte(`{"id":"cls","name":"Classifier","type":"classifier","input":["text"],"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0},"contextWindow":4096}`), &classifier); err != nil {
		t.Fatal(err)
	}
	if classifier.Type != ai.ModelTypeClassifier || classifier.ContextWindow != 4096 {
		t.Fatalf("classifier = %+v", classifier)
	}
}
