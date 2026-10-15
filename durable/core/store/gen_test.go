// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package store_test

import (
	"math/rand/v2"

	"github.com/MichaelKinsy/PiG/durable/core/jv"
)

func convW(id int64, parentConv, parentAt int64, ownerConv, ownerTask int64) any {
	v := J("id", num(id))
	if parentConv != 0 {
		v.Set("parent", J("conversationId", num(parentConv), "at", num(parentAt)))
	}
	if ownerConv != 0 {
		v.Set("owner", J("conversationId", num(ownerConv), "taskId", num(ownerTask)))
	}
	return J("type", "conversation", "value", v)
}

func entryW(id, conv int64, kind string, extra ...any) any {
	v := J("id", num(id), "conversationId", num(conv), "kind", kind)
	for i := 0; i+1 < len(extra); i += 2 {
		v.Set(extra[i].(string), extra[i+1])
	}
	return J("type", "entry", "value", v)
}

func taskW(id, conv int64, kind, status string, extra ...any) any {
	v := J("id", num(id), "conversationId", num(conv), "kind", kind, "version", num(1), "input", J("n", num(id)),
		"background", false, "abortRequested", false, "state", J("status", status, "checkpoint", J("phase", "start")))
	for i := 0; i+1 < len(extra); i += 2 {
		v.Set(extra[i].(string), extra[i+1])
	}
	return J("type", "task", "value", v)
}

func subW(id, conv int64, status string, requestID any, extra ...any) any {
	v := J("id", num(id), "conversationId", num(conv))
	if s, ok := requestID.(string); ok {
		v.Set("requestId", s)
	}
	v.Set("type", "input")
	v.Set("status", status)
	for i := 0; i+1 < len(extra); i += 2 {
		v.Set(extra[i].(string), extra[i+1])
	}
	return J("type", "submission", "value", v)
}

func scopeJ(kind string, owner int64) *jv.Object {
	switch kind {
	case "conversation":
		return J("kind", kind, "conversationId", num(owner))
	case "task":
		return J("kind", kind, "taskId", num(owner))
	}
	return J("kind", kind)
}

func docRecord(id int64, kind string, key any, scope string, owner int64, history, fork string) *jv.Object {
	v := J("id", num(id), "kind", kind)
	if s, ok := key.(string); ok {
		v.Set("key", s)
	}
	v.Set("scope", scopeJ(scope, owner))
	if history != "" {
		v.Set("history", history)
		v.Set("fork", fork)
	}
	return v
}

func docCreateW(rec *jv.Object, version int, value *jv.Object) any {
	return J("type", "document.create", "record", rec, "content", J("version", num(int64(version)), "kind", "base", "value", value))
}

func docCopyW(rec *jv.Object, source int64, at any) any {
	return J("type", "document.copy", "record", rec, "source", J("id", num(source), "at", at))
}

func docBaseW(id int64, version int, value *jv.Object) any {
	return J("type", "document.change", "id", num(id), "content", J("version", num(int64(version)), "kind", "base", "value", value))
}

func docDeltaW(id int64, version int, ops ...any) any {
	return J("type", "document.change", "id", num(id), "content", J("version", num(int64(version)), "kind", "delta", "ops", ops))
}

func docRetireW(id int64) any { return J("type", "document.retire", "id", num(id)) }

func opSet(key string, v any) any         { return []any{"s", []any{key}, v} }
func opAppend(key, s string) any          { return []any{"a", []any{key}, s} }
func docValue(n int, s string) *jv.Object { return J("n", num(int64(n)), "s", s) }

