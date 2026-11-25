### Fixed

- `durable/core/history` now rejects records larger than `math.MaxInt32` bytes before storing `uint32(len(rec))` or allocating an arena chunk, prevents uint32 offset wrap on chunk rollover, and guards `Chain.reload`/`Chain.read` against oversized chunk math (code scanning #253). <!-- fix: f2b580916a --> <!-- cause: Chain.appendRecord cast len(rec) to uint32 without a maximum-size check -->
