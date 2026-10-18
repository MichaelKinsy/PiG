// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package driver

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/core/abi"
	"github.com/MichaelKinsy/PiG/durable/session"
)

// Commit publications (PROTOCOL.md, `commits`): while at least one listener is subscribed, the core attaches the complete
// publication {seq, changes} to each commit's `published` notice. A table change is {type, value}; a document change is
// {type: "document", record, conversationId?, version?, value, ops}; a copied document is {type: "document.copy", record,
// conversationId, source: {id, at}}.

// commitListeners are the SubscribeCommits listeners of a Session, in subscription order.
type commitListeners struct {
	next int
	ids  []int
	fns  map[int]func(context.Context, durable.CommitPublication)

	queue   []queuedPublication
	running bool
}

// notice delivers the publication a published notice carries to every listener, on its own goroutine so listener code never
// runs on the owner. A malformed publication is dropped: the core produced it.
func (s *coreSession) commitNotice(n abi.Notice) {
	if n.Kind != abi.NoticePublished {
		return
	}
	var p struct {
		WatchID *uint32           `json:"watchId"`
		Seq     durable.Seq       `json:"seq"`
		Changes []json.RawMessage `json:"changes"`
	}
	if json.Unmarshal(n.Payload, &p) != nil || p.WatchID != nil || p.Changes == nil {
		return
	}
	publication := durable.CommitPublication{Seq: p.Seq}
	for _, raw := range p.Changes {
		change, err := decodeCommitChange(raw)
		if err != nil {
			s.reportListener(err)
			return
		}
		publication.Changes = append(publication.Changes, change)
	}
	s.mu.Lock()
	fns := make([]func(context.Context, durable.CommitPublication), 0, len(s.commits.ids))
	for _, id := range s.commits.ids {
		fns = append(fns, s.commits.fns[id])
	}
	s.mu.Unlock()
	if len(fns) == 0 {
		return
	}
	s.deliverCommits(fns, publication)
}

func decodeCommitChange(raw json.RawMessage) (durable.CommitChange, error) {
	var head struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(raw, &head); err != nil {
		return nil, err
	}
	switch head.Type {
	case "conversation":
		var c struct {
			Value durable.ConversationRecord `json:"value"`
		}
		err := json.Unmarshal(raw, &c)
		return durable.ConversationWrite{Value: c.Value}, err
	case "entry":
		var c struct {
			Value durable.EntryRecord `json:"value"`
		}
		err := json.Unmarshal(raw, &c)
		return durable.EntryWrite{Value: c.Value}, err
	case "task":
		var c struct {
			Value durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue] `json:"value"`
		}
		err := json.Unmarshal(raw, &c)
		return durable.TaskWrite{Value: c.Value}, err
	case "submission":
		var c struct {
			Value durable.SubmissionRecord `json:"value"`
		}
		err := json.Unmarshal(raw, &c)
		return durable.SubmissionWrite{Value: c.Value}, err
	case "document":
		var c struct {
			Record         durable.DocumentRecord  `json:"record"`
			ConversationID *durable.ConversationId `json:"conversationId"`
			Version        *int                    `json:"version"`
			Value          durable.JsonObject      `json:"value"`
			Ops            []durable.Op            `json:"ops"`
		}
		err := json.Unmarshal(raw, &c)
		return durable.DocumentChange{Record: c.Record, ConversationId: c.ConversationID, Version: c.Version, Value: c.Value, Ops: c.Ops}, err
	case "document.copy":
		var c struct {
			Record         durable.DocumentRecord `json:"record"`
			ConversationID durable.ConversationId `json:"conversationId"`
			Source         struct {
				ID durable.DocumentId `json:"id"`
				At json.RawMessage    `json:"at"`
			} `json:"source"`
		}
		if err := json.Unmarshal(raw, &c); err != nil {
			return nil, err
		}
		at := durable.CurrentPoint
		if string(c.Source.At) != `"current"` {
			var seq durable.Seq
			if err := json.Unmarshal(c.Source.At, &seq); err != nil {
				return nil, err
			}
			at = durable.AtSeq(seq)
		}
		return durable.DocumentCopyChange{Record: c.Record, ConversationId: c.ConversationID, Source: durable.DocumentCopySource{Id: c.Source.ID, At: at}}, nil
	}
	return nil, fmt.Errorf("the core published a change of unknown type %q", head.Type)
}

// SubscribeCommits observes every successful Session commit (session.ts subscribeCommits). The first listener asks the core
// to attach the complete publication to its commit notices; the last one to leave turns that off.
func (s *coreSession) SubscribeCommits(listener func(ctx context.Context, publication durable.CommitPublication)) func() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return func() {}
	}
	s.commits.next++
	id := s.commits.next
	if s.commits.fns == nil {
		s.commits.fns = map[int]func(context.Context, durable.CommitPublication){}
	}
	s.commits.fns[id] = listener
	s.commits.ids = append(s.commits.ids, id)
	first := len(s.commits.ids) == 1
	s.mu.Unlock()
	if first {
		s.setCommitChanges(true)
	}
	return func() {
		s.mu.Lock()
		if _, ok := s.commits.fns[id]; !ok {
			s.mu.Unlock()
			return
		}
		delete(s.commits.fns, id)
		for i, v := range s.commits.ids {
			if v == id {
				s.commits.ids = append(s.commits.ids[:i], s.commits.ids[i+1:]...)
				break
			}
		}
		last := len(s.commits.ids) == 0 && !s.closed
		s.mu.Unlock()
		if last {
			s.setCommitChanges(false)
		}
	}
}

// setCommitChanges asks the core to attach (or stop attaching) the complete publication; the subscription is in effect when
// SubscribeCommits returns, as Pi's is.
func (s *coreSession) setCommitChanges(on bool) {
	_, _ = s.client.API(context.Background(), "commits", map[string]any{"on": on})
}

// deliverCommits hands a publication to the listeners on one delivery goroutine per Session, so publications reach every
// listener in commit order and listener code never runs on the owner goroutine.
func (s *coreSession) deliverCommits(fns []func(context.Context, durable.CommitPublication), p durable.CommitPublication) {
	s.mu.Lock()
	s.commits.queue = append(s.commits.queue, queuedPublication{fns, p, s.watches.deliveryContext()})
	start := !s.commits.running
	s.commits.running = true
	s.mu.Unlock()
	if !start {
		return
	}
	s.watches.spawn(func() {
		for {
			s.mu.Lock()
			if len(s.commits.queue) == 0 {
				s.commits.running = false
				s.mu.Unlock()
				return
			}
			next := s.commits.queue[0]
			s.commits.queue = s.commits.queue[1:]
			s.mu.Unlock()
			// A host listener's panic is reported and the others still run (session.ts subscribeCommits).
			for _, fn := range next.fns {
				session.Contained(func() { fn(next.ctx, next.publication) }, s.reportListener)
			}
		}
	})
}

type queuedPublication struct {
	fns         []func(context.Context, durable.CommitPublication)
	publication durable.CommitPublication
	// ctx carries the values of the commit that produced the publication (watchHub.deliveryContext).
	ctx context.Context
}
