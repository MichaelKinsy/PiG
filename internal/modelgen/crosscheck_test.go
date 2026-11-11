package modelgen_test

// Cross-checks of the generator with the production ai package. modelgen itself never imports ai; only this external test package does.

import (
	"encoding/json"
	"math"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/modelgen"
	"github.com/MichaelKinsy/PiG/internal/modelgen/providers"
)

func mustParseObj(t *testing.T, text string) *modelgen.Obj {
	t.Helper()
	value, err := modelgen.ParseJSON([]byte(text))
	if err != nil {
		t.Fatal(err)
	}
	object, ok := value.(*modelgen.Obj)
	if !ok {
		t.Fatalf("%s is not an object", text)
	}
	return object
}

func sameFloat(a, b float64) bool { return math.Float64bits(a) == math.Float64bits(b) }

// The cases are those the ai package checks against Pi's openRouterCost (ai/openrouter_cost_oracle_test.go, ai/openrouter_api_test.go).
func TestOpenRouterCostMatchesAI(t *testing.T) {
	prices := []string{
		`{}`,
		`{"prompt":"0.000003","completion":"0.000015","input_cache_read":"0.0000003","input_cache_write":"0.00000375"}`,
		`{"prompt":"-1","completion":"-1"}`,
		`{"prompt":"0","completion":"0"}`,
		`{"prompt":"abc","completion":"","input_cache_read":null,"input_cache_write":"-0.5"}`,
		`{"prompt":"abc","completion":"","input_cache_read":null,"input_cache_write":true}`,
		`{"prompt":"0.000002xyz","completion":" 0.000004","input_cache_read":"\n0.000001"}`,
		`{"prompt":0.000001,"completion":2e-6,"input_cache_read":1e-7}`,
		`{"prompt":"0.00000012345678","completion":"0.0000004999995","input_cache_read":"0.0000004999994"}`,
		`{"prompt":"Infinity","completion":"-Infinity","input_cache_read":"NaN"}`,
		`{"prompt":"1e-6","completion":"1E-5","input_cache_read":".5e-5","input_cache_write":"+0.000001"}`,
		`{"prompt":"0x10","completion":"1_000","input_cache_read":"0.5e","input_cache_write":"--1"}`,
		`{"prompt":"1e15","completion":"123456789012345.5"}`,
		`{"prompt":"-0","completion":"-0.0000000001"}`,
		`{"prompt":true,"completion":false,"input_cache_read":{},"input_cache_write":[]}`,
		`{"prompt":"0.000002","completion":"0.000012","overrides":[{"min_prompt_tokens":200000,"prompt":"0.000004"}]}`,
		`{"prompt":"0.000002","completion":"0.000012","input_cache_read":"0.0000002","overrides":[{"min_prompt_tokens":200000,"prompt":"0.000004","completion":"-1","input_cache_read":"abc","input_cache_write":"0.000005"}]}`,
		`{"prompt":"0.000002","completion":"0.000012","overrides":[{"min_prompt_tokens":200000,"prompt":"0.000004","completion":"-1"}]}`,
		`{"prompt":"0.000002","overrides":[{"min_prompt_tokens":1,"utc_start":0,"utc_end":6,"prompt":"0.000001"},{"min_prompt_tokens":2,"utc_days":["sat"],"prompt":"0.000001"},{"min_prompt_tokens":3,"utc_end":1},{"utc_start":1,"prompt":"0.000001"}]}`,
		`{"prompt":"0.000002","overrides":[{"min_prompt_tokens":10,"utc_start":null,"prompt":"0.000009"},{"min_prompt_tokens":11,"utc_days":null,"prompt":"0.000009"},{"min_prompt_tokens":12,"utc_end":null}]}`,
		`{"prompt":"0.000002","overrides":[{"min_prompt_tokens":0,"prompt":"0.000009"},{"min_prompt_tokens":-5},{"min_prompt_tokens":1.5,"completion":"0.00001"},{"prompt":"0.000009"},{}]}`,
		`{"prompt":"0.000002","overrides":[{"min_prompt_tokens":200000,"prompt":"0.000004"},{"min_prompt_tokens":100000,"prompt":"0.000003"}]}`,
		`{"prompt":"0.000002","completion":"0.00001","overrides":[{"min_prompt_tokens":5,"prompt":"0","completion":"0"}]}`,
		`{"prompt":"0.000002","input_cache_write":"0.000003","overrides":[{"min_prompt_tokens":9,"prompt":"0.000004"},{"min_prompt_tokens":10,"input_cache_write":"abc"}]}`,
		`{"prompt":"0.000002","overrides":[]}`,
		`{"prompt":"0.000002","overrides":null}`,
		`{"prompt":"0.000001","completion":"0.000002","overrides":[{"min_prompt_tokens":100000,"prompt":"0.0000005","completion":"0.0000025","input_cache_read":"0.00000005","input_cache_write":"0.000000625"}]}`,
	}
	for _, text := range prices {
		t.Run(text, func(t *testing.T) {
			var pricing ai.OpenRouterPricing
			if err := json.Unmarshal([]byte(text), &pricing); err != nil {
				t.Fatal(err)
			}
			want := ai.OpenRouterCost(&pricing)
			got := modelgen.OpenRouterCost(mustParseObj(t, text))
			for _, f := range []struct {
				name      string
				got, want float64
			}{{"input", got.Num("input"), want.Input}, {"output", got.Num("output"), want.Output}, {"cacheRead", got.Num("cacheRead"), want.CacheRead}, {"cacheWrite", got.Num("cacheWrite"), want.CacheWrite}} {
				if !sameFloat(f.got, f.want) {
					t.Errorf("%s = %v, ai says %v", f.name, f.got, f.want)
				}
			}
			tiers := got.List("tiers")
			if len(tiers) != len(want.Tiers) {
				t.Fatalf("%d tiers, ai says %d", len(tiers), len(want.Tiers))
			}
			for i, tier := range tiers {
				object, wantTier := tier.(*modelgen.Obj), want.Tiers[i]
				for _, f := range []struct {
					name      string
					got, want float64
				}{{"inputTokensAbove", object.Num("inputTokensAbove"), wantTier.InputTokensAbove}, {"input", object.Num("input"), wantTier.InputCostPer1M}, {"output", object.Num("output"), wantTier.OutputCostPer1M}, {"cacheRead", object.Num("cacheRead"), wantTier.CacheReadCostPer1M}, {"cacheWrite", object.Num("cacheWrite"), wantTier.CacheWriteCostPer1M}} {
					if !sameFloat(f.got, f.want) {
						t.Errorf("tier %d %s = %v, ai says %v", i, f.name, f.got, f.want)
					}
				}
			}
		})
	}
}

