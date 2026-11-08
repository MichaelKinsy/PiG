package node

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	chordsqlite "github.com/MichaelKinsy/PiG/chord/sqlite"
)

const killChildEnv = "PIG_SQLITE_KILL_CHILD"

const killRowsPerTransaction = 20

// TestKilledWriterLeavesAnIntactDatabase kills a process that commits transactions in a loop and checks what a reader
// finds. A transaction that committed (the child had printed its number) is there whole, the transaction that was
// running is there whole or not at all, and the file passes integrity_check. This is the crash behaviour of WAL with
// synchronous = NORMAL against a process kill; a power cut can lose the last transactions and is not tested.
func TestKilledWriterLeavesAnIntactDatabase(t *testing.T) {
	if path := os.Getenv(killChildEnv); path != "" {
		runKillChild(path)
		return
	}
	for round, killAfter := range []int{1, 7, 40, 150} {
		path := filepath.Join(t.TempDir(), "kill.sqlite")
		command := exec.Command(os.Args[0], "-test.run=^TestKilledWriterLeavesAnIntactDatabase$")
		command.Env = append(os.Environ(), killChildEnv+"="+path)
		output, err := command.StdoutPipe()
		if err != nil {
			t.Fatal(err)
		}
		if err := command.Start(); err != nil {
			t.Fatal(err)
		}
		acknowledged := 0
		for lines := bufio.NewScanner(output); lines.Scan(); {
			text, ok := strings.CutPrefix(lines.Text(), "committed ")
			if !ok {
				continue
			}
			acknowledged, _ = strconv.Atoi(text)
			if acknowledged >= killAfter {
				break
			}
		}
		_ = command.Process.Kill()
		_ = command.Wait()
		if acknowledged < killAfter {
			t.Fatalf("round %d: the child committed %d transactions, want %d", round, acknowledged, killAfter)
		}

		database, err := OpenNodeSqliteDatabase(path, NodeSqliteDatabaseOptions{})
		if err != nil {
			t.Fatalf("round %d: reopen after kill: %v", round, err)
		}
		check, err := database.Get("PRAGMA integrity_check")
		if err != nil || check["integrity_check"] != "ok" {
			t.Fatalf("round %d: integrity_check = %v, %v", round, check, err)
		}
		counted, err := database.Get("SELECT count(*) AS rows, count(DISTINCT n) AS distinct_rows FROM t")
		if err != nil {
			t.Fatal(err)
		}
		rows := counted["rows"].(int64)
		if rows%killRowsPerTransaction != 0 || rows < int64(acknowledged*killRowsPerTransaction) ||
			rows > int64((acknowledged+1)*killRowsPerTransaction) || counted["distinct_rows"] != rows {
			t.Fatalf("round %d: %d rows after %d acknowledged transactions of %d rows", round, rows, acknowledged,
				killRowsPerTransaction)
		}
		if err := database.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func runKillChild(path string) {
	database, err := OpenNodeSqliteDatabase(path, NodeSqliteDatabaseOptions{})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if err := database.Exec("CREATE TABLE t (n INTEGER PRIMARY KEY, body BLOB)"); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	body := make([]byte, 3000)
	for transaction := 1; ; transaction++ {
		err := database.Transaction(func(tx chordsqlite.SqliteExecutor) error {
			for row := range killRowsPerTransaction {
				if err := tx.Run("INSERT INTO t (n, body) VALUES (?, ?)", (transaction-1)*killRowsPerTransaction+row, body); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		fmt.Println("committed", transaction)
	}
}
