### Changed

- Chord v2 (`chord/next`) checks arguments, params, documents, json columns and archive parts with the `schema` package instead of the TypeBox-era validator, as Pi does since the switch to pi-schema. A failed check explains itself with pi-schema's messages (`args/title: expected string, got integer`, `params: expected object, got string`, `args/blob: expected at most 2 bytes`), a missing field and an unknown field are reported at their own path, and `~` and `/` in keys are escaped in `details.path` (`/tags/a~1b`). `format` is an annotation: it is part of a schema's normal form and fingerprint, but no format is checked. A `bytes()` leaf is checked natively, also inside a `Map`. Fingerprints and normal forms of contracts, documents, tables and jobs, and the data stored before the switch, are unchanged (proved against Pi's recorded fingerprints, SQLite store and archive).
- `contracts.Obj` writes `properties` before `required`, as pi-schema's `obj` does, and `contracts.Opt(...)` outside an object is refused by schema snapshots (`<path>: expected a schema`). Params refuse an optional field before reading its schema.

### Removed

- `contracts.CheckBytes` and the `data.Validator.Type` accessor: a bytes leaf is plain data checked by the validator.
