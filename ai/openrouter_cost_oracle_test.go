package ai

// pi: packages/ai/src/utils/openrouter-cost.ts
// pi: packages/ai/src/providers/openrouter-api.ts

import (
	"bytes"
	"encoding/json"
	"math"
	"os/exec"
	"strconv"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

type openRouterCostProbe struct {
	Kind     string `json:"kind"`
	Provider string `json:"provider"`
	BaseURL  string `json:"baseUrl"`
	Reported string `json:"reported,omitempty"`
	Pricing  string `json:"pricing,omitempty"`
}

// runOpenRouterOracle answers each probe with the pinned Pi's openrouter-cost.js and openrouter-api.js.
func runOpenRouterOracle(t *testing.T, probes []openRouterCostProbe) []json.RawMessage {
	t.Helper()
	payload, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/openrouter_cost.mjs", pigversion.UpstreamPackageVersion)
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var answers []json.RawMessage
	if err := json.Unmarshal(output, &answers); err != nil {
		t.Fatal(err)
	}
	if len(answers) != len(probes) {
		t.Fatalf("Pi answered %d of %d probes", len(answers), len(probes))
	}
	return answers
}

// canonNumber prints a float the way the oracle's answers are compared: NaN and the infinities as String() text, -0 as 0.
func canonNumber(v float64) string {
	switch {
	case math.IsNaN(v):
		return "NaN"
	case math.IsInf(v, 1):
		return "Infinity"
	case math.IsInf(v, -1):
		return "-Infinity"
	case v == 0:
		return "0"
	}
	return strconv.FormatFloat(v, 'g', -1, 64)
}

// canonAnswer prints a JSON number, the String() text of a non-finite one, or null as canonNumber does.
func canonAnswer(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text
	}
	if string(raw) == "null" {
		return "null"
	}
	var v float64
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("answer %s: %v", raw, err)
	}
	return canonNumber(v)
}

func TestIsOpenRouterModelMatchesPi(t *testing.T) {
	cases := []struct{ provider, baseURL string }{
		{"openrouter", "https://example.com"},
		{"openrouter-work", "https://openrouter.ai/api/v1"},
		{"custom", "https://example.com/v1"},
		{"custom", "https://proxy.example/?upstream=openrouter.ai"},
		{"OpenRouter", "https://example.com"},
		{"openrouter-work", "https://example.com"},
		{"custom", "https://OPENROUTER.AI/api"},
		{"custom", "https://openrouterxai/api"},
		{"", ""},
	}
	var probes []openRouterCostProbe
	for _, c := range cases {
		probes = append(probes, openRouterCostProbe{Kind: "model", Provider: c.provider, BaseURL: c.baseURL})
	}
	for i, answer := range runOpenRouterOracle(t, probes) {
		var want bool
		if err := json.Unmarshal(answer, &want); err != nil {
			t.Fatal(err)
		}
		if got := isOpenRouterModel(cases[i].provider, cases[i].baseURL); got != want {
			t.Errorf("isOpenRouterModel(%q, %q) = %v, Pi says %v", cases[i].provider, cases[i].baseURL, got, want)
		}
	}
}

