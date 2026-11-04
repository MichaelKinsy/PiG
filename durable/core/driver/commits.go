// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package driver

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/core/abi"
	"github.com/MichaelKinsy/PiG/durable/session"
)

// Commit publications (PROTOCOL.md, `commits`): while at least one observer is attached, the core attaches the complete
// publication {seq, changes} to each commit's `published` notice. A table change is {type, value}; a document change is
// {type: "document", kind: "change", record, conversationId?, version, value, ops} or, for a retirement, {type: "document",
// kind: "retire", record, conversationId?}; a copied document is {type: "document.copy", record,
// conversationId, source: {id, at}}.

// commitListeners are the ObserveCommits observers of a Session, in attachment order.
type commitListeners struct {
	next int
	ids  []int
	subs map[int]*commitSub

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
		// Observed is the commit's own changes when the storage transaction also stored held changes (session.ts #publish `observed`).
		Observed []json.RawMessage `json:"observed"`
		// Memory marks a deferred commit held in memory (session/held.go): it reaches the commit listeners once it is stored, and
		// only gives the values of the documents it changes.
		Memory bool `json:"memory"`
	}
	if json.Unmarshal(n.Payload, &p) != nil || p.WatchID != nil || p.Changes == nil {
		return
	}
	// The commit's own changes first: a document the storage transaction also stored held changes of has its value (current) after them.
	for _, raw := range p.Observed {
		if _, err := s.decodeCommitChange(raw); err != nil {
			s.reportListener(err)
			return
		}
	}
	publication := durable.CommitPublication{Seq: p.Seq}
	for _, raw := range p.Changes {
		change, err := s.decodeCommitChange(raw)
		if err != nil {
			s.reportListener(err)
			return
		}
		publication.Changes = append(publication.Changes, change)
	}
	if p.Memory {
		return
	}
	s.mu.Lock()
	subs := make([]*commitSub, 0, len(s.commits.ids))
	for _, id := range s.commits.ids {
		subs = append(subs, s.commits.subs[id])
	}
	s.mu.Unlock()
	if len(subs) == 0 {
		return
	}
	s.deliverCommits(subs, publication)
}

// decodeCommitChange decodes one change of a publication. The core leaves out the value of a document change when the host holds
// the document's previous value (a notice carried it): the operations apply to it, or `current` says it is the value held (host/cf
// facade harness.ts restoreValue). Every document change, also of a commit held in memory, keeps the value held.
func (s *coreSession) decodeCommitChange(raw json.RawMessage) (durable.CommitChange, error) {
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
			Kind           string                  `json:"kind"`
			Record         durable.DocumentRecord  `json:"record"`
			ConversationID *durable.ConversationId `json:"conversationId"`
			Version        int                     `json:"version"`
			Value          json.RawMessage         `json:"value"`
			Current        bool                    `json:"current"`
			// Ops decode through chord's JSON, so an object an operation carries keeps its key order and the applier can traverse it.
			Ops delta.Ops `json:"ops"`
		}
		if err := json.Unmarshal(raw, &c); err != nil {
			return nil, err
		}
		switch c.Kind {
		case "change":
			value, err := s.restoreValue(c.Record.Id, c.Value, c.Current, c.Ops)
			if err != nil {
				return nil, err
			}
			return durable.DocumentChange{Record: c.Record, ConversationId: c.ConversationID, Version: c.Version, Value: value, Ops: c.Ops}, nil
		case "retire":
			s.mu.Lock()
			delete(s.documentValues, c.Record.Id)
			s.mu.Unlock()
			return durable.DocumentRetireChange{Record: c.Record, ConversationId: c.ConversationID}, nil
		}
		return nil, fmt.Errorf("the core published a document change of unknown kind %q", c.Kind)
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

// restoreValue is the value of a document after a change: the one the change carries, the one held when it is the current one, or the
// one held with the change's operations applied.
func (s *coreSession) restoreValue(id durable.DocumentId, carried json.RawMessage, current bool, ops delta.Ops) (durable.JsonObject, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(carried) > 0 && !current {
		value, err := delta.DecodeJson(carried)
		if err != nil {
			return nil, err
		}
		object, err := jsonObject(value)
		if err != nil {
			return nil, err
		}
		if s.documentValues == nil {
			s.documentValues = map[durable.DocumentId]durable.JsonObject{}
		}
		s.documentValues[id] = object
		return object, nil
	}
	before, held := s.documentValues[id]
	if !held {
		return nil, fmt.Errorf("the core left out the value of document %d, which the host does not hold", id)
	}
	if current {
		return before, nil
	}
	next, err := delta.ApplyImmutable(before, ops)
	if err != nil {
		return nil, err
	}
	object, err := jsonObject(next)
	if err != nil {
		return nil, err
	}
	s.documentValues[id] = object
	return object, nil
}

