package plugins

// Ports packages/chord/src/node/plugins/watcher.ts

// Ports `identityOf` of packages/chord/src/node/plugins/watcher.ts: the identity of a directory is its inode and its birth time
// (an inode is reused at once by a directory made again; its birth time is new), never following a link. An event for an entry
// on the way to base code renews the watches under it only when its identity is not the one it had when it was watched: a
// directory moved away and back is the one watched, and a change of its attributes is no replacement.

// dirIdentity is the inode and the birth time of an entry; the zero value is none (Pi's `undefined`: gone, or no access).
type dirIdentity struct {
	present bool
	ino     uint64
	// birth is the birth time in nanoseconds since the Unix epoch: 0 where the file system has none (Node's `birthtimeNs` is
	// 0 there too).
	birth int64
}

// identityOf is the identity of the entry at path.
func identityOf(path string) dirIdentity {
	ino, birth, ok := statIdentity(path)
	return dirIdentity{present: ok, ino: ino, birth: birth}
}

// replaced reports whether the directory at path is not the one watched there: created, removed, or another one now.
func (w *pluginWatcher) replaced(path string) bool {
	return identityOf(path) != w.identities[path]
}
