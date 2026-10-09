### Changed

- `chord/delta` is the only implementation of Pi's `@earendil-works/chord/delta` operations, as Pi exports them from one subpath. It now also holds `Apply`, `DiffRevisions`, `AssertSafePath`, `ReservedSegments`, `TypeError` and `DiffCandidate`, which were internal. An invalid operation is now a `*delta.TypeError` that carries Pi's message without a prefix, and `errors.Is(err, delta.ErrInvalidOp)` still holds for it. `Overlap` takes Pi's optional probe and candidate limits.

### Added

- `delta.ApplyImmutableBatchesSeq` takes an `iter.Seq[[]Op]`, as Pi's `applyImmutableBatches` takes an `Iterable`.
- `delta.AssertValidOpValue` accepts any value, as Pi's `assertValidOp` takes `unknown`.
- `delta.Verb` and `delta.PathOf` read an operation's verb and path.
- `delta.Ops` is a batch of operations whose `UnmarshalJSON` keeps the key order of the objects the operations carry. Use it for a decoded field; a `[]delta.Op` decoded by `encoding/json` holds Go maps.

### Deprecated

- `delta.ApplyImmutableBatches(target, [][]Op)`: use `delta.ApplyImmutableBatchesSeq`. It still works.
- `delta.AssertValidOp(Op)`: use `delta.AssertValidOpValue`. It still works.

### Fixed

- `delta.AssertValidOp` checks path safety as Pi's `assertValidOp` does: it rejects a negative, fractional, boolean, null or nested path segment, and `__proto__`, `constructor` or `prototype`. Before, it accepted them, and only `ApplyImmutable` rejected them while applying. Its messages are Pi's (`path is empty`, `t shape`).
- `delta.ApplyImmutable` sets a numeric path segment on an object as that key, as Pi does. Before, it reported `unresolvable path`.
- `PathError` and `UnsafePathError` messages print paths as `JSON.stringify` does: `<`, `>`, `&`, U+2028 and U+2029 stay unescaped and `-0` prints as `0`.
- An `"m"` operation whose permutation a Go caller builds as `[]int` validates and applies again, as it did in 0.4.1. Before this fix, `AssertValidOp` reported `m permutation is not an array`.
- `durable.DocumentContent` decoded by `encoding/json` keeps the key order of the objects its operations carry, as Pi's storage `JSON.parse` does.
