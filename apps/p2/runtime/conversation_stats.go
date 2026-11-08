package runtime

import "github.com/MichaelKinsy/PiG/ai"

// Ports the parts of apps/p2/src/runtime/conversation-stats.ts the clients read: a response's cost row and its cache hit
// rate.

// AssistantCost is one response of the active entries with its cost (`AssistantCost`).
type AssistantCost struct {
	EntryID   string
	Timestamp float64
	Cost      float64
	// CacheHit is nil when the response reported no prompt tokens.
	CacheHit   *float64
	Provider   string
	Model      string
	StopReason string
}

// CacheHitRate is the share of prompt tokens served from the provider cache, or nil without prompt tokens
// (`cacheHitRate`).
func CacheHitRate(usage ai.Usage) *float64 {
	prompt := usage.Input + usage.CacheRead + usage.CacheWrite
	if prompt <= 0 {
		return nil
	}
	rate := float64(usage.CacheRead) / float64(prompt)
	return &rate
}
