package modelgen

// pi: packages/ai/catalog/sources/openrouter.ts

import (
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"sort"
	"testing"
)

const (
	imageOnly     = `{"id":"example/image-model","name":"Example Image Model","architecture":{"input_modalities":["text","image"],"output_modalities":["image"]},"pricing":{"prompt":"0.000001","completion":"0.000002"}}`
	chatWithImage = `{"id":"example/multimodal","name":"Example Multimodal","supported_parameters":["tools"],"architecture":{"modality":"text+image->text+image","input_modalities":["text","image"],"output_modalities":["text","image"]},"context_length":32000}`
	chatOnly      = `{"id":"example/chat","name":"Example Chat","supported_parameters":["tools"],"architecture":{"modality":"text->text","output_modalities":["text"]}}`
	decisionModel = `{"id":"typesafe/jev-1.13","name":"TypeSafe: Jev 1.13","supported_parameters":[],"architecture":{"modality":"text->decisions","input_modalities":["text"],"output_modalities":["decisions"]},"pricing":{"prompt":"0.000000042","completion":"0"},"context_length":32000,"top_provider":{"context_length":32000,"max_completion_tokens":28800}}`
)

func list(items ...string) []*Obj {
	out := make([]*Obj, len(items))
	for i, item := range items {
		object, err := ParseJSON([]byte(item))
		if err != nil {
			panic(err)
		}
		out[i] = object.(*Obj)
	}
	return out
}

func idsOf(models []*Obj) []string {
	out := []string{}
	for _, model := range models {
		out = append(out, model.Str("id"))
	}
	return out
}

func TestOpenRouterCatalogParsing(t *testing.T) {
	// upstream: packages/ai/test/image-model-data.test.ts:47
	t.Run("emits image-only models from the image listing as image models", func(t *testing.T) {
		catalog := BuildOpenRouterCatalog(nil, list(imageOnly), nil)
		if len(catalog.Chat) != 0 || len(catalog.Images) != 1 {
			t.Fatalf("catalog = %d chat, %d images, want 0 and 1", len(catalog.Chat), len(catalog.Images))
		}
		image := catalog.Images[0]
		requireJSON(t, image.Object("cost"), `{"input":1,"output":2,"cacheRead":0,"cacheWrite":0}`)
		requireJSON(t, O("type", image.Str("type"), "id", image.Str("id"), "api", image.Str("api"), "input", image.List("input"), "output", image.List("output")),
			`{"type":"image","id":"example/image-model","api":"openrouter-images","input":["text","image"],"output":["image"]}`)
		// The full entry, member order included.
		requireJSON(t, image, `{"type":"image","id":"example/image-model","name":"Example Image Model","api":"openrouter-images","provider":"openrouter","baseUrl":"https://openrouter.ai/api/v1","input":["text","image"],"output":["image"],"cost":{"input":1,"output":2,"cacheRead":0,"cacheWrite":0}}`)
	})
	// upstream: packages/ai/test/image-model-data.test.ts:62
	t.Run("emits separate chat and image entries for an id that supports both operations", func(t *testing.T) {
		catalog := BuildOpenRouterCatalog(list(chatWithImage, chatOnly), list(chatWithImage, imageOnly), nil)
		if got := idsOf(catalog.Chat); !slices.Equal(got, []string{"example/multimodal", "example/chat"}) {
			t.Fatalf("chat ids = %v", got)
		}
		for _, model := range catalog.Chat {
			if model.Str("type") != "chat" {
				t.Errorf("chat %s type = %q", model.Str("id"), model.Str("type"))
			}
		}
		if got := idsOf(catalog.Images); !slices.Equal(got, []string{"example/multimodal", "example/image-model"}) {
			t.Fatalf("image ids = %v", got)
		}
		var outputs []any
		for _, model := range catalog.Images {
			if model.Str("type") != "image" {
				t.Errorf("image %s type = %q", model.Str("id"), model.Str("type"))
			}
			outputs = append(outputs, model.List("output"))
		}
		requireJSON(t, outputs, `[["text","image"],["image"]]`)
	})
	// upstream: packages/ai/test/image-model-data.test.ts:71
	t.Run("ignores listed models that neither support tools nor emit images", func(t *testing.T) {
		toolless := Spread(list(chatOnly)[0], O("supported_parameters", []any{}))
		textOnlyImage := Spread(list(imageOnly)[0], O("architecture", O("output_modalities", Strings("text"))))
		textOnlyDecision := Spread(list(decisionModel)[0], O("architecture", O("output_modalities", Strings("text"))))
		catalog := BuildOpenRouterCatalog([]*Obj{toolless}, []*Obj{textOnlyImage}, []*Obj{textOnlyDecision})
		if len(catalog.Chat) != 0 || len(catalog.Images) != 0 || len(catalog.Classifiers) != 0 {
			t.Fatalf("catalog = %d chat, %d images, %d classifiers, want none", len(catalog.Chat), len(catalog.Images), len(catalog.Classifiers))
		}
	})
	// upstream: packages/ai/test/image-model-data.test.ts:82
	t.Run("emits decision models as System One classifier models", func(t *testing.T) {
		catalog := BuildOpenRouterCatalog(nil, nil, list(decisionModel, decisionModel))
		if len(catalog.Chat) != 0 {
			t.Fatalf("chat = %d, want 0", len(catalog.Chat))
		}
		requireJSON(t, catalog.Classifiers, `[{"type":"classifier","id":"typesafe/jev-1.13","name":"TypeSafe: Jev 1.13","api":"typesafe-system-one","provider":"openrouter","baseUrl":"https://openrouter.ai/api/v1","input":["text"],"cost":{"input":0.042,"output":0,"cacheRead":0,"cacheWrite":0},"contextWindow":32000,"compat":{"imageInput":"state-image-urls","sendImageOptInHeader":false}}]`)
	})
}

