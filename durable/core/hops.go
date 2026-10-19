// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package core

import (
	"slices"

	"github.com/MichaelKinsy/PiG/durable/core/abi"
	"github.com/MichaelKinsy/PiG/durable/core/session"
	"github.com/MichaelKinsy/PiG/durable/core/session/hopcost"
)

// Line-job duration (dcore-trace A1, ABI section 5.3). Pi's Session line is a FIFO of jobs, and a job holds the line for
// the awaits of its change callback, its settle and its Storage commit (session.ts #runCommit :415-443). The core runs a
// job's body at once; when the host may act meanwhile, the job's completion (publication, done, release of the line)
// waits for a hops chain as long as Pi's job, so a host call that reaches Pi's line during the job queues behind it and
// ahead of the job's continuation, as in Pi. The job's own body still runs at once, so its storage reads see the state
// at the job's start, as Pi's line guarantees.

// jobStats is what the running line job did through the Session's Store, in Pi's await ticks: the change callback's
// Storage reads, the IDs it minted, the reads the settle makes, and the commit's batch.
type jobStats struct {
	change       int // ticks of the change callback's Storage reads (hopcost.ReadHops)
	mintStart    session.ID
	settleRows   int                      // single-statement reads of settleSuccess (hopcost.Settle)
	settleOwners int                      // created tasks with an owner: validateOwners awaits each (hopcost.Settle)
	settleCached int                      // replaced tasks the callback had read: assemble awaits a settled promise (hopcost.Settle)
	writes       *hopcost.Writes          // the commit's batch; nil: no commit
	readLine     bool                     // a Session.readOnLine job (api reads, context bounds): no change callback, settle or commit
	direct       bool                     // its job returns the Storage promise itself (readOnLine(() => storage.task(id)))
	reads        [maxPlanned]hopcost.Read // the first Storage reads the job made, in order (the gate profiles' statement queue, capsql.go)
	nreads       int                      // reads made, planned or not: more than maxPlanned has no statement plan
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
	h.job.change += h.readHops(r)
	if h.capsql() != nil {
		if h.job.nreads < maxPlanned {
			h.job.reads[h.job.nreads] = r
		}
		h.job.nreads++
	}
}

// jobCommit records the batch of the running job's commit: its table writes and merged document actions
// (storage.ts prepareDocumentActions), and the reads settleSuccess makes for it (the committed task of each replaced task
// and a scanDocuments page for each task going terminal, transaction.ts assemble).
//
//go:noinline
func (h *handler) jobCommit(writes []session.Write, actions map[session.ID]*mirrorAction, order []session.ID) {
	w := &hopcost.Writes{}
	// The maps are made at their first entry: most commits copy no document and write no document action.
	var copies map[session.ID]bool
	h.job.settleRows += h.settleRows(writes) - h.sess.SettleCached()
	h.job.settleCached += h.sess.SettleCached()
	h.job.settleOwners += h.settleOwners(writes)
	for _, wr := range writes {
		switch wr.Kind {
		case session.WriteConversation, session.WriteEntry, session.WriteSubmission:
			w.Tables++
		case session.WriteTask:
			w.Tables++
		case session.WriteDocCreate:
			w.DocCreates++
		case session.WriteDocCopy:
			w.DocCreates++
			if copies == nil {
				copies = map[session.ID]bool{}
			}
			copies[session.ID(numOf(wr.Record, "id"))] = true
		case session.WriteDocChange, session.WriteDocRetire:
			w.DocEdits++
		}
	}
	var seen map[string]bool
	for _, id := range order {
		a := actions[id]
		rec := a.create
		if rec == nil {
			if d := h.mir.docs.get(id); d != nil {
				rec = d.record
			}
		}
		act := hopcost.Action{Create: a.create != nil, Copy: copies[id], Retire: a.retire}
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
			key := recordAddrKey(rec)
			act.KnownAddress = seen[key]
			if seen == nil {
				seen = map[string]bool{}
			}
			seen[key] = true
		}
		w.Actions = append(w.Actions, act)
	}
	h.job.writes = w
}

// settleRows is the single-statement reads settleSuccess makes for a commit's writes: the committed task of each replaced task
// and a scanDocuments page for each task going terminal (transaction.ts assemble), and the committed submission of each
// staged submission change (:818-823).
func (h *handler) settleRows(writes []session.Write) int {
	// The committed record of each submission change the callback staged without having read it (transaction.ts :818-823).
	rows := h.sess.SettleReads()
	for _, wr := range writes {
		if wr.Kind != session.WriteTask {
			continue
		}
		if existing, _ := h.mir.Task(session.ID(numOf(wr.Record, "id"))); existing != nil {
			rows++
		}
		if strOf(objOf(wr.Record, "state"), "status") == session.StatusTerminal {
			rows++
		}
	}
	return rows
}

