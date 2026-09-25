package codingagent

import (
	"bytes"
	"encoding/json"
	"slices"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// SessionAccounting is the immutable all-entry accounting maintained with a Session.
type SessionAccounting struct {
	UserMessages      int
	AssistantMessages int
	ToolCalls         int
	ToolResults       int
	TotalMessages     int
	Tokens            SessionTokenStats
	UsageBreakdown    []SessionUsageBreakdown
	CacheWaste        SessionCacheWaste
	// LatestCacheHitRate is the cache-read share (percent) of the last
	// assistant message's prompt, nil when that prompt had no tokens.
	LatestCacheHitRate *float64
}

// SessionUsageBreakdown attributes billed usage to a model or to host-side summaries.
type SessionUsageBreakdown struct {
	Key    string
	Cost   float64
	Tokens int
}

// SessionCacheWaste reports prompt tokens that a preceding request made cacheable but a later request did not read from cache.
type SessionCacheWaste struct {
	MissedTokens int
	MissedCost   float64
	MissCount    int
}

type sessionAccountingAccumulator struct {
	stats      SessionAccounting
	usageByKey map[string]SessionUsageBreakdown
	usageOrder []string
	cachePrev  *previousCacheRequest
}

type sessionAccountingEntry struct {
	Type      string                   `json:"type"`
	Timestamp json.RawMessage          `json:"timestamp"`
	Provider  string                   `json:"provider,omitempty"`
	Model     string                   `json:"model,omitempty"`
	Usage     *ai.Usage                `json:"usage,omitempty"`
	Message   sessionAccountingMessage `json:"message"`
	// Kind is set on "usage" entries.
	Kind string `json:"kind,omitempty"`
}

type sessionAccountingMessage struct {
	Role          string               `json:"role"`
	Content       sessionToolCallCount `json:"content"`
	Usage         *ai.Usage            `json:"usage,omitempty"`
	Provider      string               `json:"provider,omitempty"`
	ModelID       string               `json:"model,omitempty"`
	ResponseModel string               `json:"responseModel,omitempty"`
	Timestamp     int64                `json:"timestamp,omitempty"`
}

type sessionToolCallCount int

func (count *sessionToolCallCount) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if len(data) == 0 || data[0] != '[' {
		*count = 0
		return nil
	}
	var blocks []struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(data, &blocks); err != nil {
		return err
	}
	for _, block := range blocks {
		if block.Type == "toolCall" || block.Type == "tool_use" {
			*count++
		}
	}
	return nil
}

func (s *Session) Accounting() SessionAccounting {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.stats.snapshot()
}

func (a *sessionAccountingAccumulator) add(raw json.RawMessage, wireType string) string {
	var entry sessionAccountingEntry
	if err := json.Unmarshal(raw, &entry); err != nil {
		return wireType
	}
	if entry.Type != "" {
		wireType = entry.Type
	}

	switch wireType {
	case "usage":
		a.addUsage(entry.Provider+"/"+entry.Model, entry.Usage)
		if entry.Kind == "cache_warm" {
			a.addCacheWarm(entry)
		}
	case "compaction", "branch_summary":
		a.addUsage("Tools/summaries", entry.Usage)
		a.cachePrev = nil
	case "message":
		a.stats.TotalMessages++
		message := entry.Message
		switch message.Role {
		case "user":
			a.stats.UserMessages++
		case "assistant":
			a.stats.AssistantMessages++
			a.stats.ToolCalls += int(message.Content)
			key := message.Provider + "/" + message.ModelID
			if message.ResponseModel != "" {
				key = message.Provider + "/" + message.ResponseModel
			}
			a.addUsage(key, message.Usage)
			a.stats.LatestCacheHitRate = latestCacheHitRate(message.Usage)
			a.addCacheWaste(message)
		case "toolResult":
			a.stats.ToolResults++
			a.addUsage("Tools/summaries", message.Usage)
		case "bashExecution":
			return "bash_execution"
		}
	case "bash_execution":
		a.stats.TotalMessages++
	}
	return wireType
}

// latestCacheHitRate mirrors footer.ts: cacheRead over the whole prompt
// (input + cacheRead + cacheWrite), in percent.
func latestCacheHitRate(usage *ai.Usage) *float64 {
	if usage == nil {
		return nil
	}
	promptTokens := usage.Input + usage.CacheRead + usage.CacheWrite
	if promptTokens <= 0 {
		return nil
	}
	rate := (float64(usage.CacheRead) / float64(promptTokens)) * 100
	return &rate
}

