package extension

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

type modelObjectIDProvider struct{ ai.Provider }

func (modelObjectIDProvider) ID() string { return "from-provider" }

func modelObjectString(t *testing.T, model ai.AnyModel) string {
	t.Helper()
	data, err := ModelObject(model)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// A model that no catalog record describes is Pi's composed model object: its members in provider-composer.ts:230-248's order,
// absent members left out, and none of PiG's own (no modelId, displayName or flat prices).
func TestModelObjectOfAComposedModelHasPisMembersInPisOrder(t *testing.T) {
	low := "low"
	model := &ai.Model{
		ID: "every-member", DisplayName: "Every <member> & more",
		ProviderMeta:     ai.ProviderMetadata{ProviderID: "custom", API: ai.APIOpenAICompletions, BaseURL: "https://example.test/v1", Reasoning: true, Compat: &ai.ModelCompat{MaxTokensField: "max_tokens"}},
		Capabilities:     ai.ModelCapabilities{ContextWindow: 200000, MaxOutputTokens: 8192, InputCostPer1M: 0.1, OutputCostPer1M: 1, CacheReadCostPer1M: 0.01, CacheWriteCostPer1M: 3},
		Input:            []string{"text", "image"},
		ThinkingLevelMap: ai.ThinkingLevelMap{"high": &low},
	}
	want := `{"id":"every-member","name":"Every <member> & more","api":"openai-completions","provider":"custom","baseUrl":"https://example.test/v1","reasoning":true,"thinkingLevelMap":{"high":"low"},"input":["text","image"],"cost":{"input":0.1,"output":1,"cacheRead":0.01,"cacheWrite":3},"contextWindow":200000,"maxTokens":8192,"compat":{"maxTokensField":"max_tokens"}}`
	if got := modelObjectString(t, model); got != want {
		t.Fatalf("model object = %s\nwant           %s", got, want)
	}
}

// A chat model with no provider id reads its provider's, one with no input modalities reads as Pi's composer default, and a chat
// model carries the type its entry sets and no other.
func TestModelObjectDefaults(t *testing.T) {
	got := modelObjectString(t, &ai.Model{ID: "m", Provider: modelObjectIDProvider{}, Capabilities: ai.ModelCapabilities{SupportsImages: true}})
	if !strings.Contains(got, `"provider":"from-provider"`) || !strings.Contains(got, `"input":["text","image"]`) {
		t.Fatalf("model object = %s, want the provider's id and text and image input", got)
	}
	if got := modelObjectString(t, &ai.Model{ID: "gpt-4o", Type: ai.ModelTypeChat, Input: []string{"text"}}); !strings.HasPrefix(got, `{"type":"chat",`) {
		t.Fatalf("typed chat model = %s", got)
	}
	if got := modelObjectString(t, &ai.Model{ID: "custom", Input: []string{"text"}}); strings.Contains(got, `"type"`) {
		t.Fatalf("a model without a type gained one: %s", got)
	}
	for _, model := range []ai.AnyModel{nil, (*ai.Model)(nil), (*ai.ImageModel)(nil), (*ai.ClassifierModel)(nil)} {
		if got := modelObjectString(t, model); got != "" {
			t.Fatalf("nil model %T = %s, want none", model, got)
		}
	}
}

// packages/ai/src/types.ts ImageModel and ClassifierModel: an extension reads a non-chat model with its type, its own API, its
// provider and the fields of its variant, and nothing of a chat model's.
func TestModelObjectOfImageAndClassifierModels(t *testing.T) {
	image := &ai.ImageModel{ID: "flux", Name: "Flux", API: "openrouter-images", Provider: "openrouter", BaseURL: "https://openrouter.ai/api/v1", Input: []string{"text", "image"}, Output: []string{"image", "text"}, Headers: map[string]string{"X-Title": "pi"}, Cost: ai.ModelCost{Input: 1, Output: 2, CacheRead: 3, CacheWrite: 4}}
	if got, want := modelObjectString(t, image), `{"type":"image","id":"flux","name":"Flux","api":"openrouter-images","provider":"openrouter","baseUrl":"https://openrouter.ai/api/v1","input":["text","image"],"cost":{"input":1,"output":2,"cacheRead":3,"cacheWrite":4},"headers":{"X-Title":"pi"},"output":["image","text"]}`; got != want {
		t.Fatalf("image = %s\nwant    %s", got, want)
	}
	classifier := &ai.ClassifierModel{ID: "jev-latest", Name: "Jev", API: "typesafe-system-one", Provider: "typesafe", BaseURL: "https://api.typesafe.ai/v1/", Input: []string{"text"}, ContextWindow: 64000}
	if got, want := modelObjectString(t, classifier), `{"type":"classifier","id":"jev-latest","name":"Jev","api":"typesafe-system-one","provider":"typesafe","baseUrl":"https://api.typesafe.ai/v1/","input":["text"],"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0},"contextWindow":64000}`; got != want {
		t.Fatalf("classifier = %s\nwant         %s", got, want)
	}
}

// upstream: models.ts:440-471 hands the provider object's model objects on as they are, so a model whose record has
// cost.tiers: [] shows it to extensions; a model with no tiers has none.
func TestModelObjectKeepsARecordsEmptyTiers(t *testing.T) {
	for _, tc := range []struct {
		record, want string
	}{
		{`{"id":"m","name":"M","api":"openai-completions","provider":"p","baseUrl":"https://example.test","reasoning":false,"input":["text"],"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"tiers":[]},"contextWindow":1,"maxTokens":1}`, `"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"tiers":[]}`},
		{`{"id":"m","name":"M","api":"openai-completions","provider":"p","baseUrl":"https://example.test","reasoning":false,"input":["text"],"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0},"contextWindow":1,"maxTokens":1}`, `"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0},`},
	} {
		models, err := ai.DecodeStoredModels([]json.RawMessage{json.RawMessage(tc.record)})
		if err != nil || len(models) != 1 {
			t.Fatalf("decode %s: %v", tc.record, err)
		}
		listed, err := MarshalModelObjects(models)
		if err != nil {
			t.Fatal(err)
		}
		for name, got := range map[string]string{"ModelObject": modelObjectString(t, models[0]), "MarshalModelObjects": string(listed)} {
			if !strings.Contains(got, tc.want) {
				t.Errorf("%s = %s, want it to contain %s", name, got, tc.want)
			}
		}
	}
}

// MarshalModelObjects joins the objects as they are, skipping nil models.
func TestMarshalModelObjects(t *testing.T) {
	models := []*ai.Model{{ID: "a", Input: []string{"text"}}, nil, {ID: "b", Input: []string{"text"}}}
	got, err := MarshalModelObjects(models)
	if err != nil {
		t.Fatal(err)
	}
	want := "[" + modelObjectString(t, models[0]) + "," + modelObjectString(t, models[2]) + "]"
	if string(got) != want {
		t.Fatalf("MarshalModelObjects = %s\nwant %s", got, want)
	}
	if empty, _ := MarshalModelObjects([]*ai.Model{}); string(empty) != "[]" {
		t.Fatalf("no models = %s, want []", empty)
	}
}
