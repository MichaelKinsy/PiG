### Added

- The `schema` package validates JSON values against JSON Schema drafts 2020-12 and 7 and builds schemas, as Pi's new `pi-schema` package does. `schema.Compile` returns a validator with `Check` and `Errors` (JSON Pointer paths); `schema/formats` supplies the standard format checks. Nothing in PiG uses it yet.
