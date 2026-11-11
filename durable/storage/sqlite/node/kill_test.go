package node

// A killed process never leaves half a call of Durable's SQLite inserts: every statement of a call runs in one transaction,
// whatever the rows' VALUES statements (insert_values_differential_test.go in the parent package).

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/durable"
)

const killChildEnv = "PIG_DURABLE_SQLITE_KILL_CHILD"

// killBatches is the batches of each call the child commits: more than one VALUES statement per table (16 rows of 5
// cells fill one), so a kill can fall between the statements of a call.
const killBatches = 40

func TestKilledDurableWriterLeavesWholeCalls(t *testing.T) {
	if path := os.Getenv(killChildEnv); path != "" {
		runKillChild(path)
		return
	}
	for round, killAfter := range []int{1, 5, 25} {
		path := filepath.Join(t.TempDir(), "kill.sqlite")
		command := exec.Command(os.Args[0], "-test.run=^TestKilledDurableWriterLeavesWholeCalls$")
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
			t.Fatalf("round %d: the child committed %d calls, want %d", round, acknowledged, killAfter)
		}

		database, err := OpenNodeSqliteDatabase(path, NodeSqliteDatabaseOptions{})
		if err != nil {
			t.Fatalf("round %d: reopen after kill: %v", round, err)
		}
		check, err := database.Get("PRAGMA integrity_check")
		if err != nil || check["integrity_check"] != "ok" {
			t.Fatalf("round %d: integrity_check = %v, %v", round, check, err)
		}
		// One row of each table per batch: every acknowledged call is there, and the one running when the process died is
		// there whole or not at all (how many calls the child ran before the kill lands depends on the host's load).
		counted, err := database.Get(`SELECT
			(SELECT count(*) FROM entries) AS entries,
			(SELECT count(DISTINCT commit_seq) FROM entries) AS seqs,
			(SELECT count(*) FROM record_ids WHERE record_type = 'entry') AS claimed`)
		if err != nil {
			t.Fatal(err)
		}
		entries := counted["entries"].(int64)
		if entries%killBatches != 0 || entries < int64(acknowledged*killBatches) ||
			counted["claimed"] != entries || counted["seqs"] != entries {
			t.Fatalf("round %d: %v after %d acknowledged calls of %d batches", round, counted, acknowledged, killBatches)
		}
		if err := database.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func runKillChild(path string) {
	fail := func(err error) {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	storage, err := OpenNodeSqliteStorage(path, NodeSqliteStorageOptions{})
	if err != nil {
		fail(err)
	}
	for call := 1; ; call++ {
		batches := make([][]durable.StorageWrite, killBatches)
		for index := range batches {
			id, err := storage.MintId()
			if err != nil {
				fail(err)
			}
			batches[index] = []durable.StorageWrite{
				durable.EntryWrite{Value: durable.EntryRecord{Id: durable.IdFromNumber[durable.EntryId](id), ConversationId: durable.ROOT_CONVERSATION_ID, Kind: "note", Data: strings.Repeat("x", 100)}},
			}
		}
		if call == 1 {
			batches[0] = append([]durable.StorageWrite{durable.ConversationWrite{Value: durable.ConversationRecord{Id: durable.ROOT_CONVERSATION_ID}}}, batches[0]...)
		}
		result, err := storage.Commit(background, batches, func(int, durable.Seq) {})
		if err != nil {
			fail(err)
		}
		if result.Stored != killBatches {
			fail(fmt.Errorf("commit stored %d of %d", result.Stored, killBatches))
		}
		fmt.Println("committed", call)
	}
}
