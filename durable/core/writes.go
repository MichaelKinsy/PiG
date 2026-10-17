// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package core

import (
	"slices"

	"github.com/MichaelKinsy/PiG/durable/core/internal/delta"
	"github.com/MichaelKinsy/PiG/durable/core/jv"
	"github.com/MichaelKinsy/PiG/durable/core/rec"
	"github.com/MichaelKinsy/PiG/durable/core/session"
	"github.com/MichaelKinsy/PiG/durable/core/store"
)

// recordText returns the JSON text of a record in an array of its own length. jv.Append grows from nil by doubling, which
// left up to half of each text's array unused, and a few smaller arrays for the collector, for every row a commit wrote.
// The store's statement and the mirror share the text of an entry, task or submission row (store.Tx.RecordText, mirror.Commit).
func recordText(scratch *[]byte, o *jv.Object) []byte {
	*scratch = jv.Append((*scratch)[:0], o)
	return slices.Clone(*scratch)
}

// stageWrite adds one Session write to a store commit, as SqliteStorage's applyTableWrite and applyDocumentActions take
// pi-durable's StorageWrite. A document copy arrives resolved to a create with its source's content (mirror.resolveCopies).
// An entry, task or submission row's record text stays in the staged statement, where the mirror reads it as its stored
// text (store.Tx.RecordText).
//
//go:noinline
func stageWrite(tx *store.Tx, w *session.Write, scratch *[]byte) error {
	switch w.Kind {
	case session.WriteConversation:
		c, err := rec.ConversationOf(w.Record)
		if err != nil {
			return err
		}
		tx.Conversation(c)
	case session.WriteEntry:
		var head int64
		if v, ok := w.Record.Get("head"); ok {
			if f, isNum := v.(float64); isNum {
				head = int64(f)
			}
		}
		tx.Entry(int64(numOf(w.Record, "id")), int64(numOf(w.Record, "conversationId")), head, recordText(scratch, w.Record))
	case session.WriteTask:
		t, err := rec.TaskOf(w.Record)
		if err != nil {
			return err
		}
		tx.Task(t, recordText(scratch, w.Record))
	case session.WriteSubmission:
		sub, err := rec.SubmissionOf(w.Record)
		if err != nil {
			return err
		}
		tx.Submission(sub, recordText(scratch, w.Record))
	case session.WriteDocCreate, session.WriteDocCopy:
		d, err := rec.DocumentOf(w.Record)
		if err != nil {
			return err
		}
		var base any
		if w.Value != nil {
			base = w.Value
		}
		tx.DocumentCreate(d, w.Version, base)
	case session.WriteDocChange:
		if w.Base {
			tx.DocumentBase(w.ID, w.Version, w.Value)
		} else {
			tx.DocumentDelta(w.ID, w.Version, jv.Append(nil, delta.OpsValue(w.Ops)))
		}
	case session.WriteDocRetire:
		tx.DocumentRetire(w.ID)
	}
	return nil
}

func numOf(o *jv.Object, k string) float64 {
	if o == nil {
		return 0
	}
	v, _ := o.Get(k)
	f, _ := v.(float64)
	return f
}

func strOf(o *jv.Object, k string) string {
	if o == nil {
		return ""
	}
	v, _ := o.Get(k)
	s, _ := v.(string)
	return s
}

func objOf(o *jv.Object, k string) *jv.Object {
	if o == nil {
		return nil
	}
	v, _ := o.Get(k)
	r, _ := v.(*jv.Object)
	return r
}

func boolOf(o *jv.Object, k string) (value, present bool) {
	if o == nil {
		return false, false
	}
	v, ok := o.Get(k)
	b, isBool := v.(bool)
	return b, ok && isBool
}

func has(o *jv.Object, k string) bool { return o != nil && o.Has(k) }
