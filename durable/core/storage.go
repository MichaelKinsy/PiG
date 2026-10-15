// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package core

import (
	"errors"

	"github.com/MichaelKinsy/PiG/durable/core/internal/delta"
	"github.com/MichaelKinsy/PiG/durable/core/jv"
	"github.com/MichaelKinsy/PiG/durable/core/session"
)

// maxSafeID is Number.MAX_SAFE_INTEGER, the last ID mintId may return.
const maxSafeID = session.ID(1)<<53 - 1

// storageAPI serves the core's store as pi-durable's Storage interface (CONTRACT 3.10, the SC row): the api ops
// "storage.<method>" of an open in mode "storage". A commit is the same adapter commit the Session uses: Pi's statements
// through store.Commit, then the mirror.
func (h *handler) storageAPI(method string, req *jv.Object) (any, error) {
	if h.storageClosed {
		return nil, errors.New("Storage is closed")
	}
	a := &adapter{h: h}
	m := h.mir
	at := func(v any) (session.Point, error) {
		if s, ok := v.(string); ok && s == "current" {
			return session.Current, nil
		}
		f, ok := v.(float64)
		if !ok || f != float64(int64(f)) {
			return session.Point{}, &typeError{"Invalid document point"}
		}
		return session.AsOf(session.Seq(f)), nil
	}
	id := session.ID(numOf(req, "id"))
	query := objOf(req, "query")
	if query == nil {
		query = jv.NewObject()
	}
	cursor, _ := req.Get("cursor")
	limit := int(numOf(req, "limit"))
	switch method {
	case "close":
		h.storageClosed = true
		return nil, nil
	case "mintId":
		if h.mintNext > maxSafeID {
			return nil, errors.New("ID space is exhausted")
		}
		n := h.mintNext
		h.mintNext++
		return float64(n), nil
	case "commit":
		raw, _ := req.Get("writes")
		list, _ := raw.([]any)
		writes := make([]session.Write, 0, len(list))
		for _, item := range list {
			w, err := storageWrite(item)
			if err != nil {
				return nil, err
			}
			writes = append(writes, w)
		}
		seq, err := a.Commit(writes, h.mintNext)
		if err != nil {
			return nil, err
		}
		if next := m.NextID(); next > h.mintNext {
			h.mintNext = next
		}
		return float64(seq), nil
	case "conversation":
		return orNull(m.Conversation(id))
	case "entry":
		var e *jv.Object
		var seq session.Seq
		var err error
		if has(req, "conversationId") {
			e, seq, err = m.EntryIn(session.ID(numOf(req, "conversationId")), id)
		} else {
			e, seq, err = m.Entry(id)
		}
		if err != nil || e == nil {
			return nil, err
		}
		out := jv.NewObject()
		out.Set("entry", e)
		out.Set("commitSeq", float64(seq))
		return out, nil
	case "findLatestHeadMarker":
		return orNull(m.LatestHeadMarker(session.ID(numOf(req, "conversationId")), session.ID(numOf(req, "atOrBefore"))))
	case "task":
		return orNull(m.Task(id))
	case "submission":
		return orNull(m.Submission(id))
	case "submissionByRequest":
		return orNull(m.SubmissionByRequest(session.ID(numOf(req, "conversationId")), strOf(req, "requestId")))
	case "scanConversations":
		order, after, err := scanStart(query, cursor, "ascending")
		if err != nil {
			return nil, err
		}
		p, err := m.ScanConversations(session.ConversationQuery{OwnerConversationID: session.ID(numOf(query, "ownerConversationId")),
			OwnerTaskID: session.ID(numOf(query, "ownerTaskId")), Descending: order == "descending"}, limit, after)
		return pageOrErr(p, order, err)
	case "scanEntries":
		order, after, err := scanStart(query, cursor, "descending")
		if err != nil {
			return nil, err
		}
		p, err := m.ScanEntries(session.EntryQuery{ConversationID: session.ID(numOf(query, "conversationId")),
			MinEntryID: session.ID(numOf(query, "minEntryId")), MaxEntryID: session.ID(numOf(query, "maxEntryId")),
			Ascending: order == "ascending"}, limit, after)
		return pageOrErr(p, order, err)
	case "scanTasks":
		order, after, err := scanStart(query, cursor, "ascending")
		if err != nil {
			return nil, err
		}
		p, err := m.ScanTasks(session.TaskQuery{ConversationID: session.ID(numOf(query, "conversationId")), Kind: strOf(query, "kind"),
			Status: strOf(query, "status"), AbortRequested: optBool(query, "abortRequested"), Background: optBool(query, "background"),
			Descending: order == "descending"}, limit, after)
		return pageOrErr(p, order, err)
	case "scanSubmissions":
		order, after, err := scanStart(query, cursor, "ascending")
		if err != nil {
			return nil, err
		}
		p, err := m.ScanSubmissions(session.SubmissionQuery{ConversationID: session.ID(numOf(query, "conversationId")),
			Status: strOf(query, "status"), Descending: order == "descending"}, limit, after)
		return pageOrErr(p, order, err)
	case "findDocument":
		point, err := at(valueOf(req, "at"))
		if err != nil {
			return nil, err
		}
		return orNull(m.FindDocument(storageAddress(objOf(req, "address")), point))
	case "document":
		point, err := at(valueOf(req, "at"))
		if err != nil {
			return nil, err
		}
		d, err := m.Document(id, point)
		if err != nil || d == nil {
			return nil, err
		}
		out := jv.NewObject()
		out.Set("record", d.Record)
		out.Set("version", float64(d.Version))
		out.Set("value", d.Value)
		out.Set("deltasSinceBase", float64(d.DeltasSinceBase))
		return out, nil
	case "scanDocuments":
		point, err := at(valueOf(query, "at"))
		if err != nil {
			return nil, err
		}
		var after session.ID
		if c, ok := cursor.(*jv.Object); ok {
			a, isNum := valueOf(c, "after").(float64)
			if !isNum || a != float64(int64(a)) {
				return nil, session.NewStorageRequestError("Invalid storage cursor")
			}
			after = session.ID(a)
		}
		p, err := m.ScanDocuments(storageScope(objOf(query, "scope")), point, strOf(query, "kind"), limit, after)
		return pageOrErr(p, "ascending", err)
	}
	return nil, ErrNotImplemented
}

