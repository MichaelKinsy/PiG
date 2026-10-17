package codingagent

import (
	"fmt"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

type cacheWarmerOracleCase struct {
	PromptCache    map[string]float64 `json:"promptCache"`
	CacheRetention string             `json:"cacheRetention,omitempty"`
	Env            map[string]string  `json:"env,omitempty"`
	API            string             `json:"api"`
	Reasoning      string             `json:"reasoning,omitempty"`
	ForceAdaptive  *bool              `json:"forceAdaptive,omitempty"`
}

type cacheWarmerOracleResult struct {
	TTL        *float64 `json:"ttl"`
	Replayable bool     `json:"replayable"`
}

// cache-warmer.ts getPromptCacheTtlMs and isReplayable: the lifetime a request's cache entry has from the model's
// promptCache tier for the retention the request used (an explicit cacheRetention beats PI_CACHE_RETENTION in the request
// env, which beats the process environment; only the value "long" selects the long tier), and whether a one-token replay
// leaves the entry untouched. Run against the pinned Pi with the process variable unset and set.
func TestCachePromptTTLAndReplayableMatchPi(t *testing.T) {
	var cases []cacheWarmerOracleCase
	yes, no := true, false
	for _, cache := range []map[string]float64{nil, {}, {"short": 300}, {"long": 3600}, {"short": 300, "long": 3600}, {"short": 0.5, "long": 1e-9}, {"short": 1e300}} {
		for _, retention := range []string{"", "none", "short", "long"} {
			for _, env := range []map[string]string{nil, {"PI_CACHE_RETENTION": "long"}, {"PI_CACHE_RETENTION": "short"}, {"PI_CACHE_RETENTION": ""}, {"PI_CACHE_RETENTION": "LONG"}, {"PI_CACHE_RETENTION": "none"}, {"OTHER": "long"}} {
				cases = append(cases, cacheWarmerOracleCase{PromptCache: cache, CacheRetention: retention, Env: env, API: "openai-completions"})
			}
		}
	}
	for _, api := range []string{"anthropic-messages", "openai-completions"} {
		for _, reasoning := range []string{"", "minimal", "high"} {
			for _, force := range []*bool{nil, &yes, &no} {
				cases = append(cases, cacheWarmerOracleCase{API: api, Reasoning: reasoning, ForceAdaptive: force})
			}
		}
	}
	for _, processEnv := range []string{"", "long", "short", "other"} {
		var want []cacheWarmerOracleResult
		pioracle.Run(t, `
const mod = await load("pi-coding-agent/core/cache-warmer.js");
emit(input.map((c) => {
	const model = { api: c.api, promptCache: c.promptCache ?? undefined, compat: c.forceAdaptive === undefined || c.forceAdaptive === null ? undefined : { forceAdaptiveThinking: c.forceAdaptive } };
	const options = { cacheRetention: c.cacheRetention || undefined, env: c.env ?? undefined, reasoning: c.reasoning || undefined };
	const ttl = mod.getPromptCacheTtlMs(model, options);
	return { ttl: ttl === undefined ? null : ttl, replayable: mod.isReplayable(model, options) };
}));`, cases, &want, "PI_CACHE_RETENTION="+processEnv)
		t.Setenv("PI_CACHE_RETENTION", processEnv)
		failures := 0
		for i, c := range cases {
			model := &ai.Model{PromptCache: nil, ProviderMeta: ai.ProviderMetadata{API: ai.API(c.API)}}
			if c.PromptCache != nil {
				model.PromptCache = ai.ModelPromptCache(c.PromptCache)
			}
			if c.ForceAdaptive != nil {
				model.ProviderMeta.Compat = &ai.ModelCompat{ForceAdaptiveThinking: c.ForceAdaptive}
			}
			options := ai.StreamOptions{CacheRetention: ai.CacheRetention(c.CacheRetention), Env: ai.ProviderEnv(c.Env), Thinking: ai.ThinkingLevel(c.Reasoning)}
			ttl, ok := GetPromptCacheTtlMs(model, options)
			gotTTL, wantTTL := "none", "none"
			if ok {
				gotTTL = fmt.Sprint(ttl)
			}
			if want[i].TTL != nil {
				wantTTL = fmt.Sprint(*want[i].TTL)
			}
			if gotTTL != wantTTL || IsReplayable(model, options) != want[i].Replayable {
				if failures++; failures <= 8 {
					t.Errorf("process env %q case %d %+v\n  Pig ttl %s replayable %t\n  Pi  ttl %s replayable %t", processEnv, i, c, gotTTL, IsReplayable(model, options), wantTTL, want[i].Replayable)
				}
			}
		}
		if failures > 8 {
			t.Errorf("process env %q: %d of %d cases differ from Pi", processEnv, failures, len(cases))
		}
	}
}

// cache-warmer.ts getCacheWarmingDelayMs: refresh at 90% of the lifetime keeping ten seconds of margin; undefined for
// ten seconds or less. A lifetime comes from the promptCache seconds of a models.json (any number above zero), so very
// large and fractional lifetimes reach it.
func TestCacheWarmingDelayMatchesPi(t *testing.T) {
	ttls := []float64{-5, 0, 1e-9, 1, 9_999, 10_000, 10_000.0000001, 10_001, 10_010, 11_111.1, 20_000.5, 99_999, 100_000, 300_000, 3_600_000, 86_400_000, 1e12, 9e15, 9.1e15, 1e18, 9.3e18, 1e19, 1e30, 1e300}
	var want []*float64
	pioracle.Run(t, `
const mod = await load("pi-coding-agent/core/cache-warmer.js");
emit(input.map((ttl) => { const delay = mod.getCacheWarmingDelayMs(ttl); return delay === undefined ? null : delay; }));`, ttls, &want)
	for i, ttl := range ttls {
		got, ok := GetCacheWarmingDelayMs(ttl)
		gotText, wantText := "none", "none"
		if ok {
			gotText = fmt.Sprint(got)
		}
		if want[i] != nil {
			wantText = fmt.Sprint(*want[i])
		}
		if gotText != wantText {
			t.Errorf("delay(%v): Pig %s, Pi %s", ttl, gotText, wantText)
		}
	}
}
