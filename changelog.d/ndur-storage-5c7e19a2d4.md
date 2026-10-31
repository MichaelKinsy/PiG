### Changed

- `Storage.Commit` stores a call's batches in order, each with its own seq, and stops at the first batch Durable's own checks refuse, keeping the batches before it. Memory, JSONL and SQLite storage implement it; a JSONL commit appends its batches' markers in one write, and JSONL reads wait while a commit is being appended.

### Fixed

- A JSONL commit whose file path fails to resolve after its batch applied in memory now poisons the storage, as every failure after that point does, instead of leaving memory ahead of the files.
