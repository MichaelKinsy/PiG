### Added

- Chord: plugins can declare named live queries (a `queries` member with `self.queries`, `self.liveQuery` and `self.count`) over the column sets that a table exposes, and another object's named query can be read through a reference with a query reader. Queries can use `exists` and joins. Port of Pi's Chord slice 14 (Pi 7cc21c24f6b5).
- Chord: a list (`self.list`) loads its rows a page at a time and keeps the pages as one gapless value while rows move, are written in bulk, or the publisher reloads. A commit no longer runs a live query it cannot change, and the counters `chord.query.probes`, `chord.query.skips` and `chord.query.rows` report the probes, the skipped runs and the rows read.
- Chord: a served type whose contract declares an `admission` is served with `admit`, which decides for each caller and hands the factory a typed value. A caller that is refused sees its reference fail with the new `denied` failure kind, a running call is answered `forbidden/revoked`, and a changed decision seals the running attempt and admits a new one. The `denied` kind is byte 3 in the binary codec and letter `d` in the text codec; the refused byte moves from 3 to 4, so a client that decodes failure causes must know `denied` (the Go consumer does).
- Chord: a route takes `admit` too. It is asked once for each principal and address, after `authorize` and before anything is resolved or forwarded. Until its first allow a hold is `loading`, a denial fails the principal's holds `failed { denied }` and releases them at the address's host, and a later allow forwards them again. `createWorkers` passes the option to its route.
- Chord: `DeleteHostData` takes options that limit the delete to the records and tables of one plugin; a plugin that is not loaded is refused as invalid arguments.

### Changed

- Chord: `HostError` `query_failed` names the query that failed in `Query`, where it named the table in `Table`.

### Fixed

- Chord: the reloads of a changed binding are owned tasks of the store's reads index, so a binding that changes while a query is made is reloaded in order and waiting for the host to settle waits for them. <!-- fix: 674e3b98693590395be118d765adac76bfad3ff8 -->
- Chord: a query reader opens its read in the section of the move that created it, as Pi does, so what it reads follows the move in the same section. <!-- fix: 6d4e10cfcf11fad2b41f16c3ac1b82bcbb860b28 -->
