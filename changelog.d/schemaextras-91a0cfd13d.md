### Added

- `schema.Validate` returns the value or its errors without coercing, on every validator; `schema.Recursive(id, build)` builds a schema that contains itself; `schema.Equal(a, b)` is the JSON equality `const`, `enum` and `uniqueItems` use; `schema.Unsafe(schema...)` returns the schema it is given, or `{}`, as Pi's `unsafe()` does. Follows Pi main af79b0c3d (the `bigint` and `undefined` types and `BigInt` are in their own entry).
