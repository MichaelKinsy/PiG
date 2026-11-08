package node

import (
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"

	chordsqlite "github.com/MichaelKinsy/PiG/chord/sqlite"
)

// pig additive (D104): the commit token has no Pi counterpart; these tests pin what the cache relies on.

func openTokenDatabase(t *testing.T, path string) *NodeSqliteDatabase {
	t.Helper()
	switch runtime.GOOS {
	case "linux", "darwin", "freebsd":
	default:
		t.Skip("no commit token on " + runtime.GOOS)
	}
	database, err := OpenNodeSqliteDatabase(path, NodeSqliteDatabaseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	return database
}

func mustToken(t *testing.T, database *NodeSqliteDatabase) chordsqlite.CommitToken {
	t.Helper()
	token, ok := database.CommitToken()
	if !ok {
		t.Fatal("no commit token")
	}
	return token
}

func TestCommitTokenNamesTheCommittedState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token.sqlite")
	reader := openTokenDatabase(t, path)
	if err := reader.Exec("CREATE TABLE t (n INTEGER)"); err != nil {
		t.Fatal(err)
	}
	if err := reader.AttachCommitToken(); err != nil {
		t.Fatal(err)
	}
	writer := openTokenDatabase(t, path)

	start := mustToken(t, reader)
	if again := mustToken(t, reader); again != start {
		t.Fatal("two tokens of the same state differ")
	}
	if _, err := reader.All("SELECT * FROM t"); err != nil {
		t.Fatal(err)
	}
	if after := mustToken(t, reader); after != start {
		t.Fatal("a read changed the token")
	}
	previous := start
	for round := range 5 {
		// Commits of the connection itself and of another one: each moves the token by exactly one commit.
		database := writer
		if round%2 == 1 {
			database = reader
		}
		if err := database.Run("INSERT INTO t (n) VALUES (?)", round); err != nil {
			t.Fatal(err)
		}
		next := mustToken(t, reader)
		if next == previous || !next.Follows(previous) {
			t.Fatalf("round %d: token %x does not follow %x", round, next, previous)
		}
		previous = next
	}
	// A transaction that commits is one commit, however many statements; one that rolls back is none.
	if err := writer.Transaction(func(tx chordsqlite.SqliteExecutor) error {
		for n := range 10 {
			if err := tx.Run("INSERT INTO t (n) VALUES (?)", n); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if next := mustToken(t, reader); !next.Follows(previous) {
		t.Fatal("a transaction of ten statements is not one commit")
	} else {
		previous = next
	}
	_ = writer.Transaction(func(tx chordsqlite.SqliteExecutor) error {
		if err := tx.Run("INSERT INTO t (n) VALUES (99)"); err != nil {
			return err
		}
		return os.ErrInvalid
	})
	if next := mustToken(t, reader); next != previous {
		t.Fatal("a rolled back transaction changed the token")
	}
	// A checkpoint that restarts the log writes a new header without a commit: the token differs and no one commit apart.
	if err := writer.Exec("PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		t.Fatal(err)
	}
	if next := mustToken(t, reader); next == previous || next.Follows(previous) {
		t.Fatalf("a restarting checkpoint: token %x, before %x", next, previous)
	}
}

func TestCommitTokenIsUnavailableWhereItCannotBeRead(t *testing.T) {
	memory := openTokenDatabase(t, ":memory:")
	if err := memory.AttachCommitToken(); err == nil {
		t.Fatal("an in-memory database has a commit token")
	}
	if _, ok := memory.CommitToken(); ok {
		t.Fatal("an in-memory database named its state")
	}

	path := filepath.Join(t.TempDir(), "closed.sqlite")
	database := openTokenDatabase(t, path)
	if err := database.Exec("CREATE TABLE t (n INTEGER)"); err != nil {
		t.Fatal(err)
	}
	if _, ok := database.CommitToken(); ok {
		t.Fatal("a token before the attach")
	}
	if err := database.AttachCommitToken(); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	if _, ok := database.CommitToken(); ok {
		t.Fatal("a closed database named its state")
	}

	// A database that is not in WAL mode keeps no wal-index.
	rollback := filepath.Join(t.TempDir(), "rollback.sqlite")
	plain, err := NewDatabaseSync(rollback, DatabaseSyncOptions{})
	if err != nil {
		t.Fatal(err)
	}
	adapter := NewNodeSqliteDatabase(plain)
	t.Cleanup(func() { _ = adapter.Close() })
	if err := adapter.Exec("CREATE TABLE t (n INTEGER)"); err != nil {
		t.Fatal(err)
	}
	if err := adapter.AttachCommitToken(); err == nil {
		t.Fatal("a database in rollback-journal mode has a commit token")
	}

	// Exclusive locking keeps the wal-index in memory: another connection cannot be told apart, so none is offered.
	exclusive := openTokenDatabase(t, filepath.Join(t.TempDir(), "exclusive.sqlite"))
	if err := exclusive.Exec("PRAGMA locking_mode = EXCLUSIVE"); err != nil {
		t.Fatal(err)
	}
	if err := exclusive.Exec("CREATE TABLE t (n INTEGER)"); err != nil {
		t.Fatal(err)
	}
	if err := exclusive.AttachCommitToken(); err == nil {
		t.Fatal("a database in exclusive locking mode has a commit token")
	}
}

// Any change of one byte of a header is told as no token: the two copies SQLite keeps, the checksum, the version.
func TestCommitTokenRejectsAHeaderThatIsNotWhole(t *testing.T) {
	path := filepath.Join(t.TempDir(), "header.sqlite")
	database := openTokenDatabase(t, path)
	if err := database.Exec("CREATE TABLE t (n INTEGER)"); err != nil {
		t.Fatal(err)
	}
	if err := database.AttachCommitToken(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path + "-shm")
	if err != nil {
		t.Fatal(err)
	}
	header := raw[:walIndexHeaderBytes]
	if !walIndexHeaderValid(header) {
		t.Fatal("the header SQLite wrote is not valid")
	}
	for index := range header {
		for _, flip := range []byte{1, 0x80, 0xff} {
			damaged := append([]byte(nil), header...)
			damaged[index] ^= flip
			if walIndexHeaderValid(damaged) {
				t.Fatalf("a header with byte %d changed by %#x is valid", index, flip)
			}
		}
	}
}

// A token read while another connection commits is a whole header some commit left: never torn, never going back.
func TestCommitTokenIsWholeWhileAnotherConnectionCommits(t *testing.T) {
	path := filepath.Join(t.TempDir(), "race.sqlite")
	reader := openTokenDatabase(t, path)
	if err := reader.Exec("CREATE TABLE t (n INTEGER)"); err != nil {
		t.Fatal(err)
	}
	if err := reader.AttachCommitToken(); err != nil {
		t.Fatal(err)
	}
	writer := openTokenDatabase(t, path)
	var done atomic.Bool
	var group sync.WaitGroup
	group.Add(1)
	go func() {
		defer group.Done()
		defer done.Store(true)
		for n := range 3000 {
			if err := writer.Run("INSERT INTO t (n) VALUES (?)", n); err != nil {
				t.Error(err)
				return
			}
		}
	}()
	last, reads, unavailable := mustToken(t, reader).Commits(), 0, 0
	for !done.Load() {
		token, ok := reader.CommitToken()
		reads++
		if !ok {
			unavailable++
			continue
		}
		// The count only goes forward, by the commits made since: far fewer than a wrap of 32 bits.
		if step := token.Commits() - last; step > 1<<20 {
			t.Fatalf("the commit count went from %d to %d", last, token.Commits())
		}
		last = token.Commits()
	}
	group.Wait()
	if final := mustToken(t, reader).Commits(); final < last {
		t.Fatalf("the final count %d is behind %d", final, last)
	}
	t.Logf("%d token reads, %d unavailable", reads, unavailable)
}

// A database opened through a symbolic link names the same wal-index as one opened through its target, or offers no token.
func TestCommitTokenThroughASymbolicLink(t *testing.T) {
	directory := t.TempDir()
	real := filepath.Join(directory, "real")
	if err := os.Mkdir(real, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(directory, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skip("no symbolic links:", err)
	}
	viaLink := openTokenDatabase(t, filepath.Join(link, "x.sqlite"))
	if err := viaLink.Exec("CREATE TABLE t (n INTEGER)"); err != nil {
		t.Fatal(err)
	}
	viaTarget := openTokenDatabase(t, filepath.Join(real, "x.sqlite"))
	if err := viaTarget.Exec("SELECT count(*) FROM t"); err != nil {
		t.Fatal(err)
	}
	for _, database := range []*NodeSqliteDatabase{viaLink, viaTarget} {
		if err := database.AttachCommitToken(); err != nil {
			t.Logf("no token through this path: %v", err)
			continue
		}
		before := mustToken(t, database)
		other := viaTarget
		if database == viaTarget {
			other = viaLink
		}
		if err := other.Run("INSERT INTO t (n) VALUES (1)"); err != nil {
			t.Fatal(err)
		}
		if after := mustToken(t, database); !after.Follows(before) {
			t.Fatalf("a commit through the other path did not move the token")
		}
	}
}

// A read-only connection reads the token of a file another connection writes.
func TestCommitTokenOnAReadOnlyConnection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "readonly.sqlite")
	writer := openTokenDatabase(t, path)
	if err := writer.Exec("CREATE TABLE t (n INTEGER)"); err != nil {
		t.Fatal(err)
	}
	reader, err := OpenNodeSqliteDatabase(path, NodeSqliteDatabaseOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reader.Close() })
	if _, err := reader.All("SELECT * FROM t"); err != nil {
		t.Fatal(err)
	}
	if err := reader.AttachCommitToken(); err != nil {
		t.Fatal(err)
	}
	before := mustToken(t, reader)
	if err := writer.Run("INSERT INTO t (n) VALUES (1)"); err != nil {
		t.Fatal(err)
	}
	if after := mustToken(t, reader); !after.Follows(before) {
		t.Fatalf("a commit of another connection did not move the read-only token: %x after %x", after, before)
	}
}
