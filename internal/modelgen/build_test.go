package modelgen

// pi: packages/ai/catalog/build.ts
// No upstream test covers the build; the cases pin its order of steps on tiny fake provider definitions.

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

func fakeModel(provider, id, api string) ChatModel {
	return O(
		"id", id,
		"name", id,
		"api", api,
		"provider", provider,
		"baseUrl", "https://"+provider+".test",
		"reasoning", false,
		"input", Strings("text"),
		"cost", O("input", 0, "output", 0, "cacheRead", 0, "cacheWrite", 0),
		"contextWindow", 1000,
		"maxTokens", 100,
	)
}

func fakeProvider(id string, models ...ChatModel) ProviderDefinition {
	return ProviderDefinition{ID: id, Models: func(*ProviderContext) ([]ChatModel, error) { return models, nil }}
}

func buildFake(t *testing.T, definitions ...ProviderDefinition) []ProviderCatalog {
	t.Helper()
	catalogs, err := BuildCatalog(definitions, &CatalogSources{}, false)
	if err != nil {
		t.Fatal(err)
	}
	return catalogs
}

func TestBuildCatalogKeepsDefinitionOrderAndDedupesByFirstID(t *testing.T) {
	first, second := fakeModel("a", "dup", "openai-completions"), fakeModel("a", "dup", "openai-completions")
	second.Set("name", "second")
	catalogs := buildFake(t, fakeProvider("a", first, second, fakeModel("a", "other", "openai-completions")), fakeProvider("b"))
	if len(catalogs) != 2 || catalogs[0].ID != "a" || catalogs[1].ID != "b" {
		t.Fatalf("catalogs = %+v", catalogs)
	}
	if got := idsOf(catalogs[0].Chat); !slices.Equal(got, []string{"dup", "other"}) {
		t.Fatalf("chat ids = %v", got)
	}
	if catalogs[0].Chat[0].Str("name") != "dup" {
		t.Fatalf("the first model with an id wins, got name %q", catalogs[0].Chat[0].Str("name"))
	}
	if len(catalogs[1].Chat) != 0 || len(catalogs[1].Image) != 0 || len(catalogs[1].Classifier) != 0 {
		t.Fatalf("a provider without models is empty: %+v", catalogs[1])
	}
}

func TestBuildCatalogCompatDefaultsSitUnderModelCompat(t *testing.T) {
	model := fakeModel("a", "m", "openai-completions")
	model.Set("compat", O("b", 3, "c", 4))
	other := fakeModel("a", "other-api", "anthropic-messages")
	definition := fakeProvider("a", model, other)
	definition.Compat = ApiCompatDefaults{"openai-completions": FixedCompat(O("a", 1, "b", 2))}
	catalogs := buildFake(t, definition)
	// Default keys come first, the model's own values win, and the model's extra keys follow.
	requireJSON(t, catalogs[0].Chat[0].Object("compat"), `{"a":1,"b":3,"c":4}`)
	if catalogs[0].Chat[1].Has("compat") {
		t.Fatalf("a model of an API without defaults gets no compat: %s", Stringify(catalogs[0].Chat[1], ""))
	}
}

func TestBuildCatalogCompatDefaultsAreFreshPerModel(t *testing.T) {
	definition := fakeProvider("a", fakeModel("a", "one", "openai-completions"), fakeModel("a", "two", "openai-completions"))
	definition.Compat = ApiCompatDefaults{"openai-completions": FixedCompat(O("a", 1))}
	definition.Finalize = func(model ChatModel, _ *ProviderContext) {
		if model.Str("id") == "one" {
			model.Object("compat").Set("a", 99)
		}
	}
	catalogs := buildFake(t, definition)
	requireJSON(t, catalogs[0].Chat[1].Object("compat"), `{"a":1}`)
}

