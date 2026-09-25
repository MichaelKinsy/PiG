package session

import (
	"context"
	"slices"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// SessionContextBuildOptions configure BuildSessionContext.
type SessionContextBuildOptions struct {
	EntryProjectors map[string]EntryProjector
}

// BuildContextEntries returns the latest compaction and every entry after it,
// or the whole path when no compaction exists.
func BuildContextEntries(pathEntries []Entry) []Entry {
	for index, pathEntrie := range slices.Backward(pathEntries) {
		if pathEntrie.Type == EntryTypeCompaction {
			return append([]Entry{pathEntrie}, pathEntries[index+1:]...)
		}
	}
	return append([]Entry{}, pathEntries...)
}

// isContextMessage drops assistant responses that stopped with error,
// aborted, or deferred.
func isContextMessage(message agent.AgentMessage) bool {
	if message.Assistant == nil {
		return true
	}
	switch message.Assistant.StopReason {
	case ai.StopReasonError, ai.StopReasonAborted, ai.StopReasonDeferred:
		return false
	default:
		return true
	}
}

// SessionEntryToContextMessages projects one non-custom entry into model
// context; custom entries project nothing here.
func SessionEntryToContextMessages(entry Entry) []agent.AgentMessage {
	switch entry.Type {
	case EntryTypeMessage:
		if isContextMessage(entry.Message) {
			return []agent.AgentMessage{entry.Message}
		}
		return []agent.AgentMessage{}
	case EntryTypeCompaction:
		messages := []agent.AgentMessage{compactionSummaryMessage(entry.Summary, entry.TokensBefore, entry.Timestamp)}
		for _, message := range entry.RetainedTail {
			if isContextMessage(message) {
				messages = append(messages, message)
			}
		}
		return messages
	case EntryTypeBranchSummary:
		if entry.Summary == "" {
			return []agent.AgentMessage{}
		}
		return []agent.AgentMessage{branchSummaryMessage(entry.Summary, entry.FromID, entry.Timestamp)}
	default:
		return []agent.AgentMessage{}
	}
}

// BuildSessionContext projects a branch path into model context: the latest
// compaction checkpoint and later entries, with custom entries run through
// their projectors in path order. A projector error aborts the build.
func BuildSessionContext(ctx context.Context, pathEntries []Entry, options *SessionContextBuildOptions) ([]agent.AgentMessage, error) {
	messages := []agent.AgentMessage{}
	for _, entry := range BuildContextEntries(pathEntries) {
		if entry.Type != EntryTypeCustom {
			messages = append(messages, SessionEntryToContextMessages(entry)...)
			continue
		}
		if options == nil {
			continue
		}
		projector, ok := options.EntryProjectors[entry.CustomType]
		if !ok || projector == nil {
			continue
		}
		projected, err := projector(ctx, entry)
		if err != nil {
			return nil, err
		}
		messages = append(messages, projected...)
	}
	return messages, nil
}

// compactionSummaryMessage is the harness createCompactionSummaryMessage.
func compactionSummaryMessage(summary string, tokensBefore int, timestamp int64) agent.AgentMessage {
	return agent.AgentMessage{Custom: map[string]any{
		"role": agent.RoleCompactionSummary, "summary": summary, "tokensBefore": tokensBefore, "timestamp": timestamp,
	}}
}

// branchSummaryMessage is the harness createBranchSummaryMessage.
func branchSummaryMessage(summary string, fromID *string, timestamp int64) agent.AgentMessage {
	var from any
	if fromID != nil {
		from = *fromID
	}
	return agent.AgentMessage{Custom: map[string]any{
		"role": agent.RoleBranchSummary, "summary": summary, "fromId": from, "timestamp": timestamp,
	}}
}
