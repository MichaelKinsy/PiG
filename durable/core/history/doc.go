// SPDX-License-Identifier: MIT

// Package history is the transcript half of the Durable core: entries, forks, head markers, context edits,
// compaction ranges and reset, and the context derivation that turns them into the model's messages.
//
// It ports pi-durable's src/harness/context.ts, the range selection of src/harness/compaction.ts, the fork and entry
// writes of src/session/transaction.ts and src/session/forks.ts, and the head-write rule of src/harness/inbox.ts
// (spec sections 2.1, 3.7 and 8.7). It follows ADR-0001 D4 and D5: a conversation's entry records stay as the
// stored UTF-8 bytes in one append-only arena beside a fixed-width index, and a derived context is a list of
// references into that arena, so a warm turn never decodes a record.
//
// The package is part of the one core source that TinyGo, Go wasip1 and native Go all build: it has no reflection,
// no encoding/json, no goroutines, no I/O, no clock and no package-level mutable state. A Store belongs to one
// Session handle and is not safe for concurrent use.
//
// JSON that the package writes is byte-identical to JSON.stringify of the value JSON.parse reads (insertion order
// except integer keys, ECMAScript number formatting, well-formed string escapes). Stored records are read by a
// structural scanner, so unknown fields, unknown entry kinds and any key order are tolerated and carried through.
package history