// FooterUsageTotals returns the footer's all-entry usage totals without
// building the per-model breakdown.
func (s *Session) FooterUsageTotals() footerUsageTotals {
	s.mu.RLock()
	defer s.mu.RUnlock()
	tokens := s.stats.stats.Tokens
	return footerUsageTotals{
		input:              tokens.Input,
		output:             tokens.Output,
		cacheRead:          tokens.CacheRead,
		cacheWrite:         tokens.CacheWrite,
		cost:               tokens.Cost,
		latestCacheHitRate: s.stats.stats.LatestCacheHitRate,
	}
}

func (a *sessionAccountingAccumulator) addUsage(key string, usage *ai.Usage) {
	if usage == nil {
		return
	}
	a.stats.Tokens.Input += usage.Input
	a.stats.Tokens.Output += usage.Output
	a.stats.Tokens.CacheRead += usage.CacheRead
	a.stats.Tokens.CacheWrite += usage.CacheWrite
	a.stats.Tokens.Total += usage.Input + usage.Output + usage.CacheRead + usage.CacheWrite
	a.stats.Tokens.Cost += usage.Cost.Total

	if a.usageByKey == nil {
		a.usageByKey = make(map[string]SessionUsageBreakdown)
	}
	breakdown, found := a.usageByKey[key]
	if !found {
		a.usageOrder = append(a.usageOrder, key)
	}
	breakdown.Key = key
	breakdown.Cost += usage.Cost.Total
	breakdown.Tokens += usage.Input + usage.Output + usage.CacheRead + usage.CacheWrite
	a.usageByKey[key] = breakdown
}

func (a *sessionAccountingAccumulator) addCacheWaste(message sessionAccountingMessage) {
	assistant := &agent.AssistantMessage{
		Usage:     message.Usage,
		Provider:  message.Provider,
		ModelID:   message.ModelID,
		Timestamp: message.Timestamp,
	}
	if miss := detectMiss(a.cachePrev, assistant); miss != nil {
		a.stats.CacheWaste.MissedTokens += miss.missedTokens
		a.stats.CacheWaste.MissedCost += miss.missedCost
		a.stats.CacheWaste.MissCount++
	}
	if next := asPreviousCacheRequest(assistant, a.cachePrev != nil && a.cachePrev.reportedCache); next != nil {
		a.cachePrev = next
	}
}

// addCacheWarm treats a cache-warming refresh as the latest cached request.
func (a *sessionAccountingAccumulator) addCacheWarm(entry sessionAccountingEntry) {
	var iso string
	_ = json.Unmarshal(entry.Timestamp, &iso)
	a.cachePrev = cacheWarmPreviousRequest(entry.Provider, entry.Model, entry.Usage, iso, a.cachePrev)
}

// cacheWarmPreviousRequest is the cached request a cache-warming refresh
// leaves behind, or previous when the refresh reported no prompt. Mirrors the
// usage branch of upstream cache-stats.ts scan.
func cacheWarmPreviousRequest(provider, model string, usage *ai.Usage, timestamp string, previous *previousCacheRequest) *previousCacheRequest {
	if usage == nil {
		return previous
	}
	promptTokens := usage.Input + usage.CacheRead + usage.CacheWrite
	if promptTokens <= 0 {
		return previous
	}
	parsed, _ := time.Parse(time.RFC3339Nano, timestamp)
	return &previousCacheRequest{
		promptTokens:  promptTokens,
		modelKey:      provider + "/" + model,
		timestamp:     parsed.UnixMilli(),
		reportedCache: true,
	}
}

func (a sessionAccountingAccumulator) snapshot() SessionAccounting {
	stats := a.stats
	if len(a.usageByKey) == 0 {
		return stats
	}
	stats.UsageBreakdown = make([]SessionUsageBreakdown, 0, len(a.usageByKey))
	for _, key := range a.usageOrder {
		breakdown := a.usageByKey[key]
		if breakdown.Cost > 0 || breakdown.Tokens > 0 {
			stats.UsageBreakdown = append(stats.UsageBreakdown, breakdown)
		}
	}
	slices.SortStableFunc(stats.UsageBreakdown, func(left, right SessionUsageBreakdown) int {
		switch {
		case left.Cost > right.Cost:
			return -1
		case left.Cost < right.Cost:
			return 1
		default:
			return 0
		}
	})
	return stats
}