func valueOf(o *jv.Object, k string) any {
	if o == nil {
		return nil
	}
	v, _ := o.Get(k)
	return v
}

func orNull(o *jv.Object, err error) (any, error) {
	if err != nil || o == nil {
		return nil, err
	}
	return o, nil
}

func pageOrErr(p session.Page, order string, err error) (any, error) {
	if err != nil {
		return nil, err
	}
	return page(p, order), nil
}

func storageScope(s *jv.Object) session.Scope {
	switch strOf(s, "kind") {
	case "conversation":
		return session.Scope{Kind: session.ScopeConversation, Owner: session.ID(numOf(s, "conversationId"))}
	case "task":
		return session.Scope{Kind: session.ScopeTask, Owner: session.ID(numOf(s, "taskId"))}
	}
	return session.Scope{Kind: session.ScopeSession}
}

// storageAddress is a DocumentAddress {kind, scope, key?}.
func storageAddress(a *jv.Object) session.Address {
	addr := session.Address{Kind: strOf(a, "kind"), Scope: storageScope(objOf(a, "scope"))}
	if k, ok := a.Get("key"); ok {
		addr.Key, _ = k.(string)
		addr.Family = true
	}
	return addr
}

// storageWrite converts one StorageWrite (types.ts) to the Session's write.
func storageWrite(item any) (session.Write, error) {
	o, ok := item.(*jv.Object)
	if !ok {
		return session.Write{}, &typeError{"Invalid storage write"}
	}
	content := func(c *jv.Object) (session.Write, error) {
		w := session.Write{Version: int(numOf(c, "version"))}
		switch strOf(c, "kind") {
		case "base":
			w.Base, w.Value = true, objOf(c, "value")
		case "delta":
			ops, err := delta.DecodeOps(valueOf(c, "ops"))
			if err != nil {
				return w, err
			}
			w.Ops = ops
		default:
			return w, &typeError{"Invalid document content"}
		}
		return w, nil
	}
	switch strOf(o, "type") {
	case "conversation":
		return session.Write{Kind: session.WriteConversation, Record: objOf(o, "value")}, nil
	case "entry":
		return session.Write{Kind: session.WriteEntry, Record: objOf(o, "value")}, nil
	case "task":
		return session.Write{Kind: session.WriteTask, Record: objOf(o, "value")}, nil
	case "submission":
		return session.Write{Kind: session.WriteSubmission, Record: objOf(o, "value")}, nil
	case "document.create":
		w, err := content(objOf(o, "content"))
		w.Kind, w.Record = session.WriteDocCreate, objOf(o, "record")
		return w, err
	case "document.copy":
		src := objOf(o, "source")
		point := session.Current
		if f, isNum := valueOf(src, "at").(float64); isNum {
			point = session.AsOf(session.Seq(f))
		}
		return session.Write{Kind: session.WriteDocCopy, Record: objOf(o, "record"), Source: session.DocCopySource{ID: session.ID(numOf(src, "id")), At: point}}, nil
	case "document.change":
		w, err := content(objOf(o, "content"))
		w.Kind, w.ID = session.WriteDocChange, session.ID(numOf(o, "id"))
		return w, err
	case "document.retire":
		return session.Write{Kind: session.WriteDocRetire, ID: session.ID(numOf(o, "id"))}, nil
	}
	return session.Write{}, &typeError{"Invalid storage write"}
}
