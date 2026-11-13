package extension

import "github.com/MichaelKinsy/PiG/ai"

// Ports packages/coding-agent/src/core/cache-warmer.ts

// CacheWarmingAction selects whether the scheduled prompt-cache refresh runs. It is pi-ai's (cache-warmer.ts re-exports it from there).
type CacheWarmingAction = ai.CacheWarmingAction

const (
	CacheWarmingActionWarm = ai.CacheWarmingActionWarm
	CacheWarmingActionStop = ai.CacheWarmingActionStop
)

// CacheWarmingDecisionEvent carries the economics and default action before a cache refresh.
type CacheWarmingDecisionEvent struct {
	Type                    string             `json:"type"`
	WarmCost                float64            `json:"warmCost"`
	MissCost                float64            `json:"missCost"`
	ContinuationProbability float64            `json:"continuationProbability"`
	Action                  CacheWarmingAction `json:"action"`
}

// CacheWarmingDecisionEventResult overrides the decision when Action is present.
type CacheWarmingDecisionEventResult struct {
	Action *CacheWarmingAction `json:"action,omitempty"`
}