// basicScript exercises every StorageWrite type once, in the order a first turn does.
func basicScript() *commitScript {
	s := &commitScript{}
	// Commit 1: the root conversation (reserved ID 1), a session document, and a conversation document.
	s.add(3,
		convW(1, 0, 0, 0, 0),
		docCreateW(docRecord(2, "pi.usage", nil, "session", 0, "", ""), 1, docValue(0, "")),
		docCreateW(docRecord(3, "pi.live", nil, "conversation", 1, "latest", "initial"), 1, docValue(0, "a")),
		docCreateW(docRecord(4, "pi.agent", "key \"quoted\" \u00e9 \U0001F600", "conversation", 1, "rewindable", "asOf"), 2, docValue(1, "r")),
	)
	// Commit 2: an entry, a task and a submission; the task moves to running with startedAt.
	s.add(3,
		entryW(5, 1, "pi.user", "model", []any{J("role", "user", "content", "hi")}),
		taskW(6, 1, "pi.generation", "pending"),
		subW(7, 1, "queued", "req \u2028 1"),
		docDeltaW(3, 1, opSet("n", num(1)), opAppend("s", "bc")),
		docDeltaW(4, 2, opSet("n", num(2))),
	)
	s.add(0,
		taskW(6, 1, "pi.generation", "running", "startedAt", num(1000)),
		subW(7, 1, "placed", "req \u2028 1", "entry", num(5)),
		docBaseW(2, 1, docValue(5, "usage")),
		docBaseW(4, 2, docValue(9, "snap")),
	)
	// A head marker, a null head, a task settling with endedAt, a retired and a recreated document.
	s.add(1,
		entryW(8, 1, "pi.compaction", "head", num(5)),
		entryW(9, 1, "pi.note", "head", nil),
		taskW(6, 1, "pi.generation", "terminal", "startedAt", num(1000), "endedAt", num(2000)),
		docRetireW(3),
		docCreateW(docRecord(10, "pi.live", nil, "conversation", 1, "latest", "initial"), 1, docValue(0, "again")),
	)
	// A fork: conversation 11 with a copy of the rewindable document at the current point.
	s.add(2,
		convW(11, 1, 9, 0, 0),
		docCopyW(docRecord(12, "pi.agent", "key \"quoted\" \u00e9 \U0001F600", "conversation", 11, "rewindable", "asOf"), 4, "current"),
		docCreateW(docRecord(13, "pi.task", nil, "task", 6, "", ""), 1, docValue(0, "t")),
	)
	// Retire and recreate the same address in one commit; a task document retires with its task.
	s.add(1,
		docRetireW(10),
		docCreateW(docRecord(14, "pi.live", nil, "conversation", 1, "latest", "initial"), 1, docValue(2, "third")),
		docRetireW(13),
	)
	return s
}