func TestBuildCatalogRunsFamilyRulesBeforeFinalizeAndFinalizeBeforeEmptyCompatDeletion(t *testing.T) {
	var sawLevels, sawEmptyCompat []string
	definition := fakeProvider("openai", fakeModel("openai", "gpt-5.2", "openai-responses"), fakeModel("openai", "plain", "openai-responses"))
	definition.Compat = ApiCompatDefaults{"openai-responses": FixedCompat(NewObj())}
	definition.Finalize = func(model ChatModel, _ *ProviderContext) {
		// The shared OpenAI family rule has already merged its levels, and the empty compat object still exists.
		if model.Has("thinkingLevelMap") {
			sawLevels = append(sawLevels, string(Stringify(model.Object("thinkingLevelMap"), "")))
		}
		if compat := model.Object("compat"); compat != nil && compat.Len() == 0 {
			sawEmptyCompat = append(sawEmptyCompat, model.Str("id"))
		}
		if model.Str("id") == "gpt-5.2" {
			// Finalize wins over the shared rules: off keeps its position and takes the new value.
			MergeThinking(model, O("off", "none"))
		}
		if model.Str("id") == "plain" {
			MergeCompat(model, O("flag", true))
		}
	}
	catalogs := buildFake(t, definition)
	if want := []string{`{"off":null,"xhigh":"xhigh"}`}; !slices.Equal(sawLevels, want) {
		t.Fatalf("levels seen by Finalize = %v, want %v", sawLevels, want)
	}
	if !slices.Equal(sawEmptyCompat, []string{"gpt-5.2", "plain"}) {
		t.Fatalf("empty compat seen by Finalize on %v", sawEmptyCompat)
	}
	requireJSON(t, catalogs[0].Chat[0].Object("thinkingLevelMap"), `{"off":"none","xhigh":"xhigh"}`)
	if catalogs[0].Chat[0].Has("compat") {
		t.Fatalf("an empty compat object is deleted after Finalize: %s", Stringify(catalogs[0].Chat[0], ""))
	}
	requireJSON(t, catalogs[0].Chat[1].Object("compat"), `{"flag":true}`)
}

func TestBuildCatalogFamilyRulesRunInSharedOrderAfterCompatDefaults(t *testing.T) {
	// DeepSeek compat rules run before the effort rules: a V4 model gets DeepSeek's compat over the default, and its level map once.
	model := fakeModel("deepseek", "deepseek-v4-flash", "openai-completions")
	definition := fakeProvider("deepseek", model)
	definition.Compat = ApiCompatDefaults{"openai-completions": FixedCompat(O("supportsStore", false, "thinkingFormat", "openai"))}
	catalogs := buildFake(t, definition)
	requireJSON(t, catalogs[0].Chat[0].Object("compat"), `{"supportsStore":false,"thinkingFormat":"deepseek","requiresReasoningContentOnAssistantMessages":true}`)
	requireJSON(t, catalogs[0].Chat[0].Object("thinkingLevelMap"), `{"minimal":null,"low":"low","medium":null,"high":"high","max":"max"}`)
}

func TestBuildCatalogPutsTypeLastForModelsWithoutIt(t *testing.T) {
	typed := fakeModel("a", "typed", "openai-completions")
	typed = Spread(O("type", "chat"), typed)
	catalogs := buildFake(t, fakeProvider("a", fakeModel("a", "untyped", "openai-completions"), typed))
	untypedKeys := catalogs[0].Chat[0].Keys()
	if untypedKeys[len(untypedKeys)-1] != "type" || catalogs[0].Chat[0].Str("type") != "chat" {
		t.Fatalf("untyped keys = %v", untypedKeys)
	}
	// `{ ...model, type: "chat" }` keeps the position of a type the model already has.
	if got := catalogs[0].Chat[1].Keys(); got[0] != "type" || got[len(got)-1] != "maxTokens" {
		t.Fatalf("typed keys = %v", got)
	}
}

func TestBuildCatalogAddsImageResizeDefaults(t *testing.T) {
	vision := fakeModel("a", "vision", "openai-completions")
	vision.Set("input", Strings("text", "image"))
	vision.Set("inputLimits", O("maxRequestBytes", 1000, "images", O("maxPerMessage", 5, "resize", O("maxWidth", 800))))
	textOnly := fakeModel("a", "text", "openai-completions")
	definition := fakeProvider("a", vision, textOnly)
	definition.Images = func(*ProviderContext) ([]*Obj, error) {
		return []*Obj{O("type", "image", "id", "img", "input", Strings("text", "image"))}, nil
	}
	catalogs := buildFake(t, definition)
	requireJSON(t, catalogs[0].Chat[0].Object("inputLimits"), `{"maxRequestBytes":1000,"images":{"maxPerMessage":5,"resize":{"maxWidth":800,"maxHeight":2000,"maxBytes":4718592,"jpegQuality":80}}}`)
	if catalogs[0].Chat[1].Has("inputLimits") {
		t.Fatalf("text-only models get no limits")
	}
	requireJSON(t, catalogs[0].Image[0].Object("inputLimits"), `{"images":{"resize":{"maxWidth":2000,"maxHeight":2000,"maxBytes":4718592,"jpegQuality":80}}}`)
}

