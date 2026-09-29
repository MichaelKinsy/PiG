### Fixed

- Make the partial message that an RPC `message_start` carries over Codex SSE match Pi. The Codex stream now runs as one ordered turn transcribed from Pi's `parseSSE`, `mapCodexEvents` and `processStream` awaits instead of a free goroutine, so the state is deterministic and equal to Pi's for buffered and pending responses.
- Parse Codex SSE records as Pi does: split only on a blank `\n\n` line, skip non-object records, report `null` records as a TypeError, and use `JSON.parse` wording in `Invalid Codex SSE JSON` errors.
