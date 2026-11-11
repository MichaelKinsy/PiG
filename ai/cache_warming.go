package ai

import (
	"context"
	"math"
)

// Ports packages/ai/src/cache-warming.ts (Pi c9552c7d, #11857): the prompt cache warming policy shared by every host: when a refresh is due, whether a request can be replayed, and whether a refresh pays for itself. Hosts own the scheduling; this file owns the numbers. internal/codingagent's CacheWarmer and p2's cache-warming plugin both schedule on it.

// CacheWarmingAction is whether a refresh is sent ("warm") or warming ends until the next real request ("stop").
type CacheWarmingAction string

// The actions of upstream's CacheWarmingAction union.
const (
	CacheWarmingActionWarm CacheWarmingAction = "warm"
	CacheWarmingActionStop CacheWarmingAction = "stop"
)

// CacheWarmingPhase is "streaming" while the agent run that sent the request is still active, "idle" after it settled.
type CacheWarmingPhase string

// The phases of upstream's CacheWarmingPhase union.
const (
	CacheWarmingPhaseStreaming CacheWarmingPhase = "streaming"
	CacheWarmingPhaseIdle      CacheWarmingPhase = "idle"
)

// CacheWarmingDecision holds the inputs and outcome of one warm-or-stop decision.
type CacheWarmingDecision struct {
	Phase CacheWarmingPhase `json:"phase"`
	// WarmCost is the price of this refresh: a cache read of the prompt plus one output token.
	WarmCost float64 `json:"warmCost"`
	// MissCost is the extra price of the next real request if the cache entry is lost.
	MissCost float64 `json:"missCost"`
	// ContinuationProbability is the estimated chance that a real request arrives before the entry expires.
	ContinuationProbability float64 `json:"continuationProbability"`
	// ExpectedSavings is ContinuationProbability*MissCost - WarmCost.
	ExpectedSavings float64 `json:"expectedSavings"`
	// EconomicsAvailable is false when the prompt size or the model's prices are unknown.
	EconomicsAvailable bool `json:"economicsAvailable"`
	// Action is "warm" when ExpectedSavings is at least CacheWarmingMinimumExpectedSavings.
	Action CacheWarmingAction `json:"action"`
}

const (
	// CacheWarmingMaxAgeMs (CACHE_WARMING_MAX_AGE_MS): streaming warming never continues past this long after the real request that started it.
	CacheWarmingMaxAgeMs int64 = 60 * 60_000 // upstream: packages/ai/src/cache-warming.ts:CACHE_WARMING_MAX_AGE_MS
	// CacheWarmingMaxIdleAgeMs (CACHE_WARMING_MAX_IDLE_AGE_MS): idle warming uses a shorter horizon because continuation estimates become less reliable with age.
	CacheWarmingMaxIdleAgeMs int64 = 30 * 60_000 // upstream: packages/ai/src/cache-warming.ts:CACHE_WARMING_MAX_IDLE_AGE_MS
	// CacheWarmingMinimumExpectedSavings (CACHE_WARMING_MINIMUM_EXPECTED_SAVINGS): a refresh is sent only when it is expected to save at least this many dollars.
	CacheWarmingMinimumExpectedSavings = 0.05
	// CacheWarmingIdleContinuationProbability (CACHE_WARMING_IDLE_CONTINUATION_PROBABILITY) is the chance that a real request arrives before the cache entry expires while the agent sits idle. Measured from Pi's own usage; per-session estimates were not better than this constant.
	CacheWarmingIdleContinuationProbability = 0.15
)

// GetCacheWarmingDelayMs refreshes at 90% of the TTL while preserving at least ten seconds of margin. The bool is false when the TTL is too short. The delay is a float64 like the JavaScript number: a lifetime from a models.json promptCache can exceed the int64 range.
func GetCacheWarmingDelayMs(ttlMs float64) (float64, bool) {
	if ttlMs <= 10_000 { // upstream: packages/ai/src/cache-warming.ts:47
		return 0, false
	}
	return max(1, math.Floor(math.Min(ttlMs*0.9, ttlMs-10_000))), true
}

