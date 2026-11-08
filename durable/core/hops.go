// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package core

import (
	"slices"

	"github.com/MichaelKinsy/PiG/durable/core/abi"
	"github.com/MichaelKinsy/PiG/durable/core/session"
	"github.com/MichaelKinsy/PiG/durable/core/session/hopcost"
)

// Line-job duration (ABI section 5.3, 5.5). Pi's Session line (session.ts #enqueue, #runCommit, writer.ts) holds the line through
// a job's change callback, its settle and its admission to the writer; the Storage call, the publication and the outcome follow
// off the line. The Loop (session/loop.go) runs a job's body at once, when the line is free, and keeps Pi's durations as ticks:
// the handler tells it how long Pi's job takes (Loop.Elapsed), how long the Storage call takes (Loop.StoreTicks), and runs each
// wait of the Loop as a hops chain (Loop.Hops) or a macrotask (Loop.Macro). The statements of a Storage call leave in the step of
// the tick its receipts come, in the step of the wait that ends there.

// jobStats is what the running line job did through the Session's Store, in Pi's await ticks: the change callback's Storage reads,
// the IDs it minted.
type jobStats struct {
	change    int // ticks of the change callback's Storage reads (hopcost.ReadHops)
	mintStart session.ID
	direct    bool                     // its job returns the Storage promise itself (readOnLine(() => storage.task(id)))
	reads     [maxPlanned]hopcost.Read // the first Storage reads the job made, in order (the gate profiles' statement queue, capsql.go)
	nreads    int                      // reads made, planned or not: more than maxPlanned has no statement plan
	firstAt   int                      // the ticks the job had spent when its first Storage statement was issued
}

// nodeSqliteProfile is the storageProfile the host's openNodeSqliteStorage names: SqliteStorage over node.ts, hopcost.Node.
const nodeSqliteProfile hopcost.Profile = "node-sqlite"

// hopProfile is the hopcost profile of open.storageProfile: the gate's lazy proxy, MemoryStorage, SqliteStorage over the
// Node adapter (the profile "node" and the facade's own "node-sqlite", which is that adapter), or SqliteStorage direct over
// the reference's capsql facade (gate-capsql, and the SQLite-shaped adapters without a ported table: cloudflare-do).
func (h *handler) hopProfile() hopcost.Profile {
	switch p := hopcost.Profile(strOf(h.open, "storageProfile")); p {
	case hopcost.Gate, hopcost.Memory, hopcost.Node:
		return p
	case nodeSqliteProfile:
		return hopcost.Node
	}
	return hopcost.GateCapsql
}

// readHops is the ticks of one Storage read for the profile.
func (h *handler) readHops(r hopcost.Read) int { return hopcost.ReadHops(h.hopProfile(), r) }

// read charges one Storage read to the running job.
func (h *handler) read(r hopcost.Read) {
	h.noteFirst()
	h.job.change += h.readHops(r)
	h.planRead(r)
}

// noteFirst records the ticks the running job had spent at its first Storage statement: a statement issued while the transaction of a commit
// holds the Storage's queue waits for it (hopcost.Busy).
func (h *handler) noteFirst() {
	if h.job.nreads == 0 {
		h.job.firstAt = h.job.change
	}
}

// planRead records a Storage read the job made, for the gate profiles' statement queue.
func (h *handler) planRead(r hopcost.Read) {
	if h.capsql() != nil && h.job.nreads < maxPlanned {
		h.job.reads[h.job.nreads] = r
	}
	h.job.nreads++
}

// readRecord charges a client read of the record id the Session's CachedStorage answers (hopcost.RecordRead): a hit is no Storage read. It
// returns how the cache answered, for Session.KeepRecord.
func (h *handler) readRecord(r hopcost.Record, id session.ID) hopcost.Cache {
	c := h.sess.RecordAnswer(r, id)
	if c != hopcost.CacheHit {
		h.noteFirst()
	}
	h.job.change += hopcost.RecordRead(h.hopProfile(), r, c)
	if c != hopcost.CacheHit {
		h.planRead(hopcost.ReadRow)
	}
	return c
}