// The Cloudflare base URLs are copies of packages/ai/src/api/cloudflare.ts constants; the generated models must carry the ones ai resolves.
func TestCloudflareBaseURLsMatchAI(t *testing.T) {
	const chat = `"type":"chat","capabilities":{"tools":{"supported":true}},"modalities":{"input":["text"],"output":["text"]},"limit":{"context":1000,"output":100}`
	sources := &modelgen.CatalogSources{ModelsDev: mustParseObj(t, `{
		"cloudflare-workers-ai":{"models":{"@cf/own":{"id":"@cf/own","name":"Own",`+chat+`}}},
		"cloudflare-ai-gateway":{"models":{
			"openai/gpt-5.5":{"id":"openai/gpt-5.5","name":"GPT",`+chat+`},
			"anthropic/claude-opus-5.5":{"id":"anthropic/claude-opus-5.5","name":"Claude",`+chat+`},
			"workers-ai/@cf/listed":{"id":"workers-ai/@cf/listed","name":"Listed",`+chat+`},
			"google/gemini":{"id":"google/gemini","name":"Dropped",`+chat+`}}}}`)}
	var definitions []modelgen.ProviderDefinition
	for _, definition := range providers.All() {
		if definition.ID == "cloudflare-workers-ai" || definition.ID == "cloudflare-ai-gateway" {
			definitions = append(definitions, definition)
		}
	}
	if len(definitions) != 2 {
		t.Fatalf("found %d Cloudflare providers", len(definitions))
	}
	catalogs, err := modelgen.BuildCatalog(definitions, sources, false)
	if err != nil {
		t.Fatal(err)
	}
	baseURLs := map[string]string{}
	for _, catalog := range catalogs {
		for _, model := range catalog.Chat {
			baseURLs[catalog.ID+"/"+model.Str("id")] = model.Str("baseUrl")
		}
		for _, model := range catalog.Classifier {
			baseURLs[catalog.ID+"/classifier/"+model.Str("id")] = model.Str("baseUrl")
		}
	}
	for _, tc := range []struct{ key, want string }{
		{"cloudflare-workers-ai/@cf/own", ai.CloudflareWorkersAIBaseURL},
		{"cloudflare-workers-ai/classifier/@cf/cloudflare/clef", ai.CloudflareWorkersAIRESTBaseURL},
		{"cloudflare-ai-gateway/gpt-5.5", ai.CloudflareAIGatewayOpenAIBaseURL},
		{"cloudflare-ai-gateway/claude-opus-5-5", ai.CloudflareAIGatewayAnthropicBaseURL},
		{"cloudflare-ai-gateway/workers-ai/@cf/listed", ai.CloudflareAIGatewayCompatBaseURL},
		{"cloudflare-ai-gateway/workers-ai/@cf/own", ai.CloudflareAIGatewayCompatBaseURL},
	} {
		if got, ok := baseURLs[tc.key]; !ok || got != tc.want {
			t.Errorf("%s baseUrl = %q (present %v), want %q", tc.key, got, ok, tc.want)
		}
	}
	if _, ok := baseURLs["cloudflare-ai-gateway/google/gemini"]; ok {
		t.Error("an upstream other than openai, anthropic and workers-ai is dropped")
	}
}
