package codingagent

import (
	"encoding/json"
	"math/rand"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

type cacheWarmerOracleInput struct {
	TTL         float64            `json:"ttl"`
	API         string             `json:"api"`
	PromptCache map[string]float64 `json:"promptCache"`
	Force       *bool              `json:"force"`
	Retention   string             `json:"retention"`
	Reasoning   string             `json:"reasoning"`
	Env         map[string]string  `json:"env"`
	Status      cacheWarmerOracleStatus
	UsageCost   float64 `json:"usageCost"`
	UsageNote   string  `json:"usageNote"`
	StatusNow   int64   `json:"now"`
}

type cacheWarmerOracleStatus struct {
	State             string               `json:"state"`
	Reason            string               `json:"reason"`
	HasDecision       bool                 `json:"hasDecision"`
	Decision          CacheWarmingDecision `json:"decision"`
	NextWarm          *int64               `json:"nextWarm"`
	ExtensionOverride bool                 `json:"extensionOverride"`
}

type cacheWarmerOracleOutput struct {
	Delay  *float64 `json:"delay"`
	TTL    *float64 `json:"ttlMs"`
	Replay bool     `json:"replay"`
	Status string   `json:"status"`
	Usage  string   `json:"usage"`
}

// TestCacheWarmerPureFunctionsMatchPi runs core/cache-warmer.ts getCacheWarmingDelayMs, getPromptCacheTtlMs, isReplayable, formatCacheWarmingStatus and
// formatCacheWarmingUsage and PiG's GetCacheWarmingDelayMs, GetPromptCacheTtlMs, IsReplayable, FormatCacheWarmingStatus and FormatCacheWarmingUsage over 6,000 seeded inputs under three
// process PI_CACHE_RETENTION values (unset, "long", "short"): TTLs around the ten-second margin, model prompt-cache tiers (absent, zero, fractional), request retention and
// request env (an empty request value falls through to the process one), Anthropic models with and without adaptive thinking, and every status state with decisions at
// rounding boundaries (probabilities x.5, savings -0.0004, 1e21 dollars), set or unset nextWarmAt and costs down to 1e-7. Not compared: a NaN or infinite TTL (a model's tier is finite seconds).
func TestCacheWarmerPureFunctionsMatchPi(t *testing.T) {
	rng := rand.New(rand.NewSource(20261123))
	pick := func(xs ...string) string { return xs[rng.Intn(len(xs))] }
	floats := func(xs ...float64) float64 { return xs[rng.Intn(len(xs))] }
	const now = int64(1_700_000_000_000)
	var inputs []cacheWarmerOracleInput
	for range 6000 {
		in := cacheWarmerOracleInput{
			TTL:       floats(0, 1, 9999, 10_000, 10_001, 10_002, 11_111, 20_000, 300_000, 3_600_000, 3_599_999.5, 1e12, 15_000.7),
			API:       pick("anthropic-messages", "openai-responses", "openai-completions"),
			Retention: pick("", "", "none", "short", "long"),
			Reasoning: pick("", "", "low", "medium", "high"),
			StatusNow: now,
		}
		if rng.Intn(5) > 0 {
			in.PromptCache = map[string]float64{}
			if rng.Intn(4) > 0 {
				in.PromptCache["short"] = floats(0, 300, 0.5, 3600)
			}
			if rng.Intn(4) > 0 {
				in.PromptCache["long"] = floats(0, 3600, 86400, 0.25)
			}
		}
		switch rng.Intn(3) {
		case 0:
			in.Force = new(true)
		case 1:
			in.Force = new(false)
		}
		if rng.Intn(3) == 0 {
			in.Env = map[string]string{"PI_CACHE_RETENTION": pick("", "long", "short", "LONG")}
		}
		in.UsageCost = floats(0, 1e-7, 0.0000005, 0.1234567, 0.5, 12.3, 1e21, -0.0004, -2.5, 100, 0.001, 0.0010004, 0.0012345)
		in.UsageNote = pick("", "", "idle", "a (b)")
		status := cacheWarmerOracleStatus{State: pick("inactive", "scheduled", "refreshing"), Reason: pick("", "", "no model"), ExtensionOverride: rng.Intn(3) == 0}
		status.HasDecision = rng.Intn(5) > 0
		status.Decision = CacheWarmingDecision{
			Phase:                   pick("streaming", "idle"),
			WarmCost:                floats(0, 0.001, 0.2),
			MissCost:                floats(0, 0.5, 3),
			ContinuationProbability: floats(0, 0.15, 0.145, 0.155, 0.005, 0.995, 1, 0.3, 0.125, 0.355),
			ExpectedSavings:         floats(0, 0.05, 0.0499, -0.0004, -0.0005, -1.2345, 1e21, 0.0005, 0.0015, 0.3335),
			EconomicsAvailable:      rng.Intn(4) > 0,
			Action:                  CacheWarmingAction(pick("warm", "stop")),
		}
		if rng.Intn(3) > 0 {
			status.NextWarm = new(now + int64(floats(-5000, 0, 1, 999, 1000, 1001, 59_000, 61_000, 3_599_001, 3_600_000, 7_325_000, 90_061_001)))
		}
		in.Status = status
		inputs = append(inputs, in)
	}

	const body = `
const { getCacheWarmingDelayMs, getPromptCacheTtlMs, isReplayable, formatCacheWarmingStatus, formatCacheWarmingUsage } = await load("pi-coding-agent/core/cache-warmer.js");
const out = [];
for (const c of input) {
	const model = { api: c.api, ...(c.promptCache ? { promptCache: c.promptCache } : {}), ...(c.force === null ? {} : { compat: { forceAdaptiveThinking: c.force } }) };
	const options = { ...(c.retention ? { cacheRetention: c.retention } : {}), ...(c.reasoning ? { reasoning: c.reasoning } : {}), ...(c.env ? { env: c.env } : {}) };
	const s = c.Status;
	const status = { state: s.state, ...(s.reason ? { reason: s.reason } : {}), ...(s.nextWarm === null ? {} : { nextWarmAt: s.nextWarm }), ...(s.hasDecision ? { decision: s.decision } : {}), ...(s.extensionOverride ? { extensionOverride: true } : {}) };
	out.push({
		delay: getCacheWarmingDelayMs(c.ttl) ?? null,
		ttlMs: getPromptCacheTtlMs(model, options) ?? null,
		replay: isReplayable(model, options),
		status: formatCacheWarmingStatus(status, c.now),
		usage: formatCacheWarmingUsage({ note: c.usageNote || undefined, usage: { cost: { total: c.usageCost } } }),
	});
}
emit(out);`
	for _, processRetention := range []string{"", "long", "short"} {
		t.Run("PI_CACHE_RETENTION="+processRetention, func(t *testing.T) {
			t.Setenv("PI_CACHE_RETENTION", "")
			var env []string
			if processRetention != "" {
				env = []string{"PI_CACHE_RETENTION=" + processRetention}
			}
			var want []cacheWarmerOracleOutput
			pioracle.Run(t, body, inputsForPi(inputs), &want, env...)
			t.Setenv("PI_CACHE_RETENTION", processRetention)
			failures := 0
			for i, in := range inputs {
				model := &ai.Model{ProviderMeta: ai.ProviderMetadata{API: ai.API(in.API)}, PromptCache: ai.ModelPromptCache(in.PromptCache)}
				if in.Force != nil {
					model.ProviderMeta.Compat = &ai.ModelCompat{ForceAdaptiveThinking: in.Force}
				}
				options := ai.StreamOptions{CacheRetention: ai.CacheRetention(in.Retention), Thinking: ai.ThinkingLevel(in.Reasoning), Env: in.Env}
				var got cacheWarmerOracleOutput
				if delay, ok := GetCacheWarmingDelayMs(in.TTL); ok {
					got.Delay = new(float64(delay))
				}
				if ttl, ok := GetPromptCacheTtlMs(model, options); ok {
					got.TTL = new(ttl)
				}
				got.Replay = IsReplayable(model, options)
				status := CacheWarmingStatus{State: in.Status.State, Reason: in.Status.Reason, ExtensionOverride: in.Status.ExtensionOverride}
				if in.Status.HasDecision {
					decision := in.Status.Decision
					status.Decision = &decision
				}
				if in.Status.NextWarm != nil {
					status.NextWarmAt = *in.Status.NextWarm
				}
				got.Status = FormatCacheWarmingStatus(status, now)
				entry := UsageEntry{Note: in.UsageNote}
				entry.Usage.Cost.Total = in.UsageCost
				got.Usage = FormatCacheWarmingUsage(entry)
				if !reflect.DeepEqual(got, want[i]) {
					failures++
					if failures <= 5 {
						gotJSON, _ := json.Marshal(got)
						wantJSON, _ := json.Marshal(want[i])
						inJSON, _ := json.Marshal(in)
						t.Errorf("case %d %s\n Go %s\n Pi %s", i, inJSON, gotJSON, wantJSON)
					}
				}
			}
			t.Logf("%d cases, %d failures", len(inputs), failures)
		})
	}
}

// inputsForPi turns the inputs into the plain JSON values the JavaScript body reads.
func inputsForPi(inputs []cacheWarmerOracleInput) []map[string]any {
	out := make([]map[string]any, 0, len(inputs))
	for _, in := range inputs {
		data, _ := json.Marshal(in)
		var m map[string]any
		_ = json.Unmarshal(data, &m)
		out = append(out, m)
	}
	return out
}
