package compaction

import (
	"encoding/json"
	"slices"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

func isProjectedTurnStart(entry codingagent.ProjectedSessionEntry) bool {
	if entry.SourceEntry.Base.Type == "compaction" {
		return false
	}
	return slices.ContainsFunc(entry.Messages, isTurnStartMessage)
}

func findProjectedTurnStartIndex(entries []codingagent.ProjectedSessionEntry, entryIndex, startIndex int) int {
	for i := entryIndex; i >= startIndex; i-- {
		if isProjectedTurnStart(entries[i]) {
			return i
		}
	}
	return -1
}

// isIntrinsicallyVisible reports whether an entry contributes context before
// context edits apply. Context edits themselves never do.
func isIntrinsicallyVisible(entry codingagent.ProjectedSessionEntry) bool {
	return entry.SourceEntry.Base.Type != "context_edit" && len(codingagent.SessionEntryToContextMessages(entry.SourceEntry)) > 0
}

func isOmitted(entry codingagent.ProjectedSessionEntry) bool {
	return isIntrinsicallyVisible(entry) && len(entry.Messages) == 0
}

func isOmittedAssistantAttempt(entry codingagent.ProjectedSessionEntry) bool {
	if entry.SourceEntry.Base.Type != "message" || !isOmitted(entry) {
		return false
	}
	message, ok := entry.SourceEntry.AsMessage()
	return ok && message.Message.Assistant != nil
}

// isRecoveryOmissionSuffix reports whether the entries after a cut form a
// closed recovery suffix: an omitted assistant attempt, its omission edits, and
// context-invisible metadata only. A replacement edit of any entry that is not
// itself omitted in the suffix blocks it, because the omitted attempt answered
// the pre-edit input.
func isRecoveryOmissionSuffix(suffix []codingagent.ProjectedSessionEntry) bool {
	omittedIDs := make(map[string]struct{})
	for _, entry := range suffix {
		if isOmitted(entry) {
			omittedIDs[entry.SourceEntry.Base.ID] = struct{}{}
		}
	}
	for _, entry := range suffix {
		if entry.SourceEntry.Base.Type != "context_edit" {
			continue
		}
		var edit codingagent.ContextEditEntry
		if json.Unmarshal(entry.SourceEntry.Raw(), &edit) != nil || edit.Replacement == nil {
			continue
		}
		if _, omitted := omittedIDs[edit.TargetID]; !omitted {
			return false
		}
	}
	if !slices.ContainsFunc(suffix, isOmittedAssistantAttempt) {
		return false
	}
	for _, entry := range suffix {
		if entry.SourceEntry.Base.Type == "compaction" || (isIntrinsicallyVisible(entry) && !isOmitted(entry)) {
			return false
		}
	}
	return true
}

// findProjectedCutPoint finds the cut point over projected entries
// (compaction.ts findProjectedCutPoint). Only projected messages count, so
// omitted entries neither become cut points nor consume the keep budget.
func findProjectedCutPoint(entries []codingagent.ProjectedSessionEntry, startIndex, endIndex, keepRecentTokens int) CutPointResult {
	var cutPoints []int
	for i := startIndex; i < endIndex; i++ {
		if entries[i].SourceEntry.Base.Type != "compaction" && slices.ContainsFunc(entries[i].Messages, isCutPointMessage) {
			cutPoints = append(cutPoints, i)
		}
	}
	if len(cutPoints) == 0 {
		return CutPointResult{FirstKeptEntryIndex: startIndex, TurnStartIndex: -1}
	}

	accumulated := 0
	exceededBudget := false
	cutIndex := cutPoints[0]
	for i := endIndex - 1; i >= startIndex; i-- {
		tokens := estimateMessagesTokens(entries[i].Messages)
		if tokens == 0 {
			continue
		}
		accumulated += tokens
		if accumulated >= keepRecentTokens {
			exceededBudget = true
			cutIndex = closestCutPointAtOrAfter(cutPoints, i)
			break
		}
	}

	// A recovery attempt and its omission edits are context-invisible after the
	// last visible input. Advance only past such a closed suffix; arbitrary
	// metadata must not move the cut past unsent input.
	if exceededBudget && isRecoveryOmissionSuffix(entries[cutIndex+1:endIndex]) {
		cutIndex++
	}

	for cutIndex > startIndex {
		previous := entries[cutIndex-1]
		if previous.SourceEntry.Base.Type == "compaction" || len(previous.Messages) > 0 {
			break
		}
		cutIndex--
	}
	startsTurn := isProjectedTurnStart(entries[cutIndex])
	turnStartIndex := -1
	if !startsTurn {
		turnStartIndex = findProjectedTurnStartIndex(entries, cutIndex, startIndex)
	}
	return CutPointResult{
		FirstKeptEntryIndex: cutIndex,
		TurnStartIndex:      turnStartIndex,
		IsSplitTurn:         !startsTurn && turnStartIndex != -1,
	}
}
