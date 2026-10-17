package session_test

import (
	"github.com/MichaelKinsy/PiG/durable/core/internal/delta"
	"github.com/MichaelKinsy/PiG/durable/core/jv"
	"github.com/MichaelKinsy/PiG/durable/core/session"
	"github.com/MichaelKinsy/PiG/durable/core/session/sessiontest"
)

func errorMessage(err error) string { return err.Error() }

func idNum(id int64) float64 { return float64(id) }

func pointValue(p session.Point) any {
	if p.Current {
		return "current"
	}
	return idNum(p.Seq)
}

func contentValue(w session.Write) *jv.Object {
	if w.Base {
		return obj("version", idNum(int64(w.Version)), "kind", "base", "value", w.Value)
	}
	return obj("version", idNum(int64(w.Version)), "kind", "delta", "ops", delta.OpsValue(w.Ops))
}

// writeValue is the JSON shape of a pi-durable StorageWrite.
func writeValue(w session.Write) *jv.Object {
	switch w.Kind {
	case session.WriteConversation:
		return obj("type", "conversation", "value", w.Record)
	case session.WriteEntry:
		return obj("type", "entry", "value", w.Record)
	case session.WriteTask:
		return obj("type", "task", "value", w.Record)
	case session.WriteSubmission:
		return obj("type", "submission", "value", w.Record)
	case session.WriteDocCreate:
		return obj("type", "document.create", "record", w.Record, "content", contentValue(w))
	case session.WriteDocCopy:
		return obj("type", "document.copy", "record", w.Record, "source", obj("id", idNum(w.Source.ID), "at", pointValue(w.Source.At)))
	case session.WriteDocChange:
		return obj("type", "document.change", "id", idNum(w.ID), "content", contentValue(w))
	}
	return obj("type", "document.retire", "id", idNum(w.ID))
}

func commitValue(store *sessiontest.MemStore, pub *session.Publication) any {
	if pub == nil {
		return nil
	}
	c := store.Commits[len(store.Commits)-1]
	writes := make([]any, len(c.Writes))
	for i, w := range c.Writes {
		writes[i] = writeValue(w)
	}
	return obj("seq", idNum(c.Seq), "writes", writes)
}

func pubValue(pub *session.Publication) any {
	if pub == nil {
		return nil
	}
	changes := make([]any, len(pub.Changes))
	for i, c := range pub.Changes {
		switch c.Kind {
		case session.ChangeTable:
			changes[i] = writeValue(session.Write{Kind: c.Table, Record: c.Record})
		case session.ChangeDocumentCopy:
			o := obj("type", "document.copy", "record", c.Record)
			o.Set("conversationId", idNum(c.ConversationID))
			o.Set("source", obj("id", idNum(c.Source.ID), "at", pointValue(c.Source.At)))
			changes[i] = o
		default:
			o := obj("type", "document", "record", c.Record)
			if c.ConversationID != 0 {
				o.Set("conversationId", idNum(c.ConversationID))
			}
			if c.Value != nil {
				o.Set("version", idNum(int64(c.Version)))
				o.Set("value", c.Value)
			} else {
				o.Set("value", nil)
			}
			ops := delta.OpsValue(c.Ops)
			o.Set("ops", ops)
			changes[i] = o
		}
	}
	return obj("seq", idNum(pub.Seq), "changes", changes)
}
