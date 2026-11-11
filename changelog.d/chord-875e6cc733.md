### Changed

- Chord: `workers.WorkersBuilt` is the sealed union Pi's `WorkersBuilt` is, `WorkersBuiltBuilt{Inputs}` or `WorkersBuiltFailed{Error}`, each with its `Kind()`, where it was one struct with an `Err` field. `Workers.Build` returns it, `Workers.Built` returns nil for Pi's `undefined` (it returned a pointer), and a caller switches on the variant. The ledger's interface closure now derives `WorkersBuilt`, `StateDefinition` and `StateDefinition.host` as ported (15117 of 15117 interface IDs closed).
