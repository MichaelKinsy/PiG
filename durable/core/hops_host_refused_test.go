// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package core

import (
	"testing"

	"github.com/MichaelKinsy/PiG/durable/core/abi"
	"github.com/MichaelKinsy/PiG/durable/core/jv"
	"github.com/MichaelKinsy/PiG/durable/core/session"
	"github.com/MichaelKinsy/PiG/durable/core/session/sessiontest"
)

// Session.Finish records a host transaction's ticks for the job that ends it (jobHops). A commit the settle refuses ends no
// job: the line is freed at once, so its ticks must not stay behind for the next line job, which would take them as its own
// change (a sweep or scan job's duration).
func TestARefusedHostCommitLeavesNoTicksForTheNextJob(t *testing.T) {
	mem := sessiontest.New()
	s := session.New(mem, mem.NextID(), func() float64 { return 0 })
	h := &handler{b: abi.NewBuilder(), sess: s, open: parsed(t, `{"storageProfile":"gate-capsql"}`), txs: map[uint32]*hostTx{}}
	tx, err := s.Begin(session.TxScope{})
	if err != nil {
		t.Fatal(err)
	}
	def := &session.DocDef{Kind: "t.note", Version: 1, Scope: session.ScopeSession, History: session.HistoryLatest,
		Initial: func(any) *jv.Object { return jv.NewObject() }}
	if _, err := tx.Doc(def, session.DocArgs{}); err != nil {
		t.Fatal(err)
	}
	// A conversation owned by a task that the same transaction then ends: settle's validateOwners refuses the commit
	// (session-tables.test.ts "rejects missing, terminal, and abort-marked conversation owners atomically").
	root, err := tx.CreateRootConversation()
	if err != nil {
		t.Fatal(err)
	}
	cid, _ := root.Get("id")
	taskDef := &session.TaskDef{Name: "owner", Version: 1, Initial: func(any) any { return jv.NewObject() }}
	task, err := tx.CreateTask(taskDef, jv.NewObject(), session.TaskOptions{ConversationID: session.ID(cid.(float64))})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.CreateConversation(session.Ownership{Task: task}); err != nil {
		t.Fatal(err)
	}
	ended := jv.Clone(tx.StagedTasks()[0]).(*jv.Object)
	outcome := jv.NewObject()
	outcome.Set("status", "completed")
	state := jv.NewObject()
	state.Set("status", "terminal")
	state.Set("outcome", outcome)
	ended.Set("state", state)
	if err := tx.SetTask(ended); err != nil {
		t.Fatal(err)
	}
	ht := &hostTx{id: 1, tx: tx}
	h.txs[1] = ht
	if err := h.txEnd(ht, nil); err == nil {
		t.Fatal("the commit of a conversation owned by a task it ends was not refused")
	}
	if got := s.PeekTicks(); got >= 0 {
		t.Fatalf("the refused commit left %d ticks for the next job", got)
	}
}
