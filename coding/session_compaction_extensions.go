package coding

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/codingagent/compaction"
)

type sessionCompactionData struct {
	Summary          string
	FirstKeptEntryID string
	TokensBefore     int
	Usage            *ai.Usage
	Details          any
}

func (s *Session) extensionCompaction(
	ctx context.Context,
	preparation *compaction.CompactionPreparation,
	entries []icodingagent.SessionEntry,
	customInstructions, reason string,
	willRetry bool,
) (*sessionCompactionData, bool, error) {
	runner := s.currentRunner()
	if runner == nil || !runner.HasHandlers(icodingagent.EventSessionBeforeCompact) {
		return nil, false, nil
	}
	branchEntries := make([]extension.SessionEntry, len(entries))
	for i := range entries {
		branchEntries[i] = entries[i]
	}
	result, err := runner.Emit(ctx, extension.SessionBeforeCompactEvent{
		Type:               icodingagent.EventSessionBeforeCompact,
		Preparation:        preparation,
		BranchEntries:      branchEntries,
		CustomInstructions: customInstructions,
		Reason:             reason,
		WillRetry:          willRetry,
		Signal:             ctx,
	})
	if err != nil {
		return nil, false, err
	}
	if result == nil {
		return nil, false, nil
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return nil, false, err
	}
	var envelope struct {
		Cancel     bool            `json:"cancel"`
		Compaction json.RawMessage `json:"compaction"`
	}
	if err := json.Unmarshal(encoded, &envelope); err != nil {
		return nil, false, err
	}
	if envelope.Cancel {
		return nil, false, errors.New("Compaction cancelled")
	}
	if len(envelope.Compaction) == 0 || string(envelope.Compaction) == "null" {
		return nil, false, nil
	}
	var wire struct {
		Summary          string          `json:"summary"`
		FirstKeptEntryID string          `json:"firstKeptEntryId"`
		TokensBefore     int             `json:"tokensBefore"`
		Usage            *ai.Usage       `json:"usage,omitempty"`
		Details          json.RawMessage `json:"details,omitempty"`
	}
	if err := json.Unmarshal(envelope.Compaction, &wire); err != nil {
		return nil, false, err
	}
	var details any
	if len(wire.Details) > 0 && string(wire.Details) != "null" {
		if err := json.Unmarshal(wire.Details, &details); err != nil {
			return nil, false, err
		}
	}
	return &sessionCompactionData{
		Summary:          wire.Summary,
		FirstKeptEntryID: wire.FirstKeptEntryID,
		TokensBefore:     wire.TokensBefore,
		Usage:            wire.Usage,
		Details:          details,
	}, true, nil
}

func generatedCompactionData(result compaction.CompactionResult) *sessionCompactionData {
	return &sessionCompactionData{
		Summary:          result.Summary,
		FirstKeptEntryID: result.FirstKeptEntryID,
		TokensBefore:     result.TokensBefore,
		Usage:            result.Usage,
		Details:          result.Details,
	}
}

func (s *Session) emitSessionCompact(ctx context.Context, entry icodingagent.SessionEntry, fromExtension bool, reason string, willRetry bool) {
	runner := s.currentRunner()
	if runner == nil || !runner.HasHandlers(icodingagent.EventSessionCompact) {
		return
	}
	_, _ = runner.Emit(ctx, extension.SessionCompactEvent{
		Type:            icodingagent.EventSessionCompact,
		CompactionEntry: entry,
		FromExtension:   fromExtension,
		Reason:          reason,
		WillRetry:       willRetry,
	})
}

// emitSessionCompactFailed notifies extensions that compaction failed or was
// aborted (agent-session.ts _emitSessionCompactFailed). Upstream awaits it
// after compaction_end, including after an abort, so dispatch must not
// inherit the cancellation that aborted the compaction.
func (s *Session) emitSessionCompactFailed(ctx context.Context, event extension.SessionCompactFailedEvent) {
	runner := s.currentRunner()
	if runner == nil || !runner.HasHandlers(icodingagent.EventSessionCompactFailed) {
		return
	}
	event.Type = icodingagent.EventSessionCompactFailed
	_, _ = runner.Emit(context.WithoutCancel(ctx), event)
}
