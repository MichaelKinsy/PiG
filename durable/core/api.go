// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package core

import (
	"errors"
	"strings"

	"github.com/MichaelKinsy/PiG/durable/core/abi"
	"github.com/MichaelKinsy/PiG/durable/core/docs"
	"github.com/MichaelKinsy/PiG/durable/core/jv"
	"github.com/MichaelKinsy/PiG/durable/core/session"
	"github.com/MichaelKinsy/PiG/durable/core/session/hopcost"
	"github.com/MichaelKinsy/PiG/durable/core/turn"
)

func (h *handler) apiOK(id uint32, value any) {
	if c, ok := value.(contextAnswer); ok {
		value = rawJSON(c.json())
	}
	out := append(make([]byte, 0, 64), `{"ok":`...)
	if raw, ok := value.(rawJSON); ok {
		out = append(out, raw...)
	} else {
		out = jv.Append(out, value)
	}
	h.notice(abi.NoticeAPIResult, id, append(out, '}'))
}

func (h *handler) apiError(id uint32, err error) {
	out := append(make([]byte, 0, 64), `{"error":`...)
	out = append(out, errorJSON(err)...)
	h.notice(abi.NoticeAPIResult, id, append(out, '}'))
}

// onAPI answers the committed-state reads of ABI section 8.1 that the Session layer serves. Every answer is an
// api_result notice carrying the request ID; an absent value is null.
//
//go:noinline
func (h *handler) onAPI(ev *abi.Event) error {
	v, err := jv.Parse(ev.Payload)
	if err != nil {
		return err
	}
	req, ok := v.(*jv.Object)
	if !ok {
		return &Rejection{"TypeError", "api: the request is not an object"}
	}
	if strOf(req, "op") == "opened" {
		// Harness.open completes when the scheduler's open commit did: it waits for the host's Storage when it gates commits.
		if h.openDone {
			h.answerOpened(ev.ID)
		} else {
			h.openWaiters = append(h.openWaiters, ev.ID)
		}
		return nil
	}
	if h.apiTasks(ev.ID, strOf(req, "op"), req) {
		return nil
	}
	if strOf(req, "op") == "agent" {
		// ABI section 8.1 `agent`: the agent the host resolves from pi.agent and the registry; answered when the hook returns.
		conv := turn.ID(numOf(req, "conversationId"))
		var state *jv.Object
		h.loop.Line(func() error {
			var err error
			state, err = h.sess.Snapshot(docs.AgentDef, session.DocArgs{Owner: conv})
			return err
		}, func(err error) {
			if err != nil {
				h.apiError(ev.ID, err)
				return
			}
			h.requestAgentRaw(state, conv, nil, func(agent any, err error) {
				if err != nil {
					h.apiError(ev.ID, err)
					return
				}
				h.apiOK(ev.ID, agent)
			})
		})
		return nil
	}
	if strOf(req, "op") == "watch.open" && h.loop != nil && strOf(objOf(req, "target"), "kind") != "" {
		// Opening a watch is a job on the Session line (session.ts watchDoc, view.ts and task-graph.ts readOnLine): it takes
		// its turn behind the jobs already queued, so a step that ends the caller's invocation runs before it.
		var value any
		// ConversationViews.attach is Session.readOnLine over the Storage reads of #build (view.ts :135-175): its caller awaits the run
		// promise, so the answer of a view leaves a hop before the line frees.
		run := func() error {
			if strOf(objOf(req, "target"), "kind") == "view" {
				h.job.readLine = true
			}
			var err error
			value, err = h.api("watch.open", req)
			return err
		}
		done := func(err error) {
			if err != nil {
				h.apiError(ev.ID, err)
				return
			}
			h.apiOK(ev.ID, value)
		}
		if strOf(objOf(req, "target"), "kind") == "view" {
			h.loop.ClientReadLine(run, done)
			return nil
		}
		h.loop.Line(run, done)
		return nil
	}
	if strOf(req, "op") == "context" && h.runtimeContext(ev.ID, req) {
		return nil
	}
	if op := strOf(req, "op"); h.loop != nil && h.onLine(op, req) {
		// Pi serves these through Session.readOnLine (harness.ts conversation and task, context.ts readContextFrom,
		// task-graph.ts) or a snapshot that misses the cache: a job on the Session line, which waits behind the queued
		// commits and holds the line for its reads.
		var value any
		h.loop.ClientReadLine(func() error {
			var err error
			h.job.readLine = true
			// A read that returns the Storage promise itself (readOnLine(() => storage.submission(id)): status, get) settles one tick earlier than
			// Submissions.wait's async arrow, which awaits it (submissions.ts :63-86).
			h.job.direct = op == "conversation" || op == "getTask" || op == "entry" || op == "scanEntries" || (op == "submission" && !req.Has("wait"))
			if op == "view.attach" {
				value, err = h.viewAttach(req)
			} else {
				value, err = h.api(op, req)
			}
			return err
		}, func(err error) {
			if err != nil {
				h.apiError(ev.ID, err)
				return
			}
			h.apiOK(ev.ID, value)
		})
		return nil
	}
	value, err := h.api(strOf(req, "op"), req)
	if errors.Is(err, ErrNotImplemented) {
		return err
	}
	h.answerAfterReads(func() {
		if err != nil {
			h.apiError(ev.ID, err)
			return
		}
		h.apiOK(ev.ID, value)
	})
	return nil
}

