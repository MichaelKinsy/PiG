### Added

- The Go, Rust and Python SDKs export `Type.Refine` and `Type.IsRefine` of pi-ai's `Type`. A tool whose parameters carry refinements has its arguments checked in the host in TypeBox's own order, with each predicate and error message run in the extension that defined it, so an extension tool rejects, words and orders its failures as it does under Pi (checked against Pi's own `validateToolArguments` for the same cases in every SDK). A tool without refinements validates exactly as before.