// GetPromptCacheTtlMs returns the model's prompt-cache lifetime for the request's retention, from the model's promptCache tier. Explicit retention wins over the environment; none or a missing tier has no lifetime.
func GetPromptCacheTtlMs(model *Model, options StreamOptions) (float64, bool) {
	retention := string(options.CacheRetention)
	if retention == "" {
		retention = "short"
		if getProviderEnvValue("PI_CACHE_RETENTION", options.Env) == "long" {
			retention = "long"
		}
	}
	if retention == "none" {
		return 0, false
	}
	seconds, ok := model.PromptCache[retention]
	if !ok {
		return 0, false
	}
	return seconds * 1000, true
}

// IsCacheWarmingReplayable reports whether replaying the request with a one-token output cap leaves its cache entry untouched. Anthropic's budget-based thinking (Claude models without adaptive thinking) derives budget_tokens from max_tokens, and Anthropic keys the message cache on that budget, so only adaptive thinking replays safely: the replay would get a different budget, and the model could still think for thousands of tokens.
func IsCacheWarmingReplayable(model *Model, options StreamOptions) bool {
	reasoning := options.Thinking != ""
	if !reasoning || model.ProviderMeta.API != APIAnthropicMessages {
		return true
	}
	compat := model.ProviderMeta.Compat
	return compat != nil && compat.ForceAdaptiveThinking != nil && *compat.ForceAdaptiveThinking
}

// GetCacheWarmingRequestOptions is a refresh's request: the original's options capped at one output token and without retries, and without replacing the conversation's provider thread state (the refresh repeats the last request). The returned context is signal with the no-retry budget; it carries the request's abort and `maxRetries: 0`, which Go passes beside the options (WithProviderMaxRetries) where upstream sets them in the options object.
func GetCacheWarmingRequestOptions(options StreamOptions, signal context.Context) (context.Context, StreamOptions) {
	options.MaxTokens = 1
	options.DisableContinuation = true
	return WithProviderMaxRetries(signal, 0), options
}

// EvaluateCacheWarming decides whether refreshing the cache entry of a promptTokens-token request pays for itself in phase.
func EvaluateCacheWarming(model *Model, promptTokens int, phase CacheWarmingPhase) CacheWarmingDecision {
	cacheHitCost := cacheWarmingPrice(model, Usage{CacheRead: promptTokens})
	missUsage := Usage{Input: promptTokens}
	if model.Capabilities.CacheWriteCostPer1M > 0 {
		missUsage = Usage{CacheWrite: promptTokens}
	}
	cacheMissCost := cacheWarmingPrice(model, missUsage)
	warmCost := cacheWarmingPrice(model, Usage{CacheRead: promptTokens, Output: 1})
	missCost := math.Max(0, cacheMissCost-cacheHitCost)
	continuationProbability := 1.0
	if phase == CacheWarmingPhaseIdle {
		continuationProbability = CacheWarmingIdleContinuationProbability
	}
	expectedSavings := float64(continuationProbability*missCost) - warmCost
	action := CacheWarmingActionStop
	if expectedSavings >= CacheWarmingMinimumExpectedSavings {
		action = CacheWarmingActionWarm
	}
	return CacheWarmingDecision{
		Phase:                   phase,
		WarmCost:                warmCost,
		MissCost:                missCost,
		ContinuationProbability: continuationProbability,
		ExpectedSavings:         expectedSavings,
		EconomicsAvailable:      promptTokens > 0 && (cacheHitCost > 0 || cacheMissCost > 0),
		Action:                  action,
	}
}

func cacheWarmingPrice(model *Model, usage Usage) float64 {
	return CalculateCost(model, &usage).Total
}
