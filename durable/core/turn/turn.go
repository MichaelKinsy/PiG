// SPDX-License-Identifier: MIT

// Package turn is the agent turn: the built-in pi.generation and pi.tool tasks (spec §8.3-8.6) as session.TaskImpl
// definitions. Generation phases prepare, request, retry, poll and tools; tool phases call and execute; sequential and
// parallel rounds; the usage ledger; streaming partials committed with pi-durable main's fixed-interval throttle
// (generation.ts streamResponse) and tool output with the size-paced Progress (output.ts); retries, backoff and deferred
// polls as durable timers; recovery of an interrupted turn at any commit (CONTRACT section 3.8); the model request as a
// spliced model_context effect (context plane), and the wire plane behind the wireplane build tag.
//
// Owner: dcore-history (taking dcore-loop). Depends on session (Runtime, Scheduler), history, rec, jv, abi.
package turn