// settleOwners is the owner entries settleSuccess's validateOwners awaits one by one: each task this commit creates with an
// owner (transaction.ts :861-863, :865).
func (h *handler) settleOwners(writes []session.Write) int {
	owners := 0
	for _, wr := range writes {
		if wr.Kind != session.WriteTask || numOf(wr.Record, "owner") == 0 {
			continue
		}
		if existing, _ := h.mir.Task(session.ID(numOf(wr.Record, "id"))); existing == nil {
			owners++
		}
	}
	return owners
}

// commitCallHops is the ticks from the start of the running line job to its call of Storage.commit (session.ts #runCommit
// :415-440): `await change(tx)`, then `await tx.settleSuccess()`; the commit's own hops (hopcost.Commit) follow the call.
func (h *handler) commitCallHops(writes []session.Write) int {
	change := h.job.change
	if tx := h.sess.PeekTicks(); tx >= 0 {
		change = tx
	} else if mints := int(h.sess.NextID() - h.job.mintStart); h.job.mintStart != 0 && mints > 0 {
		change += mints * hopcost.ReadHops(h.hopProfile(), hopcost.MintID)
	}
	return 1 + change + 1 + hopcost.Settle(h.hopProfile(), h.settleRows(writes)-h.sess.SettleCached(), h.sess.SettleCached(), h.settleOwners(writes))
}

// jobHops is Pi's duration of the line job that just finished, from its start on the line to its resolution
// (session.ts #runCommit :405-446 under #enqueue :531; hopcost.Job): the change callback's Storage reads and mints, the
// settle and its reads, the Storage commit, and the return through #enqueue.
func (h *handler) jobHops() int {
	st := h.job
	p := h.hopProfile()
	change := st.change
	tx := h.sess.TakeTicks()
	if tx >= 0 {
		// A transaction callback ran: Pi's change is its Transaction method awaits and wrapper, not the Store reads.
		change = tx
	} else if mints := int(h.sess.NextID() - st.mintStart); st.mintStart != 0 && mints > 0 {
		change += mints * hopcost.ReadHops(p, hopcost.MintID)
	}
	if st.readLine || (h.loop != nil && h.loop.Reading) {
		return hopcost.ReadOnLine(change, st.direct)
	}
	return hopcost.Job{Change: change, Settle: hopcost.Settle(p, st.settleRows, st.settleCached, st.settleOwners), Writes: st.writes}.Hops(p)
}

// holdJob is the Loop's Hold: it parks the finished job's completion on a hops chain of the job's duration. It parks every
// job: a step cannot tell whether host code woken by an earlier step (a caller resuming on an api_result three hops
// later) is still pending, so "the host cannot interleave" is not observable from the core.
func (h *handler) holdJob(release func()) bool {
	if h.shadow.n > 0 {
		// The host's Storage still runs reads this job issued: the job completes when it is done with them.
		h.shadow.after(func() {
			if !h.holdJobNow(release) {
				release()
			}
		})
		return true
	}
	return h.holdJobNow(release)
}

func (h *handler) holdJobNow(release func()) bool {
	st := h.job
	n := h.jobHops() + h.loop.TakeStartDelay()
	h.job = jobStats{mintStart: h.sess.NextID()}
	held := h.held
	h.held = nil
	if len(held) == 0 {
		if h.holdReadJob(st, n, release) {
			return true
		}
		if n >= 2 && h.loop.EndingClient() {
			// A host caller awaits the job's `run` promise, which settles a hop before the tail resolves (session.ts :105-115).
			return h.holdFor(n-1, func() { h.loop.Settle(); release() })
		}
		if h.loop.QuietEnd() {
			// The end and the tail that follows it would each run nothing: one chain of both.
			return h.holdFor(n+tailHops, h.loop.QuietRelease())
		}
		return h.holdFor(n, release)
	}
	if h.opening {
		for _, c := range held {
			h.b.Commit(c.Seq, c.Stmts)
		}
		return h.holdFor(n, release)
	}
	// The job's statements land at Pi's capsql COMMIT, tail hops before the job resolves (dcore-trace 4.3): #runCommit resumes
	// from Storage.commit hopcost.CommitTail after the COMMIT, and the job's own return adds pubBeforeEnd. A host call that
	// reaches Pi's line during the job is admitted before the COMMIT, so the statements wait for that hop and not for the
	// step that ran the job body.
	tail := hopcost.CommitTail(h.hopProfile()) + pubBeforeEnd
	// The commit is published when #runCommit resumes from Storage.commit, two hops before the job's run resolves
	// (session.ts :446-449): commit listeners and publication waiters see it there, the job's caller resumes at its end.
	p := max(n-pubBeforeEnd, 0)
	if n-tail < 1 {
		h.flush(held)
		return h.holdSteps(n, []holdStep{{p, h.loop.Publish}}, release)
	}
	// The host applies the statements in the microtask of hop n-tail of the chain that runs to the publication, where the
	// core would have emitted them; the core runs again at the publication and parks the last pubBeforeEnd hops.
	return h.holdChain(abi.Hops{N: uint32(p), At: uint32(n - tail), Commits: held}, func() {
		h.loop.Publish()
		if !h.holdFor(n-p, release) {
			release()
		}
	})
}