// batchWrites is the StorageWrite batch of a commit as hopcost counts it: its table writes and merged document actions
// (storage.ts prepareDocumentActions) against the state the mirror holds.
//
//go:noinline
func (h *handler) batchWrites(writes []session.Write) hopcost.Writes {
	var w hopcost.Writes
	// The maps are made at their first entry: most commits copy no document and write no document action.
	var copies map[session.ID]bool
	for i := range writes {
		wr := &writes[i]
		switch wr.Kind {
		case session.WriteConversation:
			w.Conversations++
		case session.WriteEntry:
			w.Entries++
		case session.WriteSubmission:
			w.Submissions++
		case session.WriteTask:
			w.Tasks++
		case session.WriteDocCopy:
			if copies == nil {
				copies = map[session.ID]bool{}
			}
			copies[session.ID(numOf(wr.Record, "id"))] = true
		}
	}
	actions, order, err := h.mir.prepareDocActions(h.mir.resolveCopies(writes))
	if err != nil {
		return w
	}
	for _, id := range order {
		a := actions[id]
		rec := a.create
		if rec == nil {
			if d := h.mir.docs.get(id); d != nil {
				rec = d.record
			}
		}
		act := hopcost.Action{Doc: id, Create: a.create != nil, Copy: copies[id], Retire: a.retire}
		if a.content != nil {
			act.Content = hopcost.Delta
			if a.content.Base {
				act.Content = hopcost.Base
			}
		}
		if act.Copy {
			act.Content = hopcost.Base
		}
		if rec != nil {
			act.CurrentOnly = strOf(objOf(rec, "scope"), "kind") != "conversation" || strOf(rec, "history") == "latest"
		}
		w.Actions = append(w.Actions, act)
	}
	return w
}

// storeTicks is Loop.StoreTicks: the ticks from the writer's call of Storage.commit to the Storage's read of its ID counter (hopcost.IDReadLead
// before the receipts), to the receipts, where the batches' statements land (the Loop commits the batches then), and to the writer's
// resumption from the call (hopcost.Commit, hopcost.CommitTail; MemoryStorage stores in the tick of the call).
func (h *handler) storeTicks(batches [][]session.Write) (read, receive, resume int) {
	p := h.hopProfile()
	if p == hopcost.Memory {
		return 0, 0, hopcost.MemoryCommitResume
	}
	ws := make([]hopcost.Writes, len(batches))
	for i, b := range batches {
		ws[i] = h.batchWrites(b)
	}
	receive, resume = hopcost.CommitCall(p, ws)
	if h.capsql() != nil || p == hopcost.Node {
		h.busy = hopcost.Busy{Commit: h.vnow + receive, Set: true}
	}
	return max(receive-hopcost.IDReadLead(ws), 0), receive, resume
}

// elapsed is Loop.Elapsed: Pi's duration of the line job that just ended, in ticks from its start on the line (session.ts #runCommit
// under #enqueue; hopcost.Job): to the resumption of `await change(tx)` (callback) and to the return of its work (work), which for a
// commit with writes is its admission to the writer. The change callback's Storage reads and mints, the settle and its reads are charged
// by the Session and by the adapter's reads; the Storage call itself is the writer's (storeTicks).
func (h *handler) elapsed(e session.Ended) (callback, work int) {
	st := h.job
	h.job = jobStats{mintStart: h.sess.NextID()}
	h.endGateReads()
	ending := h.endingTx
	h.endingTx = nil
	p := h.hopProfile()
	change := st.change
	if tx := e.Ticks.Change; tx >= 0 && e.Read {
		// A read job charged its own awaits (Session.ChargeRead, a decision region) beside its Storage reads.
		change += tx
	} else if tx >= 0 {
		// A transaction callback ran: Pi's change is its Transaction method awaits and wrapper, not the Store reads.
		change = tx
	} else if mints := int(h.sess.NextID() - st.mintStart); st.mintStart != 0 && mints > 0 {
		change += mints * hopcost.ReadHops(p, hopcost.MintID)
	}
	switch {
	case e.Read:
		if h.timed && st.nreads > 0 {
			// The first statement waits for the transaction of a commit in flight (hopcost.Busy). The gate's lazy proxy adds a tick before it.
			issued := h.vnow + st.firstAt
			if p == hopcost.Gate {
				issued++
			}
			change += h.busy.Delay(p, issued)
		}
		callback, work = 0, hopcost.ReadReturn(change, st.direct)
	case e.Err != nil:
		if e.Ticks.Change < 0 {
			// It failed before its callback ran (the Session failed, or the caller's signal was aborted): the work rejects at once.
			return 0, 0
		}
		callback, work = 0, hopcost.Job{Change: change}.ReturnFailed()
	default:
		j := hopcost.Job{Change: change, Settle: hopcost.Settle(e.Ticks.SettleOwners, e.Ticks.SettleCached, e.Ticks.SettleReads), Membership: e.Ticks.Membership}
		callback, work = j.Callback(), j.Return()
	}
	if ending != nil {
		// The host's callback already spent these passes holding the line: Pi's job duration includes them.
		callback, work = max(callback-ending.elapsed, 0), max(work-ending.elapsed, 0)
	}
	return callback, work
}

// hostReadLead is the ticks the facade needs from the core's answer of a client read to its caller's resumption (host/cf/src/client.ts api,
// facade/harness.ts): the facade's await of the answer is one tick after it, so the core answers one tick before Pi's caller resumes.
const hostReadLead = 1