func TestApplyOpenRouterReportedCostMatchesPi(t *testing.T) {
	const details = `"cost_details":{"upstream_inference_cost":0.9,"upstream_inference_prompt_cost":0.6,"upstream_inference_completions_cost":0.3}`
	reports := []string{
		`{"cost":0,"is_byok":false,"cost_details":{"upstream_inference_cost":0,"upstream_inference_prompt_cost":0,"upstream_inference_completions_cost":0}}`,
		`{"cost":0.1,"is_byok":true,` + details + `}`,
		`{"cost":0.1,"is_byok":true,"cost_details":{"upstream_inference_cost":null,"upstream_inference_prompt_cost":0.6,"upstream_inference_completions_cost":0.3}}`,
		`{"cost":null,"is_byok":false,"cost_details":null}`,
		`{"cost":0.1,"is_byok":true,"cost_details":null}`,
		`{"cost":0.1,"is_byok":false}`,
		`{"cost":2.5,"is_byok":false,` + details + `}`,
		`{"cost":-1,"is_byok":false}`,
		`{"cost":"0.5","is_byok":false}`,
		`{"cost":1e999,"is_byok":false}`,
		`{"is_byok":false}`,
		`{"cost":0.5}`,
		`{"cost":0.5,"is_byok":null}`,
		`{"cost":0.5,"is_byok":"true",` + details + `}`,
		`{"cost":0.5,"is_byok":"false"}`,
		`{"cost":0.5,"is_byok":1}`,
		`{"cost":0.5,"is_byok":true}`,
		`{"cost":0.5,"is_byok":true,"cost_details":[]}`,
		`{"cost":0.5,"is_byok":true,"cost_details":"x"}`,
		`{"cost":0.5,"is_byok":true,"cost_details":{}}`,
		`{"cost":0.5,"is_byok":true,"cost_details":{"upstream_inference_cost":-0.1}}`,
		`{"cost":0.5,"is_byok":true,"cost_details":{"upstream_inference_cost":"0.9"}}`,
		`{"cost":0.5,"is_byok":true,"cost_details":{"upstream_inference_cost":0}}`,
		`{"cost":0.5,"is_byok":true,"cost_details":{"upstream_inference_cost":1e999}}`,
		`{"cost":0.5,"is_byok":true,"cost_details":{"upstream_inference_cost":null,"upstream_inference_prompt_cost":0.6}}`,
		`{"cost":0.5,"is_byok":true,"cost_details":{"upstream_inference_cost":null,"upstream_inference_prompt_cost":0.6,"upstream_inference_completions_cost":null}}`,
		`{"cost":0.5,"is_byok":true,"cost_details":{"upstream_inference_cost":null,"upstream_inference_prompt_cost":-0.6,"upstream_inference_completions_cost":0.3}}`,
		`{"cost":0.5,"is_byok":true,"cost_details":{"upstream_inference_cost":null,"upstream_inference_prompt_cost":0.6,"upstream_inference_completions_cost":-0.3}}`,
		`{"cost":0.5,"is_byok":true,"cost_details":{"upstream_inference_cost":null,"upstream_inference_prompt_cost":0,"upstream_inference_completions_cost":0}}`,
		`{"cost":0.5,"is_byok":true,"cost_details":{"upstream_inference_cost":0.25,"upstream_inference_prompt_cost":-1,"upstream_inference_completions_cost":-1}}`,
		`{"cost":0.1,"is_byok":true,"cost_details":{"upstream_inference_cost":0.2,"server_tool_cost":0.05}}`,
	}
	var probes []openRouterCostProbe
	for _, r := range reports {
		probes = append(probes, openRouterCostProbe{Kind: "reported", Reported: r})
	}
	for i, answer := range runOpenRouterOracle(t, probes) {
		var want struct {
			Total        json.RawMessage `json:"total"`
			FromProvider *bool           `json:"fromProvider"`
		}
		if err := json.Unmarshal(answer, &want); err != nil {
			t.Fatal(err)
		}
		usage := openRouterUsage()
		applyOpenRouterReportedCost(usage, decodeOpenRouterAccounting(t, reports[i]))
		if got, wantTotal := canonNumber(usage.Cost.Total), canonAnswer(t, want.Total); got != wantTotal {
			t.Errorf("%s: total = %s, Pi says %s", reports[i], got, wantTotal)
		}
		if (usage.Cost.TotalFromProvider == nil) != (want.FromProvider == nil) || usage.Cost.TotalFromProvider != nil && *usage.Cost.TotalFromProvider != *want.FromProvider {
			t.Errorf("%s: totalFromProvider = %v, Pi says %v", reports[i], usage.Cost.TotalFromProvider, want.FromProvider)
		}
	}
}

