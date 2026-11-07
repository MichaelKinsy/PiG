### Fixed

- `Value.Check` and `Value.Errors` of the Go, Rust, Python and Lisp extension SDKs check a schema's `minLength` and `maxLength` in grapheme clusters, `multipleOf` with a tolerance of 1e-10, `uniqueItems` by TypeBox's hash (0 and -0 differ), and the `hostname`, `idn-hostname`, `idn-email` and `regex` formats with TypeBox's own checks, as TypeBox does in Pi. They counted code points, compared exactly and followed a JSON Schema library's format rules. <!-- fix: 4625b4cc3d -->

### Changed

- The `pig` binary is about 500 KB smaller: the host checks `Value.Check` and `Value.Errors` with one walker, as TypeBox does, and no longer links a generic JSON Schema validator.
