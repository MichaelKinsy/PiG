package extension

// Ports packages/coding-agent/src/core/cache-warmer.ts

// CacheWarmingAction selects whether the scheduled prompt-cache refresh runs.
type CacheWarmingAction string

const (
	CacheWarmingActionWarm CacheWarmingAction = "warm"
	CacheWarmingActionStop CacheWarmingAction = "stop"
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
