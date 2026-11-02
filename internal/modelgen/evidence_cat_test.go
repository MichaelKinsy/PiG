package modelgen

import (
	"maps"
	"slices"
	"strings"
	"testing"
)

func catEvidenceContext(t testing.TB, recorded *[]string) *ProviderContext {
	return &ProviderContext{
		ProviderID: "q",
		Sources: &CatalogSources{ModelsDev: parseObj(t, `{"p":{"models":{
			"dep":{"id":"dep","name":"Dep","type":"chat","status":"deprecated","capabilities":{"tools":{"supported":true}},"api":{"responses":{"base_url":"https://r"}},"limit":{"context":1000,"output":100}},
			"live":{"id":"live","type":"chat","capabilities":{"tools":{"supported":true}},"api":{"chat":{"base_url":"https://c"}}},
			"skip":{"id":"skip","name":"Skip","type":"chat","capabilities":{"tools":{"supported":true}},"api":{"responses":{"base_url":"https://r"}}},
			"notool":{"id":"notool","type":"chat","capabilities":{"tools":{"supported":false}},"api":{"responses":{"base_url":"https://r"}}}}}}`)},
		recordModelsDevSrc: func(model ChatModel, _ *ModelsDevModel) { *recorded = append(*recorded, model.Str("id")) },
	}
}

func catEvidenceIDs(models []ChatModel) []string {
	ids := []string{}
	for _, model := range models {
		ids = append(ids, model.Str("id"))
	}
	return ids
}

func TestDefineModelsDevModelsOptionsMatchPi(t *testing.T) {
	run := func(options ModelsDevOptions) ([]ChatModel, []string) {
		var recorded []string
		options.API, options.BaseURL = "openai-responses", "https://q.test"
		models, err := ModelsDevModels(catEvidenceContext(t, &recorded), "p", options)
		if err != nil {
			t.Fatal(err)
		}
		return models, recorded
	}
	models, recorded := run(ModelsDevOptions{})
	if got := catEvidenceIDs(models); !slices.Equal(got, []string{"dep", "live", "skip"}) || !slices.Equal(recorded, got) {
		t.Fatalf("ids = %v recorded = %v", got, recorded)
	}
	// Fallback limits: models.dev limits win, then the option defaults, then 4096.
	requireJSON(t, models[0].Object("cost"), `{"input":0,"output":0,"cacheRead":0,"cacheWrite":0}`)
	if models[0].Num("contextWindow") != 1000 || models[0].Num("maxTokens") != 100 || models[1].Num("contextWindow") != 4096 || models[1].Num("maxTokens") != 4096 {
		t.Fatalf("limits = %s %s", Stringify(models[0], ""), Stringify(models[1], ""))
	}
	if models[1].Str("name") != "live" {
		t.Fatalf("name falls back to the id: %s", Stringify(models[1], ""))
	}
	models, _ = run(ModelsDevOptions{DefaultContextWindow: 8000, DefaultMaxTokens: 800})
	if models[0].Num("contextWindow") != 1000 || models[1].Num("contextWindow") != 8000 || models[1].Num("maxTokens") != 800 {
		t.Fatalf("defaults = %s %s", Stringify(models[0], ""), Stringify(models[1], ""))
	}
	if models, _ = run(ModelsDevOptions{SkipDeprecated: true}); !slices.Equal(catEvidenceIDs(models), []string{"live", "skip"}) {
		t.Fatalf("skipDeprecated ids = %v", catEvidenceIDs(models))
	}
	if models, _ = run(ModelsDevOptions{Protocol: "responses"}); !slices.Equal(catEvidenceIDs(models), []string{"dep", "skip"}) {
		t.Fatalf("protocol ids = %v", catEvidenceIDs(models))
	}
	if models, _ = run(ModelsDevOptions{Include: func(source *ModelsDevModel) bool { return source.ID != "skip" }}); !slices.Equal(catEvidenceIDs(models), []string{"dep", "live"}) {
		t.Fatalf("include ids = %v", catEvidenceIDs(models))
	}
	// map may replace the model or return undefined to skip it; recordReasoning false records nothing.
	var recorded2 []string
	models, recorded2 = run(ModelsDevOptions{NoRecordReasoning: true, Headers: O("h", "1"), Compat: O("c", 1), Map: func(model ChatModel, source *ModelsDevModel) (ChatModel, error) {
		if source.ID == "live" {
			return nil, nil
		}
		return model.Set("mapped", true), nil
	}})
	if !slices.Equal(catEvidenceIDs(models), []string{"dep", "skip"}) || len(recorded2) != 0 {
		t.Fatalf("map ids = %v recorded = %v", catEvidenceIDs(models), recorded2)
	}
	requireJSON(t, models[0], `{"id":"dep","name":"Dep","api":"openai-responses","provider":"q","baseUrl":"https://q.test","reasoning":false,"input":["text"],
		"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0},"contextWindow":1000,"maxTokens":100,"headers":{"h":"1"},"compat":{"c":1},"mapped":true}`)
}

