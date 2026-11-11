package plugins

import "errors"

// pig additive (D223): the Node host's store; Stock PiG gives none unless the embedding program sets it.
// StoreOptions gives a Node host the store its persisted documents and tables live in: Pi's `createHost({ store })`, where
// the app opens `openStore(sqliteBackend(await openNodeSqliteDatabase(path)))` over its own SQLite file and closes it after
// the host. The subprocess opens that store itself, with Pi's own `next/store` over `node:sqlite` (Pi's
// `sqlite/node.ts`), and closes the host and then the store when the NodeHost is closed.
//
// A store belongs to one open host (Pi: "one open host per store"), so this store is the Node host's own, a different one
// from the store of the Go host the host's link is attached to: documents and tables a plugin persists are in this file,
// read by Pi's code in the subprocess, and the Go host's store is not shared over the link. A host loaded without a store
// keeps Pi's error: a type that declares a persisted document or a table fails to activate with `persist needs a store`.
//
// A failure of the store after it opened (`storage_failed` in Pi) closes the Node host's host and reaches
// NodeOptions.OnError as a NodeHostError of that kind, once.
type StoreOptions struct {
	// Path is the SQLite file, as `openNodeSqliteDatabase`'s `path`: its directory is created, the file is opened in WAL
	// mode, and ":memory:" is a database no later host sees. A relative path is the subprocess's, so the app gives an
	// absolute one.
	Path string
}

// storeWire is StoreOptions as the `load` request carries it.
type storeWire struct {
	Path string `json:"path"`
}

// storeToWire is the wire form of options, nil for no store; a store without a path is an error of the load.
func storeToWire(options *StoreOptions) (*storeWire, error) {
	if options == nil {
		return nil, nil
	}
	if options.Path == "" {
		return nil, errors.New("plugins.load: Store.Path is required")
	}
	return &storeWire{Path: options.Path}, nil
}