// onLine reports whether an api read is a job on the Session line in Pi: readOnLine reads, snapshotAsOf, and a
// snapshot whose document is not loaded.
func (h *handler) onLine(op string, req *jv.Object) bool {
	switch op {
	case "conversation", "getTask", "submission", "scanEntries", "context", "entry", "taskGraph", "snapshotAsOf", "view.attach":
		return true
	case "snapshot":
		def, args, err := docDef(req)
		return err == nil && !h.sess.Loaded(def, args)
	}
	return false
}

func (h *handler) api(op string, req *jv.Object) (any, error) {
	if method, ok := strings.CutPrefix(op, "storage."); ok && h.mode == "storage" {
		return h.storageAPI(method, req)
	}
	switch op {
	case "unloadDocuments":
		// session.ts unloadDocuments: the loaded-document cache is dropped; later reads load from the store (and migrate again).
		h.sess.UnloadDocuments()
		return nil, nil
	case "snapshot":
		def, args, err := docDef(req)
		if err != nil {
			return nil, err
		}
		migrating := hostMigration(def, req)
		v, err := h.sess.Snapshot(def, args)
		if err != nil || v == nil {
			return nil, migrating.need(err)
		}
		return v, nil
	case "snapshotAsOf":
		def, args, err := docDef(req)
		if err != nil {
			return nil, err
		}
		migrating := hostMigration(def, req)
		v, err := h.sess.SnapshotAsOf(def, args, session.ID(numOf(req, "at")))
		if err != nil || v == nil {
			return nil, migrating.need(err)
		}
		return v, nil
	case "conversation":
		// readOnLine(() => storage.conversation(id)): one single-statement Storage read (harness.ts :313-316).
		h.read(hopcost.ReadRow)
		c, err := h.mir.Conversation(session.ID(numOf(req, "conversationId")))
		if err != nil || c == nil {
			return nil, err
		}
		return c, nil
	case "getTask":
		// readOnLine(() => storage.task(id)): one single-statement Storage read (harness.ts :243).
		h.read(hopcost.ReadRow)
		t, err := h.mir.Task(session.ID(numOf(req, "taskId")))
		if err != nil || t == nil {
			return nil, err
		}
		return t, nil
	case "context":
		conv := int64(numOf(req, "conversationId"))
		tail, head, found, err := h.contextBounds(conv, int64(numOf(req, "at")))
		if err != nil {
			return nil, err
		}
		return contextAnswer{conv, head, tail, found}, nil
	case "submission":
		// Submissions.status/get/wait read the record on the Session line (submissions.ts :63-86): one single-statement Storage read.
		h.read(hopcost.ReadRow)
		rec, _ := h.mir.Submission(session.ID(numOf(req, "submissionId")))
		if rec == nil {
			return nil, nil
		}
		return rec, nil
	case "scanEntries":
		// Conversation.entries is readOnLine(() => storage.scanEntries(query, limit, cursor)) (harness.ts :127-139).
		q := objOf(req, "query")
		if q == nil {
			q = jv.NewObject()
		}
		cursor, _ := req.Get("cursor")
		order, after, err := scanStart(q, cursor, "descending")
		if err != nil {
			return nil, err
		}
		h.read(hopcost.ReadScanEntries)
		items, next, err := h.mir.scanEntriesRaw(session.EntryQuery{
			ConversationID: session.ID(numOf(q, "conversationId")), MinEntryID: session.ID(numOf(q, "minEntryId")),
			MaxEntryID: session.ID(numOf(q, "maxEntryId")), Ascending: order == "ascending",
		}, int(numOf(req, "limit")), after)
		if err != nil {
			return nil, err
		}
		return rawPage(items, next, order), nil
	case "taskGraph":
		if h.graph != nil {
			return jv.Clone(h.graph.value), nil
		}
		return h.buildTaskGraph(), nil
	case "usage":
		return h.usage()
	case "commits":
		h.changes, _ = boolOf(req, "on")
		return nil, nil
	case "watch.open":
		return h.watchOpen(req)
	case "watch.close":
		return h.watchClose(req)
	case "entry":
		// readOnLine(() => storage.entry(conversationId, id, context)) (scheduler.ts :1216-1228): the conversation's row, then the entry.
		h.read(hopcost.ReadEntryIn)
		e, _, err := h.mir.EntryIn(session.ID(numOf(req, "conversationId")), session.ID(numOf(req, "entryId")))
		if err != nil || e == nil {
			return nil, err
		}
		return e, nil
	}
	return nil, ErrNotImplemented
}

// migration is the host-run migrate of one read-only document access: the definition's migrate(value, from) is host code, so
// the first attempt fails with a NeedMigrate rejection carrying {value, from} and the host repeats the request with `migrated`.
type migration struct {
	value *jv.Object
	from  int
	asked bool
}

func hostMigration(def *session.DocDef, req *jv.Object) *migration {
	m := &migration{}
	if d := objOf(req, "def"); d == nil || !boolIn(d, "migrates") {
		return m // the definition has no migrate: the kernel reports Pi's "requires migration" error
	}
	migrated := objOf(req, "migrated")
	def.HostMigrate = func(value *jv.Object, from int) (*jv.Object, error) {
		if migrated == nil {
			m.value, m.from, m.asked = value, from, true
			return nil, needHost("migrate")
		}
		return migrated, nil
	}
	return m
}

// need turns the kernel's NeedRead of a host migration into the rejection the host answers; any other error passes through.
func (m *migration) need(err error) error {
	if _, ok := session.IsNeedRead(err); !ok || !m.asked {
		return err
	}
	detail := jv.NewObject()
	detail.Set("value", m.value)
	detail.Set("from", float64(m.from))
	return &Rejection{"NeedMigrate", string(jv.Append(nil, detail))}
}

func boolIn(o *jv.Object, key string) bool {
	b, _ := boolOf(o, key)
	return b
}
