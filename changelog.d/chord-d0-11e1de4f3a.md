### Changed

- The `chord/delta` tracker keeps its most recent open change in a plain field and registers a weak pointer only for a second change open at the same time, as Pi's tracker does without a `WeakRef`. A draft dropped without `Prepare()` or `Abort()` now stays referenced by its tracker until the tracker's next `BeginChange()` or `Adopt()`, not reclaimed at the next collection.