// commitSub is a host's CommitObserver as session.ts guardObserver does: a panic of its Advance is reported,
// detaches it and ends it listener_error, and nothing reaches it after its detach or end.
type commitSub struct {
	id       int
	observer durable.CommitObserver
	over     bool // detached or ended; guarded by coreSession.mu
}

// commitObservation is the CommitObservation of a commitSub.
type commitObservation struct {
	s   *coreSession
	sub *commitSub
}

func (o commitObservation) Detach() { o.s.detachCommits(o.sub) }

// ObserveCommits observes every successful Session commit (session.ts observeCommits). The first observer asks the core to
// attach the complete publication to its commit notices; the last one to leave turns that off. A closed Session rejects as
// assertUsable does.
func (s *coreSession) ObserveCommits(observer durable.CommitObserver) durable.CommitObservation {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		panic(session.ErrSessionClosed)
	}
	s.commits.next++
	sub := &commitSub{id: s.commits.next, observer: observer}
	if s.commits.subs == nil {
		s.commits.subs = map[int]*commitSub{}
	}
	s.commits.subs[sub.id] = sub
	s.commits.ids = append(s.commits.ids, sub.id)
	first := len(s.commits.ids) == 1
	s.mu.Unlock()
	if first {
		s.setCommitChanges(true)
	}
	return commitObservation{s: s, sub: sub}
}

// releaseCommits marks sub over and removes it, reporting whether this call did; the last one to leave turns the attachment off.
func (s *coreSession) releaseCommits(sub *commitSub) bool {
	s.mu.Lock()
	if sub.over {
		s.mu.Unlock()
		return false
	}
	sub.over = true
	delete(s.commits.subs, sub.id)
	for i, v := range s.commits.ids {
		if v == sub.id {
			s.commits.ids = append(s.commits.ids[:i], s.commits.ids[i+1:]...)
			break
		}
	}
	last := len(s.commits.ids) == 0 && !s.closed
	s.mu.Unlock()
	if last {
		s.setCommitChanges(false)
	}
	return true
}

func (s *coreSession) detachCommits(sub *commitSub) { s.releaseCommits(sub) }

// endCommits ends sub once with end, unless it was detached or ended.
func (s *coreSession) endCommits(sub *commitSub, end durable.WatchEnd) {
	if !s.releaseCommits(sub) {
		return
	}
	session.Contained(func() { sub.observer.End(end) }, s.reportListener)
}

// advanceCommits gives a publication to sub: a panic is reported, detaches sub and ends it listener_error.
func (s *coreSession) advanceCommits(sub *commitSub, ctx context.Context, p durable.CommitPublication) {
	s.mu.Lock()
	over := sub.over
	s.mu.Unlock()
	if over {
		return
	}
	var failure error
	session.Contained(func() { sub.observer.Advance(ctx, p) }, func(err error) { failure = err })
	if failure != nil {
		s.reportListener(failure)
		s.endCommits(sub, durable.WatchEnd{Kind: durable.WatchListenerError, Error: failure})
	}
}

// endAllCommits ends every open observer with the Session's close: failed with session.ErrSessionClosedEnd, or with the failure that
// failed the Session (session.CloseEnds). The delivery goroutine has returned, so each saw its last publication.
func (s *coreSession) endAllCommits() {
	_, end := session.CloseEnds(s.failure())
	s.mu.Lock()
	subs := make([]*commitSub, 0, len(s.commits.ids))
	for _, id := range s.commits.ids {
		subs = append(subs, s.commits.subs[id])
	}
	s.mu.Unlock()
	for _, sub := range subs {
		s.endCommits(sub, end)
	}
}

// setCommitChanges asks the core to attach (or stop attaching) the complete publication; the observation is in effect when
// ObserveCommits returns, as Pi's is.
func (s *coreSession) setCommitChanges(on bool) {
	_, _ = s.client.API(context.Background(), "commits", map[string]any{"on": on})
}

// deliverCommits hands a publication to the observers on one delivery goroutine per Session, so publications reach every
// observer in commit order and observer code never runs on the owner goroutine.
func (s *coreSession) deliverCommits(subs []*commitSub, p durable.CommitPublication) {
	s.mu.Lock()
	s.commits.queue = append(s.commits.queue, queuedPublication{subs, p, s.watches.deliveryContext()})
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
			// A host observer's panic is reported and the others still run (session.ts observeCommits).
			for _, sub := range next.subs {
				s.advanceCommits(sub, next.ctx, next.publication)
			}
		}
	})
}

type queuedPublication struct {
	subs        []*commitSub
	publication durable.CommitPublication
	// ctx carries the values of the commit that produced the publication (watchHub.deliveryContext).
	ctx context.Context
}
