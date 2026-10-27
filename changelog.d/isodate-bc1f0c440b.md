### Changed

- `ParseDate` and `Context.ParseDate` in the Go SDK, `parse_date` and `Context::parse_date` in Rust, and `parse_date` and `ctx.parse_date` in Python answer text in the form `Date.prototype.toISOString` writes for a four-digit year (`2026-10-24T18:30:15.042Z`, how Pi writes the timestamp of every session entry) in the SDK instead of asking the host, so `SessionEntryToContextMessages` and the branch-summary helpers make no host call for an entry from a session file. Every other text still goes to the host's parser, and the answers are the same.