func TestGetOpenRouterThinkingLevelMap(t *testing.T) {
	cases := []struct{ name, input, want string }{
		// upstream: packages/ai/test/openrouter-reasoning-options.test.ts:43
		{"marks mandatory reasoning and unsupported efforts unavailable", `{"mandatory":true,"default_enabled":true,"supported_efforts":["max","high","low"],"default_effort":"max"}`, `{"off":null,"minimal":null,"low":"low","medium":null,"high":"high","xhigh":null,"max":"max"}`},
		// upstream: packages/ai/test/openrouter-reasoning-options.test.ts:62
		{"still marks off unavailable when OpenRouter omits effort metadata", `{"mandatory":true}`, `{"off":null}`},
		// upstream: packages/ai/test/openrouter-reasoning-options.test.ts:66
		{"keeps off available while restricting optional models to supported efforts", `{"mandatory":false,"default_enabled":true,"supported_efforts":["high","low"]}`, `{"off":"none","minimal":null,"low":"low","medium":null,"high":"high","xhigh":null,"max":null}`},
		// upstream: packages/ai/test/openrouter-reasoning-options.test.ts:84
		{"does not add metadata for optional models without effort controls", `{"mandatory":false}`, `null`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var metadata *OpenRouterReasoningMetadata
			if err := json.Unmarshal([]byte(tc.input), &metadata); err != nil {
				t.Fatal(err)
			}
			requireJSON(t, GetOpenRouterThinkingLevelMap(metadata), tc.want)
		})
	}
	// Source cases beyond the upstream test: absent metadata, and efforts with no Pi level.
	t.Run("absent metadata is undefined", func(t *testing.T) {
		requireJSON(t, GetOpenRouterThinkingLevelMap(nil), `null`)
	})
	t.Run("efforts without a Pi level keep only the mandatory off marker", func(t *testing.T) {
		var mandatory, optional *OpenRouterReasoningMetadata
		if err := json.Unmarshal([]byte(`{"mandatory":true,"supported_efforts":["default",null]}`), &mandatory); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal([]byte(`{"mandatory":false,"supported_efforts":["default"]}`), &optional); err != nil {
			t.Fatal(err)
		}
		requireJSON(t, GetOpenRouterThinkingLevelMap(mandatory), `{"off":null}`)
		requireJSON(t, GetOpenRouterThinkingLevelMap(optional), `null`)
	})
	t.Run("the catalog stores the map after the reasoning flag", func(t *testing.T) {
		catalog := BuildOpenRouterCatalog(list(`{"id":"stealth/ox-alpha","name":"Ox","supported_parameters":["tools","reasoning"],"reasoning":{"mandatory":true,"supported_efforts":["max"]}}`), nil, nil)
		requireJSON(t, catalog.Chat[0], `{"type":"chat","id":"stealth/ox-alpha","name":"Ox","api":"openai-completions","baseUrl":"https://openrouter.ai/api/v1","provider":"openrouter","reasoning":true,"thinkingLevelMap":{"off":null,"minimal":null,"low":null,"medium":null,"high":null,"xhigh":null,"max":"max"},"input":["text"],"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0},"contextWindow":4096,"maxTokens":4096}`)
	})
}

