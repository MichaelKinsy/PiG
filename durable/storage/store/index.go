// Package store is Durable's Storage on Chord's store: StoreStorage keeps Durable's records as declared tables of the
// store, its documents in the store's documents engine, and commits as one series of the store's commit queue.
package store

// Ports packages/durable/src/storage/store/index.ts
//
// index.ts re-exports StoreStorage; Go exports it from the package itself (storage.go). The other files port
// commit.ts, documents.ts, queries.ts, storage.ts and tables.ts.
//
// Go forced differences, for every file of the package:
//   - A Promise-returning call blocks and returns (value, error); Pi's microtask and macrotask ordering of its
//     settles becomes the store's own queue and receipts, which already run in call order.
//   - A thrown Error is a returned error; Pi's tagged results (Read, DocumentRead) stay tagged, so a request error is
//     returned after the guarded read, never inside it.
//   - A `json(...)` record cell is a JSON tree at staging (the store's columns check and serialize trees, not Go
//     structs): a record is marshalled as Durable's other storages marshal it, parsed into a tree, and the store
//     marshals the tree again. Reads parse the cell text straight into the record's Go type.
