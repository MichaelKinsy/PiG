### Added

- Plugin views: every SDK builds the `grid` node. Go `kit.Grid`, Rust `kit::Grid`, Python `kit.Grid` and Lisp `kit-grid`, each with its cell and chip records, and in Node the plain object, typed by `PiGViewGrid`, `PiGViewGridCell` and `PiGViewGridChip`. A member you do not set is not sent, and the host stays the one validator: a grid one past a bound, with an unknown mark or with a member the record lacks fails with code `invalid` in every SDK.