func TestBuildCatalogFinalizeAllRunsLast(t *testing.T) {
	definition := fakeProvider("a", fakeModel("a", "one", "openai-completions"))
	definition.Finalize = func(model ChatModel, _ *ProviderContext) { model.Set("order", "finalize") }
	var seen string
	definition.FinalizeAll = func(models []ChatModel, _ *ProviderContext) {
		seen = models[0].Str("order")
		models[0].Set("order", "all")
	}
	catalogs := buildFake(t, definition)
	if seen != "finalize" || catalogs[0].Chat[0].Str("order") != "all" {
		t.Fatalf("seen = %q, order = %q", seen, catalogs[0].Chat[0].Str("order"))
	}
}

func TestProviderModelsReturnsClonesBeforeSharedRules(t *testing.T) {
	var borrowed []ChatModel
	dependent := ProviderDefinition{ID: "b", Models: func(ctx *ProviderContext) ([]ChatModel, error) {
		models, err := ctx.ProviderModels("a")
		if err != nil {
			return nil, err
		}
		borrowed = models
		models[0].Set("name", "changed")
		models[0].Object("cost").Set("input", 42)
		// Dependencies are built on demand, and a built provider is not rebuilt.
		again, err := ctx.ProviderModels("a")
		if err != nil {
			return nil, err
		}
		if again[0].Str("name") != "a1" || again[0] == models[0] {
			t.Errorf("a second call returns fresh clones: %s", Stringify(again[0], ""))
		}
		return []ChatModel{Spread(models[0], O("provider", "b", "id", "b1"))}, nil
	}}
	// "b" is listed first, so its dependency is built on demand.
	catalogs := buildFake(t, dependent, fakeProvider("a", fakeModel("a", "gpt-5.2", "openai-responses").Set("name", "a1")))
	if len(borrowed) != 1 {
		t.Fatalf("ProviderModels returned %d models", len(borrowed))
	}
	if borrowed[0].Has("thinkingLevelMap") {
		t.Fatalf("ProviderModels runs before the shared rules: %s", Stringify(borrowed[0], ""))
	}
	original := catalogs[1].Chat[0]
	if original.Str("name") != "a1" || original.Object("cost").Num("input") != 0 {
		t.Fatalf("the dependency's own model was mutated: %s", Stringify(original, ""))
	}
	if !catalogs[1].Chat[0].Has("thinkingLevelMap") {
		t.Fatalf("a's own model passes the shared rules: %s", Stringify(original, ""))
	}
	if catalogs[0].ID != "b" || catalogs[0].Chat[0].Str("name") != "changed" {
		t.Fatalf("b = %s", Stringify(catalogs[0].Chat[0], ""))
	}
}

