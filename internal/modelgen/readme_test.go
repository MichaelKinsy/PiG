package modelgen

// pi: packages/ai/catalog/README.md

import "testing"

// The README's "Adding a provider" example: a models.dev provider served on one API, with the compat defaults of that API. The
// README says modelsDevModels keeps tool-capable chat models and maps names, input modalities, costs (including context tiers),
// and limits, and that compat defaults for the API are merged under each model.
func TestReadmeAddingAProviderExample(t *testing.T) {
	definition := ProviderDefinition{
		ID: "example",
		Models: func(ctx *ProviderContext) ([]ChatModel, error) {
			return ModelsDevModels(ctx, "example", ModelsDevOptions{API: "openai-completions", BaseURL: "https://api.example.com/v1"})
		},
		Compat: ApiCompatDefaults{"openai-completions": FixedCompat(O("supportsStore", false, "supportsDeveloperRole", false))},
	}
	sources := &CatalogSources{ModelsDev: parseObj(t, `{"example":{"models":{
		"vision":{"id":"vision","name":"Vision","type":"chat","capabilities":{"tools":{"supported":true}},"modalities":{"input":["text","image"],"output":["text"]},
			"api":{"chat":{"base_url":"https://api.example.com/v1"}},"limit":{"context":64000,"output":8000},
			"cost":{"text":{"input":{"price":1,"overrides":[{"when":{"context":{"gt":32000}},"price":2}]},"output":{"price":3}}}},
		"plain":{"id":"plain","type":"chat","capabilities":{"tools":{"supported":true}},"modalities":{"input":["text"],"output":["text"]},"api":{"chat":{"base_url":"https://api.example.com/v1"}}},
		"embed":{"id":"embed","name":"Embed","type":"embedding","capabilities":{"tools":{"supported":true}}},
		"notools":{"id":"notools","name":"No tools","type":"chat","capabilities":{"tools":{"supported":false}},"api":{"chat":{"base_url":"https://api.example.com/v1"}}}}}}`)}
	catalogs, err := BuildCatalog([]ProviderDefinition{definition}, sources, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(catalogs) != 1 || len(catalogs[0].Chat) != 2 {
		t.Fatalf("catalogs = %+v", catalogs)
	}
	for _, model := range catalogs[0].Chat {
		requireJSON(t, model.Object("compat"), `{"supportsStore":false,"supportsDeveloperRole":false}`)
	}
	vision, plain := catalogs[0].Chat[0], catalogs[0].Chat[1]
	if vision.Str("id") != "vision" || plain.Str("id") != "plain" {
		t.Fatalf("models = %s, %s", Stringify(vision, ""), Stringify(plain, ""))
	}
	requireJSON(t, vision.Object("cost"), `{"input":1,"output":3,"cacheRead":0,"cacheWrite":0,"tiers":[{"inputTokensAbove":32000,"input":2,"output":3,"cacheRead":0,"cacheWrite":0}]}`)
	requireJSON(t, vision.List("input"), `["text","image"]`)
	requireJSON(t, plain.List("input"), `["text"]`)
	if vision.Str("name") != "Vision" || plain.Str("name") != "plain" || vision.Num("contextWindow") != 64000 || vision.Num("maxTokens") != 8000 || plain.Num("contextWindow") != 4096 || plain.Num("maxTokens") != 4096 {
		t.Fatalf("names and limits: %s, %s", Stringify(vision, ""), Stringify(plain, ""))
	}
}
