package session

// BelowRecount counts the children listed in below the slow way, to check the count IndexSizes keeps.
func (s *Scheduler) BelowRecount() int {
	n := 0
	for _, children := range s.below {
		n += children.Len()
	}
	return n
}

// IdleState is the kept contexts' idle state, computed on demand: how many are kept and how many are idle, how many disagree with a
// full recompute (an idle period without idle, or idle without one; an expiry order that differs from the idle periods, or is not
// sorted), and how many idle checks covered every scope.
func (s *Scheduler) IdleState() (contexts, idleContexts, mismatches, fullChecks int) {
	for id, kept := range s.contexts {
		contexts++
		if kept.hasIdle {
			idleContexts++
		}
		if kept.hasIdle != s.idle(id) {
			mismatches++
		}
		if s.idleOrder.Has(id) != kept.hasIdle {
			mismatches++
		}
	}
	previous := -1e300
	for _, id := range s.idleOrder.Values() {
		kept := s.contexts[id]
		if kept == nil || !kept.hasIdle || kept.idleSince < previous {
			mismatches++
			continue
		}
		previous = kept.idleSince
	}
	return contexts, idleContexts, mismatches, s.fullIdleChecks
}
