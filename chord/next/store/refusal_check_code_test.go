package store

import (
	"errors"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/sqlite"
	"github.com/MichaelKinsy/PiG/chord/sqlite/node"
)

// The extended result code of a CHECK failure through the modernc driver (chord/sqlite/node): Pi's RefusalOf reads
// `errcode === 275` on Node's `node:sqlite`; this proves what the Go error carries, on which refusal.go's isCheck
// relies.

// checkFailure is the error of an INSERT violating a CHECK, as the node facade returns it from run.
func checkFailure(t *testing.T, run func(db *node.NodeSqliteDatabase) error) error {
	t.Helper()
	db, err := node.OpenNodeSqliteDatabase(":memory:", node.NodeSqliteDatabaseOptions{})
	noError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	noError(t, db.Exec(`CREATE TABLE "checked" (n INTEGER CHECK (n < 10))`))
	err = run(db)
	if err == nil {
		t.Fatal("the INSERT violating the CHECK succeeded")
	}
	return err
}

func TestCheckFailureCode(t *testing.T) {
	t.Run("a CHECK failure carries the extended code 275 through the facade's run", func(t *testing.T) {
		err := checkFailure(t, func(db *node.NodeSqliteDatabase) error { return db.Run(`INSERT INTO "checked" VALUES (11)`) })
		var coded interface{ Code() int }
		if !errors.As(err, &coded) {
			t.Fatalf("the error %T (%v) has no Code() int", err, err)
		}
		if coded.Code() != sqliteConstraintCheck {
			t.Fatalf("Code() = %d, want %d (message %q)", coded.Code(), sqliteConstraintCheck, err.Error())
		}
	})

	t.Run("it keeps the code through a transaction's callback error", func(t *testing.T) {
		err := checkFailure(t, func(db *node.NodeSqliteDatabase) error {
			return db.Transaction(func(executor sqlite.SqliteExecutor) error {
				return executor.Run(`INSERT INTO "checked" VALUES (11)`)
			})
		})
		var coded interface{ Code() int }
		if !errors.As(err, &coded) || coded.Code() != sqliteConstraintCheck {
			t.Fatalf("the transaction's error %T (%v) does not carry code %d", err, err, sqliteConstraintCheck)
		}
	})

	t.Run("RefusalOf tells the refusal a commit returns a check from the code alone", func(t *testing.T) {
		err := checkFailure(t, func(db *node.NodeSqliteDatabase) error {
			return db.Run(`INSERT INTO "checked" VALUES (11)`)
		})
		// The same error without its message: only the code is left to tell it by.
		if !isCheck(codeOnly{err}, "") {
			t.Fatal("isCheck did not tell a CHECK failure by its extended code")
		}
		refusal := RefusalOf(err, "label")
		if refusal == nil || refusal.Kind != RefusalCheck {
			t.Fatalf("RefusalOf = %+v, want a check refusal", refusal)
		}
	})

	t.Run("an error with a code is told by it alone; one without a code, by its message (Pi's errcode branch)", func(t *testing.T) {
		other := &codedError{code: 1, message: "CHECK constraint failed: n < 10"}
		if isCheck(other, other.Error()) {
			t.Fatal("an error with another code was told a CHECK by its message")
		}
		check := &codedError{code: sqliteConstraintCheck, message: "constraint"}
		if !isCheck(check, check.Error()) {
			t.Fatal("an error with the CHECK code was not told a CHECK")
		}
		bare := errors.New("INSERT failed: CHECK constraint failed: n < 10")
		if !isCheck(bare, bare.Error()) {
			t.Fatal("an error without a code was not told a CHECK by its message")
		}
	})
}

// codeOnly is err's code with a message that names nothing.
type codeOnly struct{ err error }

func (codeOnly) Error() string { return "constraint" }

func (wrapped codeOnly) Unwrap() error { return wrapped.err }