func TestDefineWithInputLimitsAndAddMissingMatchPi(t *testing.T) {
	limits := O("maxFiles", 5, "images", O("maxBytes", 100, "maxWidth", 50))
	image := O("input", Strings("text", "image"), "inputLimits", O("maxFiles", 9, "images", O("maxBytes", 7)))
	WithInputLimits(image, limits)
	requireJSON(t, image.Object("inputLimits"), `{"maxFiles":9,"images":{"maxBytes":7,"maxWidth":50}}`)
	plain := O("input", Strings("text"))
	WithInputLimits(plain, limits)
	requireJSON(t, plain, `{"input":["text"]}`)
	fresh := O("input", Strings("text", "image"))
	WithInputLimits(fresh, limits)
	requireJSON(t, fresh.Object("inputLimits"), `{"maxFiles":5,"images":{"maxBytes":100,"maxWidth":50}}`)

	models := []ChatModel{O("id", "a", "n", 1)}
	got := AddMissing(models, []ChatModel{O("id", "a", "n", 2), O("id", "b", "n", 3), O("id", "b", "n", 4)})
	if ids := catEvidenceIDs(got); !slices.Equal(ids, []string{"a", "b"}) || got[0].Num("n") != 1 || got[1].Num("n") != 3 {
		t.Fatalf("AddMissing = %v", ids)
	}
	// FixedCompat hands every model its own copy.
	compat := FixedCompat(O("x", 1))
	first, second := compat(nil), compat(nil)
	first.Set("x", 2)
	requireJSON(t, second, `{"x":1}`)
}

func TestFamilyAnthropicSonnet46HasMaxEffortOnlyAndAdaptiveThinking(t *testing.T) {
	for _, id := range []string{"claude-sonnet-4-6", "claude-sonnet-4.6"} {
		model := fakeModel("anthropic", id, "anthropic-messages")
		applyClaudeFamilyRules(model)
		requireJSON(t, model.Object("thinkingLevelMap"), `{"max":"max"}`)
		requireJSON(t, model.Object("compat"), `{"forceAdaptiveThinking":true}`)
	}
}

func TestFamilyOpenAITablesMatchPi(t *testing.T) {
	want := map[string]string{
		"gpt-5.6-luna":  `{"input":0.2,"output":1.2,"cacheRead":0.02,"cacheWrite":0.25}`,
		"gpt-5.6-sol":   `{"input":4,"output":20,"cacheRead":0.4,"cacheWrite":5}`,
		"gpt-5.6-terra": `{"input":2,"output":12,"cacheRead":0.2,"cacheWrite":2.5}`,
		"gpt-6-astra":   `{"input":10,"output":50,"cacheRead":1,"cacheWrite":12.5}`,
		"gpt-6-luna":    `{"input":0.1,"output":0.5,"cacheRead":0.01,"cacheWrite":0.125}`,
		"gpt-6-sol":     `{"input":2,"output":10,"cacheRead":0.2,"cacheWrite":2.5}`,
		"gpt-6.1-sol":   `{"input":2,"output":10,"cacheRead":0.1,"cacheWrite":2.5}`,
		"gpt-6.1-terra": `{"input":1,"output":5,"cacheRead":0.05,"cacheWrite":1.25}`,
	}
	costs := OpenAIStandardCosts()
	if len(costs) != len(want) {
		t.Fatalf("%d standard costs, want %d", len(costs), len(want))
	}
	for id, cost := range want {
		requireJSON(t, costs[id], cost)
	}
	toolSearch := []string{"gpt-5.6-sol", "gpt-5.6-terra", "gpt-5.6-luna", "gpt-6-astra", "gpt-6-sol", "gpt-6-luna", "gpt-6.1-sol", "gpt-6.1-terra"}
	if len(OpenAIToolSearchModelIDs) != len(toolSearch) {
		t.Fatalf("tool search ids = %v", OpenAIToolSearchModelIDs)
	}
	for _, id := range toolSearch {
		if !OpenAIToolSearchModelIDs[id] || !OpenAIMidConvoModelIDs[id] {
			t.Errorf("%s missing from the tool search / mid-convo sets", id)
		}
	}
	if len(OpenAIMidConvoModelIDs) != len(toolSearch)+4 {
		t.Fatalf("mid-convo ids = %v", OpenAIMidConvoModelIDs)
	}
	for _, id := range []string{"gpt-5.4", "gpt-5.4-mini", "gpt-5.4-pro", "gpt-5.5"} {
		if !OpenAIMidConvoModelIDs[id] {
			t.Errorf("%s missing from the mid-convo set", id)
		}
	}
}

// fetch's response.ok is a status in 200..299: 300 is a failure, and the Radius error body is cut at 512 UTF-16 units.
func TestLoadCatalogSourcesStatusBoundaryAndRadiusBodyTruncation(t *testing.T) {
	for _, tc := range []struct {
		name    string
		fail    map[string]fakeResponse
		wantErr string
	}{
		{"models.dev 300", map[string]fakeResponse{ModelsDevURL: {300, ``}}, "models.dev API returned 300"},
		{"models.dev 299 is ok", map[string]fakeResponse{ModelsDevURL: {299, `{}`}}, ""},
		{"radius 512 units are kept whole", map[string]fakeResponse{"https://radius.pi.dev/v1/config": {500, " " + strings.Repeat("a", 512) + " "}},
			"Could not load Radius config from https://radius.pi.dev: 500: " + strings.Repeat("a", 512)},
		{"radius 513 units are cut at 512", map[string]fakeResponse{"https://radius.pi.dev/v1/config": {500, strings.Repeat("a", 513)}},
			"Could not load Radius config from https://radius.pi.dev: 500: " + strings.Repeat("a", 512) + "…"},
		{"radius 300", map[string]fakeResponse{"https://radius.pi.dev/v1/config": {300, "x"}}, "Could not load Radius config from https://radius.pi.dev: 300: x"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			responses := healthySources()
			maps.Copy(responses, tc.fail)
			serveSources(t, responses)
			captureStderr(t, func() {
				_, err := LoadCatalogSources(true)
				if tc.wantErr == "" && err != nil || tc.wantErr != "" && (err == nil || err.Error() != tc.wantErr) {
					t.Errorf("strict error = %v, want %q", err, tc.wantErr)
				}
			})
		})
	}
}
