// SPDX-License-Identifier: MIT

// Package sqlhost is the native Go host of the Durable core (docs/plan/durable-core/ABI.md section 11, ADR-0001 D17).
//
// One Host owns one core session and one SQLite connection. A single owner goroutine calls the core's Step, applies each
// commit of a step in its own transaction, answers read rounds before any other event, delivers notices after the commits,
// and only then starts effects. Effects run in owned goroutines under a context.Context; their completions return to the
// owner through an unbounded mailbox, so an effect never blocks the owner and the owner never blocks an effect.
//
// The host holds the ABI host loop invariants of section 3:
//
//  1. The core is never called re-entrantly: every event is processed to completion, including its read rounds, before
//     the next one.
//  2. Each commit is one transaction. A failed commit, or a durable_metadata update that does not change exactly one row
//     (the single-writer guard), is fatal to the handle.
//  3. A step with reads is answered by one rows event before any other event is delivered.
//  4. Notices are delivered after the step's commits are applied.
//  5. Effects start only after the step's commits are applied.
//  6. Effect IDs are unique for the life of the handle. Discarding the handle cancels its effects and drops their late
//     completions.
package sqlhost
