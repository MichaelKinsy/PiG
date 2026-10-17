# Representation proposals awaiting the lead (lg-review-c3, lg-c4-durable-api)

`test/parity/interface-closure/autobind/representations.json` is a reviewed input (ledger-autobind.md lists it with reviewed-types.json; lg-review-c1 and lg-review-c2 apply the same policy). Lane lg-c4-durable-api added the four entries below without a lead approval. Review lg-review-c3 took them out of the file until the lead approves them. To approve one, add the line back to `representations.json` unchanged.

## `"durable:DatabaseSync": "node.Connection"` (added in f9c0c5f84)

Pi's node SQLite storage types its database as node:sqlite `DatabaseSync` (a Node built-in). Go's durable/storage/sqlite/node wraps the database handle as node.Connection. Reviewer check: a Node built-in class has no Go counterpart to port, so the question is only whether node.Connection carries every member Pi's adapter calls.

## `"durable:Draft": "delta.Object"` (added in 940a47a63)

Pi's Tx.doc results are the mapped Draft<T> of chord delta/draft.ts, which the inventory prints expanded. Go's mutable document view is delta.Object. Reviewer check: Draft<T> is a type-level deep-mutable view and delta.Object is its runtime form; the lead decides whether one untyped Go type may stand for every Draft<T>.

## `"durable:HarnessSettings": "func() *harness.HarnessSettings"` (added in 1d830b436)

Pi's HarnessOptions.settings is a HarnessSettings object (harness/types.ts:414, :457) whose fields may be getters, so a host can change settings live (spec-usage.test.ts:259), and harness.ts:182 reads it through resolveSettings at every use. Go's HarnessOptions.Settings is a getter function returning *HarnessSettings. Reviewer check: this maps a type to a function type, not a type agreement; the HarnessSettings type row itself maps to the struct. The lead decides whether the getter is an acceptable representation of a live settings object.

## `"durable:TextDecoder": "env.RangeDecoder"` (added in f9c0c5f84)

Pi's env decodes output with the Web `TextDecoder` (streaming UTF-8). Go's env.RangeDecoder is the streaming decoder used in its place (durable/env/decode.go), and TestRangeDecoderKeepsAByteOrderMark covers it. Reviewer check: TextDecoder is a platform built-in; the question is only whether RangeDecoder carries the decode behaviour Pi's call sites use (stream mode, BOM handling).

## Status after the 1.1.0 ledger-autobind-int merge

- `durable:TextDecoder` and the `HarnessOptions` settings getter are approved in `ledger-autobind-int` (rangeDecoder and HarnessOptions rows closed there).
- `durable:DatabaseSync` is no longer needed: `NewNodeSqliteDatabase` now takes `*DatabaseSync`, Pi's `new NodeSqliteDatabase(database: DatabaseSync)` shape (durable/storage/sqlite/node/node.go). The wrapper seam the tests use is the unexported `connection`/`newNodeSqliteDatabase`. `TestNewNodeSqliteDatabaseAdaptsADatabaseSync` is mutation-checked.
- One row remains: `Tx.doc::call:0` (parameter `token: SessionDocToken<T>` against `AnyDocToken`). Calls 1-5 are closed by reviewed-types decisions that map Pi's `doc<T>()` to `Tx.Doc` with `Draft<T>` as `*delta.Object`. The same decision covers call:0: the Go method takes the erased token every typed token implements (`TxDoc[T]` is the typed entry). Requested: a reviewed-types decision for call:0 with the call:1-5 wording.

## Update: Tx.doc::call:0 decided (last durable row)

`reviewed-types.json` now carries `pkg:durable/.#Tx::property:doc::call:0` with the wording of the integrator's call:1-5 decisions for the same method, plus the token parameter (`SessionDocToken<T>` is the alias `DocToken[T]`, which implements the erased `AnyDocToken`; `TxDoc[T]` is the typed entry). It closed `Tx.doc` and `Tx` and left zero open durable rows. Review: revert it if the lead does not accept an entry that repeats an integrator-approved decision for the same method.
