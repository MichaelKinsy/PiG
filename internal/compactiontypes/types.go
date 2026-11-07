// Package compactiontypes holds the compaction types that both the extension API and the compaction pipeline name:
// Pi's CompactionPreparation, CompactionSettings and FileOperations (core/compaction). It imports no coding package, so
// coding/extension can use them without an import cycle.
package compactiontypes

import (
	"encoding/json"
	"maps"
	"slices"
	"unicode/utf16"

	"github.com/MichaelKinsy/PiG/agent"
)

// CompactionSettings controls when and how compaction runs.
// Mirrors upstream CompactionSettings (compaction.ts:115).
type CompactionSettings struct {
	Enabled          bool `json:"enabled"`
	ReserveTokens    int  `json:"reserveTokens"`    // tokens reserved for response; default 16384
	KeepRecentTokens int  `json:"keepRecentTokens"` // tokens to keep from recent history; default 20000
}

// FileOperations tracks files read/written/edited during a session segment.
// Mirrors upstream FileOperations (utils.ts): three Sets of paths.
type FileOperations struct {
	Read    map[string]struct{}
	Written map[string]struct{}
	Edited  map[string]struct{}
}

// MarshalJSON carries each Set on the subprocess wire as a sorted string array.
func (ops FileOperations) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Read    []string `json:"read"`
		Written []string `json:"written"`
		Edited  []string `json:"edited"`
	}{SortedPaths(ops.Read), SortedPaths(ops.Written), SortedPaths(ops.Edited)})
}

// SortedPaths returns the Set's paths in JavaScript sort order (UTF-16 code units).
func SortedPaths(set map[string]struct{}) []string {
	paths := slices.Collect(maps.Keys(set))
	if paths == nil {
		paths = []string{}
	}
	slices.SortFunc(paths, func(a, b string) int { return slices.Compare(utf16.Encode([]rune(a)), utf16.Encode([]rune(b))) })
	return paths
}

// CompactionPreparation is the output of PrepareCompaction.
// Mirrors upstream CompactionPreparation (compaction.ts:600).
//
// Extensions receive it in session_before_compact, so the JSON keys are
// upstream's field names.
type CompactionPreparation struct {
	FirstKeptEntryID    string               `json:"firstKeptEntryId"`
	MessagesToSummarize []agent.AgentMessage `json:"messagesToSummarize"`
	TurnPrefixMessages  []agent.AgentMessage `json:"turnPrefixMessages"`
	IsSplitTurn         bool                 `json:"isSplitTurn"`
	TokensBefore        int                  `json:"tokensBefore"`
	PreviousSummary     string               `json:"previousSummary,omitempty"`
	FileOps             FileOperations       `json:"fileOps"`
	Settings            CompactionSettings   `json:"settings"`
}