// Pricing is probed inside the types openrouter-api.ts declares: a string rate (or any JSON value whose String() parseFloat reads as NaN), a numeric
// min_prompt_tokens, and the utc_* members present or null. An array rate (String([0.5]) is "0.5") or a null or string min_prompt_tokens (Pi keeps a
// tier whose threshold is null or "7") is outside those types, and Go's typed ModelCost tier cannot hold it.
func TestOpenRouterCostMatchesPi(t *testing.T) {
	prices := []string{
		`null`,
		`{}`,
		`{"prompt":"0.000003","completion":"0.000015","input_cache_read":"0.0000003","input_cache_write":"0.00000375"}`,
		`{"prompt":"-1","completion":"-1"}`,
		`{"prompt":"0","completion":"0"}`,
		`{"prompt":"abc","completion":"","input_cache_read":null,"input_cache_write":"-0.5"}`,
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
		`{"prompt":"0.000002","overrides":[{"min_prompt_tokens":1,"utc_start":0,"utc_end":6,"prompt":"0.000001"},{"min_prompt_tokens":2,"utc_days":["sat"],"prompt":"0.000001"},{"min_prompt_tokens":3,"utc_end":1},{"utc_start":1,"prompt":"0.000001"}]}`,
		`{"prompt":"0.000002","overrides":[{"min_prompt_tokens":10,"utc_start":null,"prompt":"0.000009"},{"min_prompt_tokens":11,"utc_days":null,"prompt":"0.000009"},{"min_prompt_tokens":12,"utc_end":null}]}`,
		`{"prompt":"0.000002","overrides":[{"min_prompt_tokens":0,"prompt":"0.000009"},{"min_prompt_tokens":-5},{"min_prompt_tokens":1.5,"completion":"0.00001"},{"prompt":"0.000009"},{}]}`,
		`{"prompt":"0.000002","overrides":[{"min_prompt_tokens":200000,"prompt":"0.000004"},{"min_prompt_tokens":100000,"prompt":"0.000003"}]}`,
		`{"prompt":"0.000002","completion":"0.00001","overrides":[{"min_prompt_tokens":5,"prompt":"0","completion":"0"}]}`,
		`{"prompt":"0.000002","input_cache_write":"0.000003","overrides":[{"min_prompt_tokens":9,"prompt":"0.000004"},{"min_prompt_tokens":10,"input_cache_write":"abc"}]}`,
		`{"prompt":"0.000002","overrides":[]}`,
		`{"prompt":"0.000002","overrides":null}`,
	}
	var probes []openRouterCostProbe
	for _, p := range prices {
		probes = append(probes, openRouterCostProbe{Kind: "cost", Pricing: p})
	}
	type tier struct {
		InputTokensAbove json.RawMessage `json:"inputTokensAbove"`
		Input            json.RawMessage `json:"input"`
		Output           json.RawMessage `json:"output"`
		CacheRead        json.RawMessage `json:"cacheRead"`
		CacheWrite       json.RawMessage `json:"cacheWrite"`
	}
	for i, answer := range runOpenRouterOracle(t, probes) {
		var want struct {
			tier
			Tiers []tier `json:"tiers"`
		}
		if err := json.Unmarshal(answer, &want); err != nil {
			t.Fatal(err)
		}
		var pricing *OpenRouterPricing
		if prices[i] != "null" {
			pricing = new(OpenRouterPricing)
			if err := json.Unmarshal([]byte(prices[i]), pricing); err != nil {
				t.Fatal(err)
			}
		}
		got := OpenRouterCost(pricing)
		for _, f := range []struct {
			name string
			got  float64
			want json.RawMessage
		}{{"input", got.Input, want.Input}, {"output", got.Output, want.Output}, {"cacheRead", got.CacheRead, want.CacheRead}, {"cacheWrite", got.CacheWrite, want.CacheWrite}} {
			if g, w := canonNumber(f.got), canonAnswer(t, f.want); g != w {
				t.Errorf("%s: %s = %s, Pi says %s", prices[i], f.name, g, w)
			}
		}
		if len(got.Tiers) != len(want.Tiers) {
			t.Errorf("%s: %d tiers, Pi says %d", prices[i], len(got.Tiers), len(want.Tiers))
			continue
		}
		for j, g := range got.Tiers {
			w := want.Tiers[j]
			for _, f := range []struct {
				name string
				got  float64
				want json.RawMessage
			}{{"inputTokensAbove", g.InputTokensAbove, w.InputTokensAbove}, {"input", g.InputCostPer1M, w.Input}, {"output", g.OutputCostPer1M, w.Output}, {"cacheRead", g.CacheReadCostPer1M, w.CacheRead}, {"cacheWrite", g.CacheWriteCostPer1M, w.CacheWrite}} {
				if gs, ws := canonNumber(f.got), canonAnswer(t, f.want); gs != ws {
					t.Errorf("%s: tier %d %s = %s, Pi says %s", prices[i], j, f.name, gs, ws)
				}
			}
		}
	}
}
