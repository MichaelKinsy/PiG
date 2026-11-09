package node

import (
	"bytes"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	chordsqlite "github.com/MichaelKinsy/PiG/chord/sqlite"
)

// pig additive (D104): the commit token of a database has no Pi counterpart.
//
// A WAL-mode SQLite database keeps the state of its newest commit in the header of its wal-index, the first 96 bytes of
// the "-shm" file: two copies of a 48-byte WalIndexHdr (wal.c). Every commit writes both copies, the second first, and
// increments iChange; a checkpoint that restarts the log writes both with new salts. A read transaction begins by
// reading that header (walTryBeginRead) and sees the state it names, so two reads that find the same header see the same
// database. The header is shared memory: a commit of another connection, or another process, is in it when its COMMIT
// returns. Reading it takes one pread of the file, no transaction, no lock and no statement.
//
// A reader that finds the two copies equal, with the checksum of the header that SQLite stores in it (walIndexTryHdr),
// holds a state some commit left: a writer in the middle of a store leaves the copies different, or a checksum that does
// not fit, and the read is then told as unavailable.

// walIndexHeaderBytes is the size of one WalIndexHdr.
const walIndexHeaderBytes = 48

// walIndexVersion is WALINDEX_MAX_VERSION, the iVersion of every header SQLite 3.7 and later writes.
const walIndexVersion = 3007000

// headerReads is how many times a read of the header that finds it half written is repeated: a store takes a few
// nanoseconds.
const headerReads = 4

// commitTokenFile reads the wal-index header of one database file.
type commitTokenFile struct {
	path string
	// database is the process-wide record of the connections to the file, nil when the file could not be identified.
	database *openDatabase

	mu     sync.Mutex
	closed bool
	failed bool
	file   atomic.Pointer[os.File]
}

// openDatabase is what this process knows of one database file: how many connections of this package are open on it and
// the one descriptor it keeps on its wal-index. POSIX drops every fcntl lock a process holds on a file when it closes any
// descriptor of it, and SQLite holds its wal-index locks (the dead-man switch byte among them) through descriptors this
// package cannot see; so the descriptor of the wal-index is closed only when the last connection to the database file has
// closed its SQLite connection, whether or not that connection attached a token.
type openDatabase struct {
	key  [2]uint64
	refs int
	wal  *os.File
}

var openDatabases = struct {
	sync.Mutex
	files map[[2]uint64]*openDatabase
}{files: map[[2]uint64]*openDatabase{}}

// registerDatabase counts a connection to the database file at path; nil when the file is not one that can be identified.
func registerDatabase(path string) *openDatabase {
	key, ok := walIndexKey(path)
	if !ok {
		return nil
	}
	openDatabases.Lock()
	defer openDatabases.Unlock()
	entry := openDatabases.files[key]
	if entry == nil {
		entry = &openDatabase{key: key}
		openDatabases.files[key] = entry
	}
	entry.refs++
	return entry
}

// unregister ends the count of a connection; the last one closes the descriptor of the wal-index.
func (entry *openDatabase) unregister() {
	openDatabases.Lock()
	defer openDatabases.Unlock()
	if entry.refs--; entry.refs == 0 {
		delete(openDatabases.files, entry.key)
		if entry.wal != nil {
			_ = entry.wal.Close()
			entry.wal = nil
		}
	}
}

// walIndex is the descriptor of the wal-index at path, opened by the first caller.
func (entry *openDatabase) walIndex(path string) (*os.File, error) {
	openDatabases.Lock()
	defer openDatabases.Unlock()
	if entry.wal == nil {
		file, err := openWalIndex(path)
		if err != nil {
			return nil, err
		}
		entry.wal = file
	}
	return entry.wal, nil
}

// attach finds the wal-index of the database at path and checks its header is readable. It fails when the database has
// none a reader can use: a database that is not a file, one that is not in WAL mode, a wal-index that is not
// initialized, or a path from which the file cannot be told from another. A failed attach closes nothing: closing the
// descriptor would drop SQLite's locks.
func (source *commitTokenFile) attach() error {
	source.mu.Lock()
	defer source.mu.Unlock()
	if source.closed {
		return errDatabaseNotOpen
	}
	if source.file.Load() != nil {
		return nil
	}
	if source.failed || source.database == nil {
		return errors.New("the wal-index header is not readable")
	}
	shm, err := shmPath(source.path)
	if err != nil {
		return err
	}
	file, err := source.database.walIndex(shm)
	if err != nil {
		return err
	}
	source.file.Store(file)
	if _, ok := source.token(); !ok {
		source.file.Store(nil)
		source.failed = true
		return errors.New("the wal-index header is not readable")
	}
	return nil
}

