### Fixed

- Fixed CI on `main` after the 0.5.1 ledger trim: the reviewed-pin ledgers (`upstream-v0.84.0.json`, `cli-v0.84.0.json`, `behavior-inputs-v0.84.0.json`, `behavior-input-mapping-v0.84.0.json`) are back, since `interface-delta` reads them as the base of the current delta; the stale testprune ledger entries and bug ledger entries orphaned by the 0.5.0 squash merge are gone. <!-- fix: 0e3411c597 -->