// randomScript is a pseudo-random valid script of n commits: tasks and submissions move through their states, documents are
// created, changed, replaced and retired, and a fork copies documents.
func randomScript(seed uint64, n int) *commitScript {
	r := rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15))
	s := &commitScript{}
	type doc struct {
		id      int64
		kind    string
		key     any
		scope   string
		owner   int64
		history string
		fork    string
		version int
		retired bool
	}
	var (
		next    = int64(2)
		convs   = []int64{1}
		tasks   = map[int64]string{}
		taskIDs []int64
		subs    = map[int64]string{}
		subIDs  []int64
		docs    []*doc
		entries = []int64{}
	)
	texts := []string{"plain", "q\"uote", "back\\slash", "line\nbreak", "\u2028\u2029", "\u00e9\u4e2d\U0001F600", "\xed\xa0\x80lone", "tab\t"}
	text := func() string { return texts[r.IntN(len(texts))] }
	mint := func() int64 { id := next; next++; return id }
	first := true
	for i := range n {
		var writes []any
		minted := 0
		fresh := func() int64 { minted++; return mint() }
		if first {
			first = false
			writes = append(writes, convW(1, 0, 0, 0, 0))
		}
		for k := r.IntN(4) + 1; k > 0; k-- {
			switch r.IntN(10) {
			case 0, 1:
				id := fresh()
				conv := convs[r.IntN(len(convs))]
				entries = append(entries, id)
				if r.IntN(4) == 0 {
					writes = append(writes, entryW(id, conv, "pi.compaction", "head", num(entries[r.IntN(len(entries))])))
				} else {
					writes = append(writes, entryW(id, conv, "pi.user", "model", []any{J("role", "user", "content", text())}))
				}
			case 2:
				id := fresh()
				conv := convs[r.IntN(len(convs))]
				tasks[id] = "pending"
				taskIDs = append(taskIDs, id)
				writes = append(writes, taskW(id, conv, "kind "+text(), "pending"))
			case 3, 4:
				if len(taskIDs) == 0 {
					continue
				}
				id := taskIDs[r.IntN(len(taskIDs))]
				if tasks[id] == "terminal" {
					continue
				}
				states := []string{"running", "waiting", "completing", "terminal"}
				st := states[r.IntN(len(states))]
				tasks[id] = st
				extra := []any{}
				if st != "pending" {
					extra = append(extra, "startedAt", num(int64(1000+i)))
				}
				if st == "terminal" {
					extra = append(extra, "endedAt", num(int64(2000+i)))
				}
				if r.IntN(3) == 0 {
					extra = append(extra, "abortRequested", true)
				}
				w := taskW(id, 1, "kind "+text(), st, extra...)
				writes = append(writes, w)
			case 5:
				id := fresh()
				subs[id] = "queued"
				subIDs = append(subIDs, id)
				var req any
				if r.IntN(3) != 0 {
					req = text()
				}
				writes = append(writes, subW(id, convs[r.IntN(len(convs))], "queued", req))
			case 6:
				if len(subIDs) == 0 {
					continue
				}
				id := subIDs[r.IntN(len(subIDs))]
				if subs[id] == "done" {
					continue
				}
				subs[id] = "done"
				writes = append(writes, subW(id, 1, "done", text(), "entry", num(1), "answer", num(2)))
			case 7:
				// A new document, or the replacement of a retired one at the same address.
				scope, owner, hist, fork := "session", int64(0), "", ""
				switch r.IntN(3) {
				case 1:
					scope, owner, hist, fork = "conversation", convs[r.IntN(len(convs))], "latest", "initial"
					if r.IntN(2) == 0 {
						hist, fork = "rewindable", "asOf"
					}
				case 2:
					if len(taskIDs) == 0 {
						continue
					}
					scope, owner = "task", taskIDs[r.IntN(len(taskIDs))]
				}
				var key any
				if r.IntN(2) == 0 {
					key = text()
				}
				kind := []string{"pi.a", "pi.b"}[r.IntN(2)]
				live := false
				for _, d := range docs {
					if !d.retired && d.kind == kind && d.scope == scope && d.owner == owner && d.key == key {
						live = true
					}
				}
				if live {
					continue
				}
				d := &doc{id: fresh(), kind: kind, key: key, scope: scope, owner: owner, history: hist, fork: fork, version: 1 + r.IntN(2)}
				docs = append(docs, d)
				writes = append(writes, docCreateW(docRecord(d.id, kind, key, scope, owner, hist, fork), d.version, docValue(r.IntN(100), text())))
			case 8:
				var live []*doc
				for _, d := range docs {
					if !d.retired {
						live = append(live, d)
					}
				}
				if len(live) == 0 {
					continue
				}
				d := live[r.IntN(len(live))]
				touched := false
				for _, w := range writes {
					wo := w.(*jv.Object)
					switch get(wo, "type") {
					case "document.create", "document.copy":
						touched = touched || idOf(get(get(wo, "record").(*jv.Object), "id")) == d.id
					case "document.change", "document.retire":
						touched = touched || idOf(get(wo, "id")) == d.id
					}
				}
				if touched {
					continue
				}
				switch r.IntN(4) {
				case 0:
					d.version++
					writes = append(writes, docBaseW(d.id, d.version, docValue(r.IntN(100), text())))
				case 1:
					d.retired = true
					writes = append(writes, docRetireW(d.id))
				default:
					writes = append(writes, docDeltaW(d.id, d.version, opSet("n", num(int64(r.IntN(1000)))), opAppend("s", text())))
				}
			case 9:
				if r.IntN(3) != 0 || len(entries) == 0 {
					continue
				}
				// A fork of the root with copies of its conversation documents at the current point.
				id := fresh()
				convs = append(convs, id)
				writes = append(writes, convW(id, 1, entries[r.IntN(len(entries))], 0, 0))
				for _, d := range docs {
					if d.retired || d.scope != "conversation" || d.owner != 1 || r.IntN(2) == 0 {
						continue
					}
					cp := &doc{id: fresh(), kind: d.kind, key: d.key, scope: d.scope, owner: id, history: d.history, fork: d.fork, version: d.version}
					docs = append(docs, cp)
					writes = append(writes, docCopyW(docRecord(cp.id, cp.kind, cp.key, cp.scope, cp.owner, cp.history, cp.fork), d.id, "current"))
				}
			}
		}
		s.add(minted, writes...)
	}
	return s
}
