// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package session

// Ports packages/durable/src/harness/scheduler.ts indexSizes.

// IndexSizes are the sizes of the scheduler's ownership indexes, for tests that bound its memory (scheduler.ts indexSizes); the host replicates them into its synchronous getter.
type IndexSizes struct{ Live, Settled, Edges, Below, Roots, Unloaded, DropQueue int }

// IndexSizes returns the sizes of the scheduler's ownership indexes.
func (s *Scheduler) IndexSizes() IndexSizes {
	return IndexSizes{Live: len(s.live.m), Settled: len(s.settled), Edges: len(s.edges), Below: s.belowEntries, Roots: s.roots.Len(), Unloaded: s.unloaded.Len(), DropQueue: s.dropQueue.Len()}
}