func TestBuildCatalogErrors(t *testing.T) {
	t.Run("a provider that depends on itself", func(t *testing.T) {
		definition := ProviderDefinition{ID: "x", Models: func(ctx *ProviderContext) ([]ChatModel, error) { return ctx.ProviderModels("x") }}
		_, err := BuildCatalog([]ProviderDefinition{definition}, &CatalogSources{}, false)
		if err == nil || err.Error() != "Provider x depends on itself" {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("a dependency cycle", func(t *testing.T) {
		depends := func(id, on string) ProviderDefinition {
			return ProviderDefinition{ID: id, Models: func(ctx *ProviderContext) ([]ChatModel, error) { return ctx.ProviderModels(on) }}
		}
		_, err := BuildCatalog([]ProviderDefinition{depends("x", "y"), depends("y", "x")}, &CatalogSources{}, false)
		if err == nil || err.Error() != "Provider x depends on itself" {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("an unknown provider", func(t *testing.T) {
		definition := ProviderDefinition{ID: "x", Models: func(ctx *ProviderContext) ([]ChatModel, error) { return ctx.ProviderModels("nope") }}
		_, err := BuildCatalog([]ProviderDefinition{definition}, &CatalogSources{}, false)
		if err == nil || err.Error() != "Unknown provider: nope" {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("a provider that produces another provider's model", func(t *testing.T) {
		_, err := BuildCatalog([]ProviderDefinition{fakeProvider("p", fakeModel("p2", "id", "openai-completions"))}, &CatalogSources{}, false)
		if err == nil || err.Error() != "p produced p2:id for another provider" {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("a Models, Images or Classifiers error is returned", func(t *testing.T) {
		boom := errors.New("boom")
		for name, definition := range map[string]ProviderDefinition{
			"models":      {ID: "x", Models: func(*ProviderContext) ([]ChatModel, error) { return nil, boom }},
			"images":      {ID: "x", Images: func(*ProviderContext) ([]*Obj, error) { return nil, boom }},
			"classifiers": {ID: "x", Classifiers: func(*ProviderContext) ([]*Obj, error) { return nil, boom }},
		} {
			if _, err := BuildCatalog([]ProviderDefinition{definition}, &CatalogSources{}, false); !errors.Is(err, boom) {
				t.Errorf("%s: err = %v", name, err)
			}
		}
	})
	t.Run("strict reaches the provider context", func(t *testing.T) {
		var strict bool
		definition := ProviderDefinition{ID: "x", Models: func(ctx *ProviderContext) ([]ChatModel, error) {
			strict = ctx.Strict
			if ctx.ProviderID != "x" {
				return nil, errors.New("provider id " + ctx.ProviderID)
			}
			return nil, nil
		}}
		if _, err := BuildCatalog([]ProviderDefinition{definition}, &CatalogSources{}, true); err != nil || !strict {
			t.Fatalf("strict = %v, err = %v", strict, err)
		}
	})
}

func TestModelsDevModelsMapsToolCapableChatModelsAndRecordsSources(t *testing.T) {
	sources := &CatalogSources{ModelsDev: parseObj(t, `{"p":{"models":{
		"tool":{"id":"tool","name":"Tool","type":"chat","capabilities":{"tools":{"supported":true},"reasoning":{"supported":true,"effort":["low","high"]}},"modalities":{"input":["text","image"],"output":["text"]},"limit":{"context":5000,"output":500},"cost":{"text":{"input":{"price":1},"output":{"price":2}}}},
		"notool":{"id":"notool","type":"chat","capabilities":{"tools":{"supported":false}}},
		"image":{"id":"image","type":"image","capabilities":{"tools":{"supported":true}}},
		"nameless":{"id":"nameless","type":"chat","capabilities":{"tools":{"supported":true}}}}}}`)}
	definition := ProviderDefinition{ID: "q", Models: func(ctx *ProviderContext) ([]ChatModel, error) {
		return ModelsDevModels(ctx, "p", ModelsDevOptions{API: "openai-responses", BaseURL: "https://q.test", Compat: O("c", 1)})
	}}
	catalogs, err := BuildCatalog([]ProviderDefinition{definition}, sources, false)
	if err != nil {
		t.Fatal(err)
	}
	if got := idsOf(catalogs[0].Chat); !slices.Equal(got, []string{"tool", "nameless"}) {
		t.Fatalf("ids = %v", got)
	}
	// The recorded models.dev source drives the shared effort rule for Responses models.
	requireJSON(t, catalogs[0].Chat[0], `{"id":"tool","name":"Tool","api":"openai-responses","provider":"q","baseUrl":"https://q.test","reasoning":true,"input":["text","image"],
		"cost":{"input":1,"output":2,"cacheRead":0,"cacheWrite":0},"contextWindow":5000,"maxTokens":500,"compat":{"c":1},
		"thinkingLevelMap":{"off":null,"minimal":null,"low":"low","medium":null,"high":"high","xhigh":null,"max":null},
		"inputLimits":{"images":{"resize":{"maxWidth":2000,"maxHeight":2000,"maxBytes":4718592,"jpegQuality":80}}},"type":"chat"}`)
	if !strings.Contains(string(Stringify(catalogs[0].Chat[1], "")), `"name":"nameless","api":"openai-responses"`) || catalogs[0].Chat[1].Num("contextWindow") != 4096 || catalogs[0].Chat[1].Num("maxTokens") != 4096 {
		t.Fatalf("nameless = %s", Stringify(catalogs[0].Chat[1], ""))
	}
}