// tailHops is the tick a finished line job's tail holds the line for (session.ts #enqueue :530-537; Loop.Tail).
const tailHops = 1

// pubBeforeEnd is the hops from a commit job's publication to its end: `#runCommit` returns after publishing and `#enqueue`'s
// run resolves one hop later (session.ts :446-449, :531).
const pubBeforeEnd = 2

// holdStep is an action at a hop offset of a held line job.
type holdStep struct {
	at int
	do func()
}

// holdSteps parks release on n hops and runs each step at its offset (steps in nondecreasing order of at); a step at 0 runs
// at once. It reports whether it parked release.
func (h *handler) holdSteps(n int, steps []holdStep, release func()) bool {
	at := 0
	for len(steps) > 0 && steps[0].at <= at {
		steps[0].do()
		steps = steps[1:]
	}
	if len(steps) == 0 {
		return h.holdFor(n-at, release)
	}
	next := steps[0].at
	var run func()
	run = func() {
		at = next
		for len(steps) > 0 && steps[0].at <= at {
			steps[0].do()
			steps = steps[1:]
		}
		if len(steps) == 0 {
			if !h.holdFor(n-at, release) {
				release()
			}
			return
		}
		next = steps[0].at
		if !h.holdFor(next-at, run) {
			run()
		}
	}
	return h.holdFor(next, run)
}

// flushHeld emits the statements of a line job whose completion nothing parked (a job without a hold), in commit order.
//
//go:noinline
func (h *handler) flushHeld() {
	h.flush(h.held)
	h.held = nil
}

// flush emits statements in this step, in commit order.
func (h *handler) flush(commits []abi.Commit) {
	for _, c := range commits {
		h.b.Commit(c.Seq, c.Stmts)
	}
}

// holdFor parks release on a hops chain of n hops (effect 14) and reports whether it did.
func (h *handler) holdFor(n int, release func()) bool {
	if n <= 0 {
		return false
	}
	return h.holdChain(abi.Hops{N: uint32(n)}, release)
}

// holdChain parks release on the hops chain c names: the host runs c.N hops, applies c.Commits after hop c.At, and sends
// hopped after the last. A payload over abi.ChunkSize (a 20,000-task commit's is 7 MB) goes out in blocks.
func (h *handler) holdChain(c abi.Hops, release func()) bool {
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
		abi.AppendHopsChunks(&payload, c)
		id = h.b.EffectChunksBare(abi.EffectHops, payload)
	}
	due := 0
	if h.capsql() != nil {
		due = h.vnow + int(c.N)
	}
	origin := h.origin()
	h.holdOrigin(origin)
	h.hops = append(h.hops, parkedHops{id, due, release, h.stepNo, origin})
	return true
}

// parkedHops is the work parked on one open hops chain.
type parkedHops struct {
	id      uint32
	due     int // the virtual tick the chain ends (capsql.go); zero without a statement queue
	release func()
	step    uint64 // the step that opened the chain: a rejected step takes its chains back (dropStepHops)
	origin  uint32 // the host async context of the code that parked the work: it continues in it (origin.go)
}

// onHopped runs the work parked on a hops chain (ABI section 5, event 18).
//
//go:noinline
func (h *handler) onHopped(ev *abi.Event) error {
	for i, p := range h.hops {
		if p.id == ev.ID {
			h.hops = slices.Delete(h.hops, i, i+1)
			if h.q != nil {
				h.vnow = max(h.vnow, p.due)
			}
			h.setOrigin(p.origin)
			p.release()
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
