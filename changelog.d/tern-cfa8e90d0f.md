### Changed

- A process no longer starts 16 extra goroutines the first time it identifies a goroutine: the check that reads goroutine IDs from the runtime proves its offset on the goroutines that already ask for an ID. Output is unchanged.
