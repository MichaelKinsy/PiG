// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package driver

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/harness"
	"github.com/MichaelKinsy/PiG/durable/session"
)

// WatchEvents attaches to one conversation's agent events (events.ts watchEvents) over a view watch of the core with
// changes: the snapshot is the view at attachment, and each frame the core publishes for the conversation, with the records
// the commit wrote, becomes the batch of events the commit causes.
func (s *coreSession) WatchEvents(ctx context.Context, conversationId durable.ConversationId) (*harness.AgentEventStream, error) {
	held, err := s.completingGenerations(ctx, conversationId)
	if err != nil {
		return nil, err
	}
	m, err := openWatch(ctx, s, map[string]any{"kind": "view", "conversationId": conversationId, "changes": true}, typed[harness.ConversationView], viewReducer())
	if err != nil {
		return nil, err
	}
	if m == nil {
		return nil, fmt.Errorf("Conversation %d does not exist", conversationId)
	}
	current := m.first
	snapshot := harness.ViewSnapshotEvent(current)
	var once sync.Once
	watch := session.NewCommittedWatchOn(m.hub.sched, []harness.AgentEvent{}, func() { once.Do(func() { m.hub.release(m.id); m.closeCore() }) },
		func() []harness.AgentEvent { return []harness.AgentEvent{harness.ViewSnapshotEvent(current)} }, m.hub.report)
	m.hub.bind(m.id, func(f rawFrame) {
		after, ok := m.advance(f)
		if !ok {
			return
		}
		before := current
		current = after
		publication, err := framePublication(conversationId, f.changes)
		if err != nil {
			s.reportListener(err)
			return
		}
		if events := harness.TranslateViewEvents(conversationId, before, after, f.ops, publication, held); len(events) > 0 {
			watch.Advance(m.hub.deliveryContext(), events, nil)
		}
	}, func() { watch.SourceEnded(nil) })
	if ctx.Done() != nil {
		watch.ObserveCancellation(ctx)
	}
	return harness.NewAgentEventStream(snapshot, watch), nil
}

// completingGenerations are the generations of the conversation whose held outcome already ended their turn, read with the
// snapshot.
func (s *coreSession) completingGenerations(ctx context.Context, conversationId durable.ConversationId) (map[durable.TaskId]bool, error) {
	held := map[durable.TaskId]bool{}
	kind, status := "pi.generation", durable.TaskCompleting
	query := durable.TaskQuery{ConversationId: &conversationId, Kind: &kind, Status: &status}
	_, err := s.commit(ctx, map[string]any{"conversationId": conversationId}, func(tx durable.Tx) (any, error) {
		records, err := harness.ScanAll(func(cursor durable.Cursor) (durable.Page[durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue], durable.Cursor], error) {
			return tx.ScanTasks(query, 100, cursor)
		})
		for _, record := range records {
			held[record.Id] = true
		}
		return nil, err
	})
	return held, err
}

// framePublication is the publication of a view frame's changes: the entries, tasks and submissions the commit wrote for the
// conversation, and the nested results it stored.
func framePublication(conversationId durable.ConversationId, raw json.RawMessage) (durable.CommitPublication, error) {
	var publication durable.CommitPublication
	if len(raw) == 0 {
		return publication, nil
	}
	var changes struct {
		Entries       []durable.EntryRecord                                                         `json:"entries"`
		Tasks         []durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue] `json:"tasks"`
		Submissions   []durable.SubmissionRecord                                                    `json:"submissions"`
		NestedResults []struct {
			Key   string          `json:"key"`
			Value json.RawMessage `json:"value"`
		} `json:"nestedResults"`
	}
	if err := json.Unmarshal(raw, &changes); err != nil {
		return publication, err
	}
	for _, record := range changes.Entries {
		publication.Changes = append(publication.Changes, durable.EntryWrite{Value: record})
	}
	for _, record := range changes.Tasks {
		publication.Changes = append(publication.Changes, durable.TaskWrite{Value: record})
	}
	for _, record := range changes.Submissions {
		publication.Changes = append(publication.Changes, durable.SubmissionWrite{Value: record})
	}
	for _, result := range changes.NestedResults {
		value, err := delta.DecodeJson(result.Value)
		if err != nil {
			return publication, err
		}
		object, _ := value.(durable.JsonObject)
		key := result.Key
		publication.Changes = append(publication.Changes, durable.DocumentChange{
			Record:         durable.DocumentRecord{Kind: "pi.tool.nested-call", Key: &key},
			ConversationId: &conversationId,
			Value:          object,
		})
	}
	return publication, nil
}
