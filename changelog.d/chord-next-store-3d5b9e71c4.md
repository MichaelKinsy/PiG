### Added

- Go API `chord/next/store`: the store of Chord v2 (`@earendil-works/chord/next`, experimental) as Pi `e7a0df27` has it, on `chord/sqlite`. Tables with flat and `json(...)` columns, queries and bulk writes, eager table migrations, the documents engine, one commit queue serializing the commit step, the outbox, export and import of archives with their JSONL encoding, claims, and batch replay. The SQL it runs and the bytes it stores (catalog, outbox, archives) are Pi's. <!-- feat: chord-next-store -->
