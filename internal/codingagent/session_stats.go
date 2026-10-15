package codingagent

import (
	"bytes"
	"math"
	"slices"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
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
	prices     ModelPriceSource
	// misses are the detected cache misses in entry order, so that a new price source reprices CacheWaste.MissedCost without reading the records again.
	misses []cacheMissPrice
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
	Role     string               `json:"role"`
	Content  sessionToolCallCount `json:"content"`
	Usage    *ai.Usage            `json:"usage,omitempty"`
	Provider string               `json:"provider,omitempty"`
	ModelID  string               `json:"model,omitempty"`
	// API is read for the session index (entrySlot.api), not for accounting; raw, so any value decodes.
	API json.RawMessage `json:"api,omitempty"`
	// ResponseModel is nil when the message has none; an empty string is a value (usage-totals.ts `responseModel ?? model`).
	ResponseModel *string `json:"responseModel,omitempty"`
	Timestamp     int64   `json:"timestamp,omitempty"`
}

type sessionToolCallCount int

func (count *sessionToolCallCount) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if len(data) == 0 || data[0] != '[' {
		*count = 0
		return nil
	}
	// The decoder has validated data; the scan answers the common shapes without validating and decoding it again.
	if scanned, ok := scanToolCallCount(data); ok {
		*count = sessionToolCallCount(scanned)
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

// SetCacheReadPriceSource binds runtime model prices during Session initialization or replacement. It rebuilds cache accounting only when historical misses need repricing; ordinary footer reads remain history-independent.
func (s *Session) SetCacheReadPriceSource(prices ModelPriceSource) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stats.reprice(prices)
}

// reprice binds prices. Prices affect only a detected miss's cost, not miss detection or the previous-request state, so MissedCost is the
// sum, in entry order, of each recorded miss's cost under prices: the value accounting every record again with prices gives.
func (a *sessionAccountingAccumulator) reprice(prices ModelPriceSource) {
	a.prices = prices
	if a.stats.CacheWaste.MissCount == 0 {
		return
	}
	var cost float64
	for _, miss := range a.misses {
		cost += miss.cost(prices)
	}
	a.stats.CacheWaste.MissedCost = cost
}

func (s *Session) Accounting() SessionAccounting {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.stats.snapshot()
}

func (a *sessionAccountingAccumulator) add(raw json.RawMessage, wireType string) {
	_, _ = a.addRecord(raw, wireType)
}

// addRecord is add that also returns the entry's message, false when the record does not decode.
func (a *sessionAccountingAccumulator) addRecord(raw json.RawMessage, wireType string) (sessionAccountingMessage, bool) {
	var entry sessionAccountingEntry
	if err := json.Unmarshal(raw, &entry); err != nil {
		return sessionAccountingMessage{}, false
	}
	a.addDecoded(entry, wireType)
	return entry.Message, true
}

// addDecoded is add for a record already decoded.
func (a *sessionAccountingAccumulator) addDecoded(entry sessionAccountingEntry, wireType string) {
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
		a.addMessage(entry.Message)
	case "bash_execution":
		a.stats.TotalMessages++
	}
}

// addMessage counts one "message" entry.
func (a *sessionAccountingAccumulator) addMessage(message sessionAccountingMessage) {
	a.stats.TotalMessages++
	switch message.Role {
	case "user":
		a.stats.UserMessages++
	case "assistant":
		a.stats.AssistantMessages++
		a.stats.ToolCalls += int(message.Content)
		key := message.Provider + "/" + message.ModelID
		if message.ResponseModel != nil {
			key = message.Provider + "/" + *message.ResponseModel
		}
		a.addUsage(key, message.Usage)
		a.stats.LatestCacheHitRate = latestCacheHitRate(message.Usage)
		a.addCacheWaste(message)
	case "toolResult":
		a.stats.ToolResults++
		a.addUsage("Tools/summaries", message.Usage)
	}
}

// addMessageEntry counts the "message" entry whose record was just encoded from a message and decoded back (the entry holds what its record says). It reads the decoded fields that add reads from the record, so a long tool result is not scanned again.
func (a *sessionAccountingAccumulator) addMessageEntry(message agent.AgentMessage) {
	accounted := sessionAccountingMessage{Role: message.Role()}
	switch {
	case message.Assistant != nil:
		assistant := message.Assistant
		for _, block := range assistant.Content {
			if _, isCall := block.(ai.ToolCall); isCall {
				accounted.Content++
			}
		}
		accounted.Usage = assistant.Usage
		accounted.Provider = assistant.Provider
		accounted.ModelID = assistant.ModelID
		if assistant.ResponseModel != "" {
			accounted.ResponseModel = &assistant.ResponseModel
		}
		accounted.Timestamp = assistant.Timestamp
	case message.ToolResult != nil:
		accounted.Usage = message.ToolResult.Usage
	}
	a.addMessage(accounted)
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
	if miss := detectMiss(a.cachePrev, assistant, a.prices); miss != nil {
		a.stats.CacheWaste.MissedTokens += miss.missedTokens
		a.stats.CacheWaste.MissedCost += miss.missedCost
		a.stats.CacheWaste.MissCount++
		a.misses = append(a.misses, miss.price)
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
	// Date.parse(entry.timestamp); a timestamp it cannot read is NaN there and 0 here.
	var parsed int64
	if ms := ai.DateParse(timestamp); !math.IsNaN(ms) {
		parsed = int64(ms)
	}
	return &previousCacheRequest{
		promptTokens:  promptTokens,
		modelKey:      provider + "/" + model,
		timestamp:     parsed,
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
