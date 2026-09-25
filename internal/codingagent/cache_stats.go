package codingagent

import (
	"fmt"
	"math"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

const (
	cacheTTLMillis        int64 = 5 * 60 * 1000
	cacheNoiseFloorTokens       = 1024
)

type cacheMiss struct {
	missedTokens int
	missedCost   float64
	idleMillis   int64
	modelChanged bool
}

type previousCacheRequest struct {
	promptTokens  int
	modelKey      string
	timestamp     int64
	reportedCache bool
}

// detectCacheMiss compares the next assistant response with the last request already accounted for by the Session. The Session snapshot is maintained during load and append, so this event-loop check is independent of history length.
func (s *Session) detectCacheMiss(message *agent.AssistantMessage) *cacheMiss {
	s.mu.RLock()
	var prev *previousCacheRequest
	if s.stats.cachePrev != nil {
		copy := *s.stats.cachePrev
		prev = &copy
	}
	s.mu.RUnlock()
	return detectMiss(prev, message)
}

func detectMiss(prev *previousCacheRequest, message *agent.AssistantMessage) *cacheMiss {
	if message == nil || message.Usage == nil {
		return nil
	}
	usage := message.Usage
	promptTokens := usage.Input + usage.CacheRead + usage.CacheWrite
	if prev == nil || promptTokens <= 0 || (usage.CacheRead+usage.CacheWrite == 0 && !prev.reportedCache) {
		return nil
	}
	missedTokens := min(prev.promptTokens, promptTokens) - usage.CacheRead
	if missedTokens <= cacheNoiseFloorTokens {
		return nil
	}

	paidTokens := usage.Input + usage.CacheWrite
	paidPerToken := 0.0
	if paidTokens > 0 {
		paidPerToken = (usage.Cost.Input + usage.Cost.CacheWrite) / float64(paidTokens)
	}
	readPerToken := modelCacheReadCostPerToken(message)
	if usage.CacheRead > 0 {
		readPerToken = usage.Cost.CacheRead / float64(usage.CacheRead)
	}
	return &cacheMiss{
		missedTokens: missedTokens,
		missedCost:   float64(missedTokens) * math.Max(0, paidPerToken-readPerToken),
		idleMillis:   max(int64(0), message.Timestamp-prev.timestamp),
		modelChanged: message.Provider+"/"+message.ModelID != prev.modelKey,
	}
}

func asPreviousCacheRequest(message *agent.AssistantMessage, reportedCache bool) *previousCacheRequest {
	if message == nil || message.Usage == nil {
		return nil
	}
	usage := message.Usage
	promptTokens := usage.Input + usage.CacheRead + usage.CacheWrite
	if promptTokens <= 0 {
		return nil
	}
	return &previousCacheRequest{
		promptTokens:  promptTokens,
		modelKey:      message.Provider + "/" + message.ModelID,
		timestamp:     message.Timestamp,
		reportedCache: reportedCache || usage.CacheRead+usage.CacheWrite > 0,
	}
}

func modelCacheReadCostPerToken(message *agent.AssistantMessage) float64 {
	if gm, ok := ai.LookupModelExact(message.Provider + "/" + message.ModelID); ok {
		return gm.CacheReadCost / 1_000_000
	}
	return 0
}

func formatCacheMissNotice(miss *cacheMiss) string {
	if miss == nil || (miss.missedTokens < 20_000 && miss.missedCost < 0.1) {
		return ""
	}
	cost := ""
	if miss.missedCost >= 0.01 {
		cost = fmt.Sprintf(" (~$%.2f)", miss.missedCost)
	}
	label := "Cache miss"
	if miss.modelChanged {
		label = "Cache miss after model switch"
	} else if miss.idleMillis >= cacheTTLMillis {
		label = fmt.Sprintf("Cache miss after %dm idle", int(math.Round(float64(miss.idleMillis)/60_000)))
	}
	return fmt.Sprintf("%s: %s tokens re-billed%s", label, formatTokens(miss.missedTokens), cost)
}
