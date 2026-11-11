// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package session_test

import (
	"testing"

	"github.com/MichaelKinsy/PiG/durable/core/session"
	"github.com/MichaelKinsy/PiG/durable/core/session/hopcost"
	"github.com/MichaelKinsy/PiG/durable/core/session/sessiontest"
)

type resumeFunc func()

func (f resumeFunc) Resume() { f() }

// A host transaction's mint is noted at the tick Pi's Storage mints it, though the host's first operation reaches the core hopcost.HostFirstOp ticks after
// the grant and the Loop runs a job's body at the tick of the call (Pi's job starts a tick later on an idle line): the mint of a method that
// starts `after` ticks into Pi's callback is in the Storage's counter from the tick Pi's callback would have had it, or at once when the host is
// already later than that.
func TestAHostTransactionsMintIsNotedWhenPisStorageMintsIt(t *testing.T) {
	run := func(t *testing.T, queued bool, task session.ID, after int) (grant, noted int) {
		t.Helper()
		mem := sessiontest.New()
		sess := session.New(mem, mem.NextID(), func() float64 { return 1 })
		loop := bound(sess)
		c := newHopClock(loop)
		var tx *session.Tx
		grant = -1
		var firstEnd *session.HostJob
		if queued {
			// A job ahead of it holds the line, so this one starts when its turn comes: at the tick Pi's job starts.
			firstEnd = loop.LineHost(func() error { return nil }, func(error) {})
			c.run(t)
		}
		loop.LineHost(func() error {
			var err error
			tx, err = sess.Begin(session.TxScope{Host: true, TaskID: task})
			grant = c.now
			return err
		}, func(error) {})
		if queued {
			firstEnd.End(nil)
		}
		c.run(t)
		if tx == nil {
			t.Fatal("the host transaction was not granted")
		}
		minted := sess.StorageNextID()
		// The host's first operation reaches the core hopcost.HostFirstOp ticks after the grant, and one sampler runs at every tick after it.
		var seen []session.ID
		at := func(n int, fn func()) { c.due = append(c.due, hopDue{grant + n, resumeFunc(fn)}) }
		at(hopcost.HostFirstOp, func() { tx.Mint(0, after) })
		for k := hopcost.HostFirstOp; k <= hopcost.HostFirstOp+12; k++ {
			at(k+1, func() { seen = append(seen, sess.StorageNextID()) })
		}
		c.run(t)
		for i, id := range seen {
			if id > minted {
				return grant, hopcost.HostFirstOp + i
			}
		}
		t.Fatalf("the mint was never noted: %v", seen)
		return
	}
	for _, c := range []struct {
		name   string
		queued bool
		task   session.ID // nonzero: a task's runtime.commit, whose callback starts after the #gated region
		after  int
		want   int // ticks from the grant to the noting
	}{
		// Pi's callback, on a job queued behind another, starts at the grant: a mint `after` ticks in is at the grant + after.
		{"queued, late mint", true, 0, 7, 7},
		{"queued, mint the host is already late for", true, 0, 1, hopcost.HostFirstOp},
		// On an idle line Pi's job starts a tick after the call that the Loop's body runs in.
		{"idle, late mint", false, 0, 7, 8},
		{"idle, mint the host is already late for", false, 0, 0, hopcost.HostFirstOp},
		// A task's callback starts hopcost.GatedRegion ticks after its job does.
		{"queued task commit, late mint", true, 1, 7, 7 + hopcost.GatedRegion},
		{"idle task commit, late mint", false, 1, 7, 8 + hopcost.GatedRegion},
	} {
		t.Run(c.name, func(t *testing.T) {
			if _, noted := run(t, c.queued, c.task, c.after); noted != c.want {
				t.Fatalf("noted %d ticks after the grant, want %d", noted, c.want)
			}
		})
	}
}
