### Fixed

- Text of a pull request that the pre-flight prints (a path, a ledger reason or cite, a commit subject) could start a workflow command on the runner, in the `::command::` form and in the legacy `##[command]` form. Every line the tool prints is now inert. <!-- fix: 363faed088 -->
- The pre-flight `test-expectation` check admits a reviewed ledger row only when the named commit changed that file, as the line lint requires, so the two gates judge alike. <!-- fix: 546133acb5 -->
- The pre-flight `test-expectation` check skips the tooling directories of `test/testdata-provenance.toml` on main, as the line lint does, so a ledger row for such a file is no longer required by one gate and stale for the other. <!-- fix: a058b99ea8 -->
- A shortened notice of the pre-flight is no longer stored as a complete verdict: the record keeps the whole text and only the printed line is cut. <!-- fix: 2844ff315c -->
- When a report was too long for its comment, the next unchanged run reused the shortened record and printed no warnings or notices; it now recomputes. <!-- fix: 730c233072 -->
