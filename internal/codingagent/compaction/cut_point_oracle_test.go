package compaction

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// Pi's findCutPoint, findTurnStartIndex, getLastAssistantUsage and estimateContextTokens (core/compaction/compaction.js) run against the vendored Pi module over seeded random session branches that mix user, assistant (usage, aborted, error, tool calls), tool-result, bash, custom, branch-summary, compaction and metadata entries.
func TestCutPointAndContextEstimateMatchPiOracle(t *testing.T) {
	rng := rand.New(rand.NewSource(5))
	texts := []string{"", "a", "short text", strings.Repeat("long ", 120), strings.Repeat("😀", 40), strings.Repeat("x", 4000)}
	usage := func() map[string]any {
		pick := []int{0, 0, 10, 500, 3000}
		u := map[string]any{"input": pick[rng.Intn(5)], "output": pick[rng.Intn(5)], "cacheRead": pick[rng.Intn(5)], "cacheWrite": pick[rng.Intn(5)], "cost": map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0, "total": 0}}
		u["totalTokens"] = 0
		if rng.Intn(2) == 0 {
			u["totalTokens"] = u["input"].(int) + u["output"].(int) + u["cacheRead"].(int) + u["cacheWrite"].(int)
		}
		return u
	}
	text := func() []any { return []any{map[string]any{"type": "text", "text": texts[rng.Intn(len(texts))]}} }
	entry := func(i int) map[string]any {
		fields := map[string]any{"id": fmt.Sprintf("e%d", i), "parentId": nil, "timestamp": "2026-01-01T00:00:00.000Z"}
		if i > 0 {
			fields["parentId"] = fmt.Sprintf("e%d", i-1)
		}
		switch rng.Intn(12) {
		case 0, 1:
			fields["type"], fields["message"] = "message", map[string]any{"role": "user", "content": text(), "timestamp": 1}
		case 2, 3, 4:
			content := text()
			if rng.Intn(3) == 0 {
				content = append(content, map[string]any{"type": "toolCall", "id": "c", "name": "read", "arguments": map[string]any{"path": texts[rng.Intn(len(texts))]}})
			}
			stop := []string{"stop", "stop", "toolUse", "aborted", "error"}[rng.Intn(5)]
			fields["type"], fields["message"] = "message", map[string]any{"role": "assistant", "content": content, "api": "anthropic-messages", "provider": "anthropic", "model": "m", "usage": usage(), "stopReason": stop, "timestamp": 1}
		case 5, 6:
			fields["type"], fields["message"] = "message", map[string]any{"role": "toolResult", "toolCallId": "c", "toolName": "read", "content": text(), "isError": false, "timestamp": 1}
		case 7:
			fields["type"], fields["message"] = "message", map[string]any{"role": "bashExecution", "command": "ls", "output": texts[rng.Intn(len(texts))], "exitCode": 0, "cancelled": false, "truncated": false, "timestamp": 1, "excludeFromContext": rng.Intn(3) == 0}
		case 8:
			fields["type"], fields["customType"], fields["content"], fields["display"] = "custom_message", "t", texts[rng.Intn(len(texts))], true
		case 9:
			fields["type"], fields["fromId"], fields["summary"] = "branch_summary", "x", texts[rng.Intn(len(texts))]
		case 10:
			fields["type"], fields["summary"], fields["firstKeptEntryId"], fields["tokensBefore"] = "compaction", texts[rng.Intn(len(texts))], "e0", 100
		default:
			fields["type"], fields["thinkingLevel"] = "thinking_level_change", "high"
		}
		return fields
	}
	type branch struct {
		Entries []map[string]any `json:"entries"`
		Start   int              `json:"start"`
		End     int              `json:"end"`
		Keep    int              `json:"keep"`
	}
	var branches []branch
	for range 300 {
		n := 1 + rng.Intn(14)
		b := branch{Start: 0, Keep: []int{0, 1, 20, 100, 500, 1500, 5000}[rng.Intn(7)]}
		for i := range n {
			b.Entries = append(b.Entries, entry(i))
		}
		b.End = n
		if n > 2 && rng.Intn(3) == 0 {
			b.Start = rng.Intn(n / 2)
			b.End = n - rng.Intn(n/2)
		}
		branches = append(branches, b)
	}
	type result struct {
		FirstKeptEntryIndex int  `json:"firstKeptEntryIndex"`
		TurnStartIndex      int  `json:"turnStartIndex"`
		IsSplitTurn         bool `json:"isSplitTurn"`
		TurnStart           int  `json:"turnStart"`
		LastUsageInput      int  `json:"lastUsageInput"`
		HasLastUsage        bool `json:"hasLastUsage"`
		Tokens              int  `json:"tokens"`
		UsageTokens         int  `json:"usageTokens"`
		TrailingTokens      int  `json:"trailingTokens"`
		LastUsageIndex      int  `json:"lastUsageIndex"`
	}
	var want []result
	pioracle.Run(t, `
const mod = await load("pi-coding-agent/core/compaction/compaction.js");
const sm = await load("pi-coding-agent/core/session-manager.js");
emit(input.map((b) => {
	const cut = mod.findCutPoint(b.entries, b.start, b.end, b.keep);
	const usage = mod.getLastAssistantUsage(b.entries);
	const messages = b.entries.flatMap((e) => sm.sessionEntryToContextMessages(e));
	const est = mod.estimateContextTokens(messages);
	return { firstKeptEntryIndex: cut.firstKeptEntryIndex, turnStartIndex: cut.turnStartIndex, isSplitTurn: cut.isSplitTurn,
		turnStart: mod.findTurnStartIndex(b.entries, b.end - 1, b.start), lastUsageInput: usage?.input ?? 0, hasLastUsage: usage !== undefined,
		tokens: est.tokens, usageTokens: est.usageTokens, trailingTokens: est.trailingTokens, lastUsageIndex: est.lastUsageIndex ?? -1 };
}));`, branches, &want)
	mismatches := 0
	for i, b := range branches {
		entries := make([]codingagent.SessionEntry, len(b.Entries))
		for j, fields := range b.Entries {
			entries[j] = mustSessionEntry(t, fields)
		}
		cut := FindCutPoint(entries, b.Start, b.End, b.Keep)
		got := result{FirstKeptEntryIndex: cut.FirstKeptEntryIndex, TurnStartIndex: cut.TurnStartIndex, IsSplitTurn: cut.IsSplitTurn, TurnStart: FindTurnStartIndex(entries, b.End-1, b.Start), LastUsageIndex: -1}
		if usage := GetLastAssistantUsage(entries); usage != nil {
			got.HasLastUsage, got.LastUsageInput = true, usage.Input
		}
		var messages []agent.AgentMessage
		for _, e := range entries {
			messages = append(messages, codingagent.SessionEntryToContextMessages(e)...)
		}
		estimate := EstimateContextTokens(messages)
		got.Tokens, got.UsageTokens, got.TrailingTokens = estimate.Tokens, estimate.UsageTokens, estimate.TrailingTokens
		got.LastUsageIndex = estimate.LastUsageIndex
		if got != want[i] {
			if mismatches++; mismatches <= 6 {
				raw, _ := json.Marshal(b)
				t.Errorf("branch %d (%s)\n Go %+v\n Pi %+v", i, raw, got, want[i])
			}
		}
	}
	if mismatches > 6 {
		t.Errorf("%d mismatches in %d branches", mismatches, len(branches))
	}
}