// BuildOpenRouterCatalog: Anthropic ids (and the `~anthropic/claude-*-latest` aliases) use OpenRouter's Messages endpoint, except `:batch`;
// window limits come from top_provider before the listing's own length.
func TestBuildOpenRouterCatalogChatEntries(t *testing.T) {
	catalog := BuildOpenRouterCatalog(list(
		`{"id":"anthropic/claude-x","name":"Claude X","supported_parameters":["tools"],"architecture":{"modality":"text+image->text"},"context_length":100,"top_provider":{"context_length":200000,"max_completion_tokens":8000}}`,
		`{"id":"~anthropic/claude-opus-latest","supported_parameters":["tools"]}`,
		`{"id":"anthropic/claude-x:batch","supported_parameters":["tools"]}`,
		`{"id":"vendor/untooled","supported_parameters":["reasoning"]}`,
		`{"id":"vendor/plain","supported_parameters":["tools"],"context_length":50000,"top_provider":{"context_length":0,"max_completion_tokens":null}}`,
	), nil, nil)
	if got := idsOf(catalog.Chat); !slices.Equal(got, []string{"anthropic/claude-x", "~anthropic/claude-opus-latest", "anthropic/claude-x:batch", "vendor/plain"}) {
		t.Fatalf("chat ids = %v", got)
	}
	requireJSON(t, catalog.Chat[0], `{"type":"chat","id":"anthropic/claude-x","name":"Claude X","api":"anthropic-messages","baseUrl":"https://openrouter.ai/api","provider":"openrouter","reasoning":false,"input":["text","image"],"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0},"contextWindow":200000,"maxTokens":8000}`)
	requireJSON(t, catalog.Chat[1], `{"type":"chat","id":"~anthropic/claude-opus-latest","api":"anthropic-messages","baseUrl":"https://openrouter.ai/api","provider":"openrouter","reasoning":false,"input":["text"],"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0},"contextWindow":4096,"maxTokens":4096}`)
	if catalog.Chat[2].Str("api") != "openai-completions" || catalog.Chat[2].Str("baseUrl") != "https://openrouter.ai/api/v1" {
		t.Fatalf(":batch ids use the completions endpoint: %s", Stringify(catalog.Chat[2], ""))
	}
	if catalog.Chat[3].Num("contextWindow") != 50000 || catalog.Chat[3].Num("maxTokens") != 4096 {
		t.Fatalf("plain limits = %s", Stringify(catalog.Chat[3], ""))
	}
}

// modalities(): only text and image survive, each once, in order; no input modality falls back to text.
func TestOpenRouterModalitiesFilterAndDeduplicate(t *testing.T) {
	catalog := BuildOpenRouterCatalog(nil, list(`{"id":"fallback","name":"Fallback","architecture":{"input_modalities":["audio"],"output_modalities":["image","image","text","video","text"]}}`), nil)
	if len(catalog.Images) != 1 {
		t.Fatalf("images = %d, want 1", len(catalog.Images))
	}
	requireJSON(t, catalog.Images[0].List("input"), `["text"]`)
	requireJSON(t, catalog.Images[0].List("output"), `["image","text"]`)
}

// openRouterCost (ee409a85): a negative price takes the fallback, so "-1e-13" and "-0.0000000000001" are 0, and "-0" is not negative and rounds to +0
// (toFixed(6) of -0 is "0.000000"). BuildOpenRouterCatalog writes {"input":0,"output":0,"cacheRead":0,"cacheWrite":0}.
func TestOpenRouterCostSerializesNegativeZeroAsZero(t *testing.T) {
	catalog := BuildOpenRouterCatalog(list(`{"id":"vendor/negative","name":"Negative","supported_parameters":["tools"],"pricing":{"prompt":"-0","completion":"-1e-13","input_cache_read":"-0.0000000000001"}}`), nil, nil)
	requireJSON(t, catalog.Chat[0].Object("cost"), `{"input":0,"output":0,"cacheRead":0,"cacheWrite":0}`)
}

func openRouterChatCost(t *testing.T, pricing string) *Obj {
	t.Helper()
	catalog := BuildOpenRouterCatalog(list(fmt.Sprintf(`{"id":"anthropic/claude-haiku-5.5","name":"Claude Haiku 5.5","supported_parameters":["tools"],"pricing":%s}`, pricing)), nil, nil)
	if len(catalog.Chat) != 1 {
		t.Fatalf("chat models = %d, want 1", len(catalog.Chat))
	}
	return catalog.Chat[0].Object("cost")
}