// shmPath is the wal-index file of the database at path, as SQLite names it: its full path, with symbolic links
// resolved when SQLite resolves them, plus "-shm". A path that is not a file ("", ":memory:", a URI) has none. When the
// path with and without its links resolved names two different wal-index files, one of them is not SQLite's, and the
// database has no wal-index this package can use.
func shmPath(path string) (string, error) {
	if path == "" || path == ":memory:" || strings.HasPrefix(path, "file:") {
		return "", errors.New("the database is not a file")
	}
	full, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(full)
	if err != nil {
		return "", err
	}
	plain, err := os.Stat(full + "-shm")
	if err != nil {
		plain = nil
	}
	linked, err := os.Stat(resolved + "-shm")
	if err != nil {
		linked = nil
	}
	switch {
	case plain == nil && linked == nil:
		return "", errors.New("the database has no wal-index")
	case plain == nil:
		return resolved + "-shm", nil
	case linked == nil || os.SameFile(plain, linked):
		return full + "-shm", nil
	}
	return "", errors.New("the wal-index of the database is ambiguous")
}

// stop ends the reads of the token. The descriptor stays open until release.
func (source *commitTokenFile) stop() {
	source.mu.Lock()
	defer source.mu.Unlock()
	source.closed = true
	source.file.Store(nil)
}

// release ends the count of the connection; call it after the SQLite connection is closed.
func (source *commitTokenFile) release() {
	source.mu.Lock()
	database := source.database
	source.database = nil
	source.mu.Unlock()
	if database != nil {
		database.unregister()
	}
}

// token reads the header of the newest commit. ok is false when the wal-index is not attached or its header is not a
// whole one now.
func (source *commitTokenFile) token() (token chordsqlite.CommitToken, ok bool) {
	file := source.file.Load()
	if file == nil {
		return token, false
	}
	var buffer [2 * walIndexHeaderBytes]byte
	for range headerReads {
		if count, err := file.ReadAt(buffer[:], 0); err != nil || count != len(buffer) {
			return token, false
		}
		first, second := buffer[:walIndexHeaderBytes], buffer[walIndexHeaderBytes:]
		if bytes.Equal(first, second) && walIndexHeaderValid(first) {
			return chordsqlite.CommitToken(first), true
		}
	}
	return token, false
}

// walIndexHeaderValid is walIndexTryHdr's check of one copy: an initialized header of the version SQLite writes, whose
// stored checksum of its first 40 bytes is the one of those bytes.
func walIndexHeaderValid(header []byte) bool {
	if binary.NativeEndian.Uint32(header[0:]) != walIndexVersion || header[12] != 1 {
		return false
	}
	var first, second uint32
	for offset := 0; offset < 40; offset += 8 {
		first += binary.NativeEndian.Uint32(header[offset:]) + second
		second += binary.NativeEndian.Uint32(header[offset+4:]) + first
	}
	return first == binary.NativeEndian.Uint32(header[40:]) && second == binary.NativeEndian.Uint32(header[44:])
}

var _ chordsqlite.CommitTokener = (*NodeSqliteDatabase)(nil)

// CommitToken names the committed state of the database file without a read transaction (see CommitTokener). It is
// available for a WAL-mode database file opened read-write or read-only whose wal-index this connection has created or
// found; the first call that finds none leaves it unavailable.
func (adapter *NodeSqliteDatabase) CommitToken() (chordsqlite.CommitToken, bool) {
	source := adapter.commitTokens
	if source == nil {
		return chordsqlite.CommitToken{}, false
	}
	return source.token()
}

// AttachCommitToken makes CommitToken available: it finds the wal-index of the database, which exists once the database
// has been read in WAL mode, and checks that it is the one SQLite uses. A database that is not in WAL mode, or is one
// connection's alone (locking_mode EXCLUSIVE keeps the wal-index in memory), has none, and is told as an error.
func (adapter *NodeSqliteDatabase) AttachCommitToken() error {
	source := adapter.commitTokens
	if source == nil {
		return errors.New("the database is not a file this package opened")
	}
	for _, setting := range [...]struct{ pragma, want string }{{"journal_mode", "wal"}, {"locking_mode", "normal"}} {
		row, err := adapter.Get("PRAGMA " + setting.pragma)
		if err != nil {
			return err
		}
		if text, _ := row[setting.pragma].(string); !strings.EqualFold(text, setting.want) {
			return errors.New("the database is not in " + setting.pragma + " " + setting.want)
		}
	}
	return source.attach()
}
