package coding

import (
	"github.com/MichaelKinsy/PiG/ai"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/codingagent/compaction"
)

// CompactionSettings is upstream CompactionSettings (compaction.ts:115), exported with the compaction helpers upstream's index.ts exports.
type CompactionSettings = compaction.CompactionSettings

// CutPointResult is upstream CutPointResult (compaction.ts:383).
type CutPointResult = compaction.CutPointResult

// SessionEntry is upstream SessionEntry: one entry of a session file, as FindCutPoint and GetLastAssistantUsage read it.
type SessionEntry = icodingagent.SessionEntry

// DefaultCompactionSettings is upstream DEFAULT_COMPACTION_SETTINGS.
var DefaultCompactionSettings = compaction.DefaultCompactionSettings

// FindCutPoint is upstream findCutPoint: the entry index to keep from so that about keepRecentTokens of recent context remains between startIndex and endIndex (exclusive).
func FindCutPoint(entries []SessionEntry, startIndex, endIndex, keepRecentTokens int) CutPointResult {
	return compaction.FindCutPoint(entries, startIndex, endIndex, keepRecentTokens)
}

// FindTurnStartIndex is upstream findTurnStartIndex: the index of the user-role entry that starts the turn containing entryIndex, or -1.
func FindTurnStartIndex(entries []SessionEntry, entryIndex, startIndex int) int {
	return compaction.FindTurnStartIndex(entries, entryIndex, startIndex)
}

// GetLastAssistantUsage is upstream getLastAssistantUsage: the usage of the last assistant message that reports one, or nil.
func GetLastAssistantUsage(entries []SessionEntry) *ai.Usage {
	return compaction.GetLastAssistantUsage(entries)
}
