// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package session

// Ports packages/durable/src/harness/scheduler.ts indexSizes.

// IndexSizes are the sizes of the scheduler's ownership indexes, for tests that bound its memory (scheduler.ts indexSizes); the host replicates them into its synchronous getter.
type IndexSizes struct{ Live, Settled, Edges, Below, Roots, Unloaded, DropQueue, Pins int }

// IndexSizes returns the sizes of the scheduler's ownership indexes.
func (s *Scheduler) IndexSizes() IndexSizes {
	return IndexSizes{Live: len(s.live.m), Settled: len(s.settled), Edges: len(s.edges), Below: s.belowEntries, Roots: s.roots.Len(), Unloaded: s.unloaded.Len(), DropQueue: s.dropQueue.Len(), Pins: len(s.pins)}
}

// IdleState is the kept contexts' idle state, computed on demand (scheduler.ts idleState, a diagnostic for tests): how many are kept and
// how many are idle, how many disagree with a full recompute (an idle period without idle, or idle without one; an expiry order that
// differs from the idle periods, or is not sorted), and how many idle checks covered every scope.
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
