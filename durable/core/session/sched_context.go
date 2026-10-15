package session

import "slices"

// The context range a task runtime read last, per conversation (scheduler.ts #contexts): a later read, by any task of the
// conversation, scans only newer entries. The core derives contexts from its own transcript store, so this keeps only what
// decides which Storage reads Pi makes: the head marker and the tail of the range, and when the conversation first looked
// idle. It is derived, never persisted, dropped at the first idle check after contextRetentionMs of idleness, and at seal.

type keptContext struct {
	head, tail ID // head marker entry (0: none) and newest entry of the range
	idleSince  float64
	hasIdle    bool
}

func (s *Scheduler) retention() float64 {
	if s.env.ContextRetentionMs == nil {
		return 0
	}
	return s.env.ContextRetentionMs()
}

// KeptContext returns the head marker and tail of the range kept for the conversation.
func (s *Scheduler) KeptContext(conversationID ID) (head, tail ID, ok bool) {
	kept := s.contexts[conversationID]
	if kept == nil {
		return 0, 0, false
	}
	return kept.head, kept.tail, true
}

// KeepContext keeps the range a runtime read returned unless the invocation ended, the Harness is closing, or a concurrent
// read already kept a newer range. A read of another, idle conversation starts or continues its retention period.
func (s *Scheduler) KeepContext(conversationID, head, tail ID, invocationEnded bool) {
	if s.closing || invocationEnded {
		return
	}
	kept := s.contexts[conversationID]
	if kept != nil && kept.tail > tail {
		return
	}
	if !s.idle(conversationID) {
		s.contexts[conversationID] = &keptContext{head: head, tail: tail}
		return
	}
	if s.retention() > 0 {
		next := &keptContext{head: head, tail: tail, idleSince: s.env.Now(), hasIdle: true}
		if kept != nil && kept.hasIdle {
			next.idleSince = kept.idleSince
		}
		s.contexts[conversationID] = next
		s.scheduleExpiry()
	}
}

// expireContexts drops each kept context whose conversation has been idle for the retention period (scheduler.ts
// #settleIdle).
func (s *Scheduler) expireContexts() {
	now, retention := s.env.Now(), s.retention()
	ids := make([]ID, 0, len(s.contexts))
	for id := range s.contexts {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for _, id := range ids {
		kept := s.contexts[id]
		switch {
		case kept.hasIdle && now-kept.idleSince >= retention:
			s.dropContext(id)
		case !s.idle(id):
			kept.hasIdle = false
		case retention > 0:
			if !kept.hasIdle {
				kept.idleSince, kept.hasIdle = now, true
			}
		default:
			s.dropContext(id)
		}
	}
	s.scheduleExpiry()
}

// dropContext drops the range kept for the conversation, and tells the core it may release what it derived.
func (s *Scheduler) dropContext(id ID) {
	delete(s.contexts, id)
	if s.env.ReleaseContext != nil {
		s.env.ReleaseContext(id)
	}
}

// scheduleExpiry runs settleIdle when the earliest idle context expires (scheduler.ts #scheduleExpiry).
func (s *Scheduler) scheduleExpiry() {
	retention := s.retention()
	var at float64
	found := false
	for _, kept := range s.contexts {
		if kept.hasIdle && (!found || kept.idleSince+retention < at) {
			at, found = kept.idleSince+retention, true
		}
	}
	if s.expiry.set && found && s.expiry.at == at {
		return
	}
	s.cancelExpiry()
	if !found || s.closing || s.env.Timer == nil {
		return
	}
	s.expiry.set, s.expiry.at = true, at
	s.expiry.cancel = s.env.Timer(at, func() {
		s.expiry.set, s.expiry.cancel = false, nil
		s.settleIdle()
	})
}

func (s *Scheduler) cancelExpiry() {
	if s.expiry.set && s.expiry.cancel != nil {
		s.expiry.cancel()
	}
	s.expiry.set, s.expiry.cancel = false, nil
}

// Ended reports whether the invocation ended.
func (r *Runtime) Ended() bool { return r.inv.ended }

// EndedError is the error of an operation of an ended invocation.
func (r *Runtime) EndedError() error { return endedError(r.inv) }