func TestOpenRouterPricingOverrides(t *testing.T) {
	// upstream: packages/ai/test/model-cost-tiers.test.ts:16
	t.Run("turns prompt-length overrides into tiers", func(t *testing.T) {
		requireJSON(t, openRouterChatCost(t, `{"prompt":"0.0000001","completion":"0.0000005","input_cache_read":"0.00000001","input_cache_write":"0.000000125",
			"overrides":[{"min_prompt_tokens":100000,"prompt":"0.0000005","completion":"0.0000025","input_cache_read":"0.00000005","input_cache_write":"0.000000625"}]}`),
			`{"input":0.1,"output":0.5,"cacheRead":0.01,"cacheWrite":0.125,"tiers":[{"inputTokensAbove":100000,"input":0.5,"output":2.5,"cacheRead":0.05,"cacheWrite":0.625}]}`)
	})
	// upstream: packages/ai/test/model-cost-tiers.test.ts:41
	t.Run("keeps base rates missing from an override", func(t *testing.T) {
		cost := openRouterChatCost(t, `{"prompt":"0.000002","completion":"0.000012","input_cache_read":"0.0000002","input_cache_write":"0.000000375",
			"overrides":[{"min_prompt_tokens":200000,"prompt":"0.000004","completion":"0.000018"}]}`)
		requireJSON(t, cost.List("tiers"), `[{"inputTokensAbove":200000,"input":4,"output":18,"cacheRead":0.2,"cacheWrite":0.375}]`)
	})
	// upstream: packages/ai/test/model-cost-tiers.test.ts:53
	t.Run("skips time-of-day overrides", func(t *testing.T) {
		requireJSON(t, openRouterChatCost(t, `{"prompt":"0.000000132","completion":"0.000000528","overrides":[
			{"utc_start":0,"utc_end":1600,"prompt":"0.000000132","completion":"0.000000528"},
			{"utc_days":["saturday"],"min_prompt_tokens":1000,"prompt":"0.0000001"}]}`),
			`{"input":0.132,"output":0.528,"cacheRead":0,"cacheWrite":0}`)
	})
}

// OpenRouterCost applies Number(x.toFixed(6)) to $/token * 1e6, treats an absent pricing object as free, and reads a number price as its text.
func TestOpenRouterCostPrices(t *testing.T) {
	for _, tc := range []struct{ name, pricing, want string }{
		{"absent", `{}`, `{"input":0,"output":0,"cacheRead":0,"cacheWrite":0}`},
		{"router model reports -1", `{"prompt":"-1","completion":"-1"}`, `{"input":0,"output":0,"cacheRead":0,"cacheWrite":0}`},
		{"parseFloat reads a numeric prefix", `{"prompt":"0.000002xyz","completion":" 0.000004"}`, `{"input":2,"output":4,"cacheRead":0,"cacheWrite":0}`},
		{"a JSON number is read as its text", `{"prompt":0.000001,"completion":2e-6}`, `{"input":1,"output":2,"cacheRead":0,"cacheWrite":0}`},
		{"toFixed(6) rounds to six places", `{"prompt":"0.00000012345678"}`, `{"input":0.123457,"output":0,"cacheRead":0,"cacheWrite":0}`},
		{"a negative tier rate falls back to the base rate", `{"prompt":"0.000002","completion":"0.000012","overrides":[{"min_prompt_tokens":200000,"prompt":"0.000004","completion":"-1"}]}`,
			`{"input":2,"output":12,"cacheRead":0,"cacheWrite":0,"tiers":[{"inputTokensAbove":200000,"input":4,"output":12,"cacheRead":0,"cacheWrite":0}]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requireJSON(t, OpenRouterCost(parseObj(t, tc.pricing)), tc.want)
		})
	}
}

// sortKeys rebuilds a value with every object's members in sorted order, as test/parity/testdata/openrouter-catalog.mjs does for Pi's output.
func sortKeys(value any) any {
	switch v := value.(type) {
	case *Obj:
		keys := v.Keys()
		sort.Strings(keys)
		sorted := NewObj()
		for _, key := range keys {
			member, _ := v.Get(key)
			sorted.Set(key, sortKeys(member))
		}
		return sorted
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = sortKeys(item)
		}
		return out
	}
	return value
}

// TestOpenRouterCatalogParity prints the catalog for every case of the parity oracle input. The
// 16-image-model-data scenario compares these lines with buildOpenRouterCatalog from the pinned upstream catalog.
func TestOpenRouterCatalogParity(t *testing.T) {
	data, err := os.ReadFile("../../test/parity/testdata/openrouter-catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	cases, ok := parse(t, string(data)).([]any)
	if !ok {
		t.Fatal("openrouter-catalog.json is not an array")
	}
	for _, item := range cases {
		test := item.(*Obj)
		listing := func(key string) []*Obj {
			var out []*Obj
			for _, entry := range test.List(key) {
				out = append(out, entry.(*Obj))
			}
			return out
		}
		catalog := BuildOpenRouterCatalog(listing("listed"), listing("imageListed"), listing("decisionListed"))
		encoded := Stringify(sortKeys(O("chat", anyList(catalog.Chat), "images", anyList(catalog.Images), "classifiers", anyList(catalog.Classifiers))), "")
		if os.Getenv("PIG_PARITY_PROBE") == "1" {
			fmt.Printf("OPENROUTER_CATALOG %s\n", encoded)
		}
	}
}

func anyList(models []*Obj) []any {
	out := make([]any, len(models))
	for i, model := range models {
		out[i] = model
	}
	return out
}
