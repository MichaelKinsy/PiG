### Added

- `schema.BigInt(options...)` builds `{"type":"bigint"}`; a bigint is a `*big.Int` and `schema.Undefined` is `undefined`, for the `bigint` and `undefined` types and the number keywords of either kind. Follows Pi main af79b0c3d.

### Fixed

- Tool arguments for a `bigint` or `undefined` parameter are converted, not rejected: a JSON `"42"` or `42` for a bigint parameter and `null` for an undefined one validate, as TypeBox converted them. The arguments' JSON writes a bigint as `"42n"`, as Pi's validation probes do, and leaves out an undefined member. Follows Pi main af79b0c3d. <!-- fix: 967def653d -->
