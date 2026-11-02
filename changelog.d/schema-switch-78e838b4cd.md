### Changed

- 0.7.0 no longer runs `Type.Refine()` callbacks on a tool's arguments (changed from 0.6.x, matching Pi 1.5.0, which validates tool arguments as plain JSON Schema): arguments a refinement would reject are accepted, so check such constraints in the tool's `execute()` (its handler). Go, Rust, Python and Lisp keep `Refine` as a schema constructor whose callback no API runs (D226). The Go, Rust, Python, Lisp and Node extension runtimes no longer declare a tool's refinements or answer the host's `tool_refine_arguments` request, which no longer exists. `Type.Refine` and `Type.IsRefine` stay as schema constructors in the Go, Rust and Python SDKs (Lisp `type-refine`), and the schema's JSON is unchanged.

### Removed

- The Go, Rust and Python SDKs no longer fail a tool's registration for a `Type.Refine` refinement under a schema keyword the host did not traverse, and a child session's custom tools may carry refinements. The `validation_parameters` member of a tool declaration, which carried TypeBox's `~kind` form of the schema for the old validator, is gone with the validator that read it; the Python `refinement_sites` and Rust `RefinementSite` and `TSchema::refinement_sites`, `refinement_count` and `to_validation_value` are removed.
