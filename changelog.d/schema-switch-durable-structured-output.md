### Changed

- A tool's `structuredOutput` is checked against its `structuredOutputSchema` with the schema package (the standard formats; a `$ref` that names nothing rejects the value) instead of the TypeBox checker, so an `invalid_structured_output` report words the failure as the schema package does, for example `count: expected number, got string`, and a property name is a name, not a pattern.