// holdFor parks release on a hops chain of n hops (effect 14) and reports whether it did.
func (h *handler) holdFor(n int, release func()) bool {
	if n <= 0 {
		return false
	}
	return h.holdChain(abi.Hops{N: uint32(n)}, release)
}

// holdChain parks release on the hops chain c names: the host runs c.N hops and sends hopped after the last.
func (h *handler) holdChain(c abi.Hops, release func()) bool {
	return h.parkChain(c, parkedHops{release: release})
}

// macrotask is Loop.Macro: it queues a macrotask (ABI section 5.5, effect macrotask) and runs r when the host's `hopped` for it arrives.
func (h *handler) macrotask(r session.Resumer) bool {
	id := h.b.EffectBare(abi.EffectMacrotask, nil)
	origin := h.origin()
	h.holdOrigin(origin)
	h.hops = append(h.hops, parkedHops{id: id, due: h.vnow, fn: resumeHop, val: r, step: h.stepNo, origin: origin})
	return true
}

// holdLate parks on a chain of n hops the call of fn(h, id, val) rather than a closure: what the work needs is kept in the parked record. A
// closure that carries a value costs a heap allocation per site (and one for each variable it captures) in TinyGo; fn is a function
// declared at the top level of the package, which costs none.
func (h *handler) holdLate(n int, fn func(h *handler, id uint32, val any), id uint32, val any) bool {
	if n <= 0 {
		return false
	}
	return h.parkChain(abi.Hops{N: uint32(n)}, parkedHops{fn: fn, rid: id, val: val})
}

// parkChain opens the chain c and parks p, whose chain fields it fills in, on it.
func (h *handler) parkChain(c abi.Hops, p parkedHops) bool {
	size := abi.HopsSize(c)
	if h.hopArena == nil {
		h.hopArena = make([]byte, 0, hopArenaSize)
	}
	var id uint32
	switch {
	case len(h.hopArena)+size <= cap(h.hopArena):
		at := len(h.hopArena)
		h.hopArena = abi.AppendHops(h.hopArena, c)
		id = h.b.EffectBare(abi.EffectHops, h.hopArena[at:len(h.hopArena):len(h.hopArena)])
	case size <= abi.ChunkSize:
		id = h.b.EffectBare(abi.EffectHops, abi.AppendHops(make([]byte, 0, size), c))
	default:
		var payload abi.Chunks
		abi.ConsumeHopsChunks(&payload, c)
		id = h.b.EffectChunksBare(abi.EffectHops, payload)
	}
	due := 0
	h.capsql() // decides h.timed
	if h.timed {
		due = h.vnow + int(c.N)
	}
	origin := h.origin()
	h.holdOrigin(origin)
	p.id, p.due, p.step, p.origin = id, due, h.stepNo, origin
	h.hops = append(h.hops, p)
	return true
}

// parkedHops is the work parked on one open hops chain.
type parkedHops struct {
	id      uint32
	due     int // the virtual tick the chain ends (capsql.go); zero without a statement queue
	release func()
	// fn, when set, is what runs instead of release, with the id and value the work parked with (holdLate).
	fn     func(h *handler, id uint32, val any)
	rid    uint32
	val    any
	step   uint64 // the step that opened the chain: a rejected step takes its chains back (dropStepHops)
	origin uint32 // the host async context of the code that parked the work: it continues in it (origin.go)
}

// onHopped runs the work parked on a hops chain (ABI section 5, event 18).
//
//go:noinline
func (h *handler) onHopped(ev *abi.Event) error {
	for i, p := range h.hops {
		if p.id == ev.ID {
			h.hops = slices.Delete(h.hops, i, i+1)
			if h.timed {
				h.vnow = max(h.vnow, p.due)
			}
			h.setOrigin(p.origin)
			if p.fn != nil {
				p.fn(h, p.rid, p.val)
			} else {
				p.release()
			}
			h.dropOrigin(p.origin)
			break
		}
	}
	if h.closing && len(h.hops) == 0 && !h.closed {
		h.closing = false
		h.finishClose()
	}
	return nil
}

// hopsThen is turn.Host.Hops: then runs as a microtask once a hops chain of n hops ran.
func (h *handler) hopsThen(n int, then func()) {
	if !h.holdFor(n, func() { h.loop.Defer(then) }) {
		h.loop.Defer(then)
	}
}

// dropStepHops forgets the hops chains the step in progress opened: a rejected or fatal step drops its effects (abi.Builder.Reject), so no
// hopped event answers them, and the work parked on them would wait for ever (close waits for every parked chain).
func (h *handler) dropStepHops() {
	kept := h.hops[:0]
	for _, p := range h.hops {
		if p.step != h.stepNo {
			kept = append(kept, p)
		} else {
			h.dropOrigin(p.origin)
		}
	}
	clear(h.hops[len(kept):])
	h.hops = kept
}
