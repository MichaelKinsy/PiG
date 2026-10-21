package sdk

// Pi's findCutPoint and findTurnStartIndex (core/compaction/compaction.ts) over the session entries an extension gets (for example from
// [ReadonlySessionManager.GetEntries]), each a decoded JSON object with a "type".

import (
	"maps"
	"slices"
)

// CutPointResult is Pi's CutPointResult.
type CutPointResult struct {
	// FirstKeptEntryIndex is the index of the first entry to keep.
	FirstKeptEntryIndex int `json:"firstKeptEntryIndex"`
	// TurnStartIndex is the index of the message that starts the turn being split, or -1 when no turn is split.
	TurnStartIndex int `json:"turnStartIndex"`
	// IsSplitTurn reports that the cut point is not a turn start.
	IsSplitTurn bool `json:"isSplitTurn"`
}

// sessionEntryToContextMessages is Pi's sessionEntryToContextMessages (core/session-manager.ts) for the entries the cut point logic reads. Session files are
// parsed without validation, so a message with null or missing content reads as an empty one, and a message that is not an object (a number, string,
// array or boolean) is still one context message: it has no role, so it is neither a cut point nor a turn start and weighs nothing, but it stops the cut's
// move back over metadata entries. A null or missing message, on which Pi throws, has none.
func sessionEntryToContextMessages(entry map[string]any) []map[string]any {
	switch kind, _ := entry["type"].(string); kind {
	case "message":
		message, isObject := entry["message"].(map[string]any)
		if !isObject {
			if entry["message"] == nil {
				return nil
			}
			return []map[string]any{{}}
		}
		role, _ := message["role"].(string)
		if message["content"] == nil {
			switch role {
			case "system":
				return []map[string]any{withContent(message, "")}
			case "user", "assistant", "toolResult":
				return []map[string]any{withContent(message, []any{})}
			}
		}
		return []map[string]any{message}
	case "custom_message":
		content := entry["content"]
		if content == nil {
			content = []any{}
		}
		return []map[string]any{{"role": "custom", "content": content}}
	case "branch_summary":
		if summary, _ := entry["summary"].(string); summary != "" {
			return []map[string]any{{"role": "branchSummary", "summary": summary}}
		}
	case "compaction":
		summary, _ := entry["summary"].(string)
		messages := []map[string]any{}
		if system, ok := entry["systemMessage"].(map[string]any); ok && jsTruthy(system) {
			messages = append(messages, system)
		}
		return append(messages, map[string]any{"role": "compactionSummary", "summary": summary})
	}
	return nil
}

func withContent(message map[string]any, content any) map[string]any {
	copied := make(map[string]any, len(message))
	maps.Copy(copied, message)
	copied["content"] = content
	return copied
}

func entryType(entry map[string]any) string {
	kind, _ := entry["type"].(string)
	return kind
}

func entryAt(entries []map[string]any, index int) map[string]any {
	if index < 0 || index >= len(entries) {
		return nil
	}
	return entries[index]
}

func isCutPointMessage(message map[string]any) bool {
	switch role, _ := message["role"].(string); role {
	case "user", "assistant", "bashExecution", "custom", "branchSummary", "compactionSummary":
		return true
	}
	return false
}

func isTurnStartMessage(message map[string]any) bool {
	switch role, _ := message["role"].(string); role {
	case "user", "bashExecution", "custom", "branchSummary", "compactionSummary":
		return true
	}
	return false
}

func isTurnStartEntry(entry map[string]any) bool {
	if entryType(entry) == "compaction" {
		return false
	}
	return slices.ContainsFunc(sessionEntryToContextMessages(entry), isTurnStartMessage)
}

// findValidCutPoints lists the indices of context-visible user-like or assistant messages: a tool result is never a cut point because it must follow its call.
func findValidCutPoints(entries []map[string]any, startIndex, endIndex int) []int {
	var cutPoints []int
	for i := startIndex; i < endIndex; i++ {
		entry := entryAt(entries, i)
		if entryType(entry) == "compaction" {
			continue
		}
		if slices.ContainsFunc(sessionEntryToContextMessages(entry), isCutPointMessage) {
			cutPoints = append(cutPoints, i)
		}
	}
	return cutPoints
}

// FindTurnStartIndex is Pi's findTurnStartIndex: the index of the context-visible message that starts the turn holding entryIndex, or -1 when none starts
// it at or after startIndex.
func FindTurnStartIndex(entries []map[string]any, entryIndex, startIndex int) int {
	for i := entryIndex; i >= startIndex; i-- {
		if isTurnStartEntry(entryAt(entries, i)) {
			return i
		}
	}
	return -1
}

// FindCutPoint is Pi's findCutPoint: it walks back from the newest entry before endIndex, adding estimated message sizes until keepRecentTokens is
// reached, and cuts at the closest valid cut point at or after that entry (never at a tool result), then moves the cut back over adjacent entries that do
// not affect the context.
func FindCutPoint(entries []map[string]any, startIndex, endIndex, keepRecentTokens int) CutPointResult {
	cutPoints := findValidCutPoints(entries, startIndex, endIndex)
	if len(cutPoints) == 0 {
		return CutPointResult{FirstKeptEntryIndex: startIndex, TurnStartIndex: -1}
	}
	accumulatedTokens := 0
	cutIndex := cutPoints[0]
	for i := endIndex - 1; i >= startIndex; i-- {
		messageTokens := 0
		for _, message := range sessionEntryToContextMessages(entryAt(entries, i)) {
			messageTokens += EstimateTokens(message)
		}
		if messageTokens == 0 {
			continue
		}
		accumulatedTokens += messageTokens
		if accumulatedTokens >= keepRecentTokens {
			cutIndex = cutPoints[len(cutPoints)-1]
			for _, candidate := range cutPoints {
				if candidate >= i {
					cutIndex = candidate
					break
				}
			}
			break
		}
	}
	for cutIndex > startIndex {
		previous := entryAt(entries, cutIndex-1)
		if entryType(previous) == "compaction" || len(sessionEntryToContextMessages(previous)) > 0 {
			break
		}
		cutIndex--
	}
	startsTurn := isTurnStartEntry(entryAt(entries, cutIndex))
	turnStartIndex := -1
	if !startsTurn {
		turnStartIndex = FindTurnStartIndex(entries, cutIndex, startIndex)
	}
	return CutPointResult{FirstKeptEntryIndex: cutIndex, TurnStartIndex: turnStartIndex, IsSplitTurn: !startsTurn && turnStartIndex != -1}
}
