### Added

- The `schema` package follows Pi's pi-schema changes for tool-argument validation: `UnresolvedRefs: schema.UnresolvedRefsReject` compiles a `$ref` that names nothing as `false` (the error is `unresolved reference <ref>`), the `url` and `json-pointer-uri-fragment` formats are checked, a string's length counts an unpaired high surrogate as one code point, and error messages cap an enum listing at 20 values, word `contains` bounds in plain language and blame a failed union on the branch that declares most of the object's properties.

### Changed

- The `schema` builders take options typed by builder: `schema.Str(schema.MinItems(1))` or `schema.Num(schema.Open())` no longer compile, and `schema.Obj` writes `properties` and `required` in JavaScript's own-key order (array-index names first), as Pi's `obj()` does.
