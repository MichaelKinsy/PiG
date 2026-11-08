// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package sqlhost

import (
	"fmt"
	"slices"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/MichaelKinsy/PiG/durable/core/abi"
)

// OnCommittedRun is called once for each store transaction: a Storage call's joined batches are one run (SqliteStorage.commit is one
// transaction, one capsql trace line), while OnCommitted still sees each commit; both run after the store applied the run.
func TestOnCommittedRunSeesEachStoreTransactionOnce(t *testing.T) {
	db, err := OpenDB(t.Context(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Run(t.Context(), `CREATE TABLE probe (n INTEGER)`, nil); err != nil {
		t.Fatal(err)
	}
	var log []string
	h := &Host{ctx: t.Context(), opts: Options{
		DB:  db,
		SQL: []string{`INSERT INTO probe (n) VALUES (?)`},
		OnCommitted: func(c abi.Commit, _ []string) {
			log = append(log, fmt.Sprintf("commit %d", c.Seq))
		},
		OnCommittedRun: func(run []abi.Commit, _ []string) {
			seqs := make([]int64, len(run))
			for i, c := range run {
				seqs[i] = c.Seq
			}
			log = append(log, fmt.Sprintf("run %v", seqs))
		},
	}}
	insert := func(seq int64, joined bool) abi.Commit {
		return abi.Commit{Seq: seq, Joined: joined, Stmts: []abi.Stmt{{SQL: 0, Params: []abi.Value{abi.Int(seq)}}}}
	}
	if err := h.applyCommits([]abi.Commit{insert(1, false), insert(2, false), insert(3, true), insert(4, false)}); err != nil {
		t.Fatal(err)
	}
	want := []string{"commit 1", "run [1]", "commit 2", "commit 3", "run [2 3]", "commit 4", "run [4]"}
	if !slices.Equal(log, want) {
		t.Fatalf("observed %v, want %v", log, want)
	}
}
