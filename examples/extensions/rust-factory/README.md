# Rust factory example

This extension exposes the conventional `new_extension()` factory in
`src/lib.rs`. It registers the `hello` tool.

Validate it:

```bash
pig install --validate-only --json examples/extensions/rust-factory
```

Pig imports the library factory into a generated Rust runner. A Rust factory can
share a Rust runtime cell. Use `src/main.rs` without a factory library for an
isolated standalone.
