package store

// Ports packages/chord/src/next/store/writes.ts
//
// The store's write vocabulary (`StoreWrite`, slice 6a §7, §8; decision "Writes carry their versions"): one change of
// the store each, as the outbox and archives record it.
//
// Go forced difference: Pi's `DocumentWrite | TableWrite` union is an interface; every document write kind (D3Tables'
// engine.go) and table write kind (table_writes.go) implements StoreWriteKind, returning its `kind` tag
// ("table", "put", "delete", "index", "dropIndex", "alter", "document.create", ...). Each kind serializes in Pi's key
// order (struct fields in Pi order with json tags, or MarshalJSON).

// StoreWrite is one change of the store, tagged by its kind.
type StoreWrite interface {
	// StoreWriteKind is the write's `kind` tag.
	StoreWriteKind() string
}
