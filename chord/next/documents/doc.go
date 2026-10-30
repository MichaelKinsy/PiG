// Package documents ports packages/chord/src/next/documents: definitions, the handles an object's attempts get, the
// object's documents (in memory, or persisted in the host's store), keyed watches and `at(key)`, retirement, and
// transactions over documents, tables and job runs.
//
// # Package layout
//
// Pi splits the directory into `index.ts` (for apps) and `internal.ts` (for Chord's layers). Go has one namespace per
// package, so every name is exported from this package; the app-facing set is DefineState, StateDefinition,
// SingletonHandle, KeyedHandle, Tx, JsonObject and CommitPersistence.
//
// # Execution model
//
// Pi runs documents on the host's event loop: a slot, a transaction and the batch are plain objects, and `await` is
// the only place another task interleaves. The Go form keeps that contract through DocumentsOwner.Do, the host's
// serialization: every section that reads or changes the documents' state runs inside Do (a section never blocks and
// is never nested into another that blocks), and every wait (the line, a store read, the store's commit step, a cold
// load) happens outside it, where Pi has an `await`. The host's Do is inline when the caller is already inside a
// section, so a section may call another. Reads of a document's value (Singleton.Value, KeySource.Value) are made
// from a section, as the state package's are.
//
// # Promises
//
// A Pi `Promise<T>` call is a blocking call returning (T, error): the call takes its place on the line at the call
// (the order of the calls that reach Acquire), then waits. Cancelling a context ends only the caller's wait where Pi's
// `awaitWithContext` does. `AsyncIterable` is an iterator with Next(ctx) and Close.
//
// # Forced differences
//
//   - TypeScript type-level machinery (StateDefinition's `T`, `K` and `P` parameters, HandleOf, Draft<T>, the overloads
//     of `draft`/`retire`, the table type parameters of Tx) is designed out: a definition's documents are
//     *JsonObject, a draft is *delta.Object, and the Go form of an overload is an optional key argument.
//   - A callback that throws returns an error; Tx callbacks are func(*Transaction) (any, error).
//   - The types `tx.start` takes from `jobs/` (JobHandle, Run, StartOptions) are interfaces owned by this package, which
//     the jobs satisfy structurally, so documents imports nothing of them.
package documents
