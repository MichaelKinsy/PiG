package session

import (
	"slices"

	"github.com/MichaelKinsy/PiG/durable/core/jv"
)

// The current documents of the live tasks the Session created (session.ts #taskDocuments): the Session wrote every one of them, so a
// task that ends retires them without a scan of Storage.

// createdTasks is the IDs of the tasks the transaction creates, in the order it staged them (transaction.ts createdTaskIds).
func (tx *Tx) createdTasks() []ID {
	var ids []ID
	for _, id := range tx.taskOrder {
		if t := tx.tasks[id]; t.write != nil && t.writeIsNew {
			ids = append(ids, id)
		}
	}
	return ids
}

// TaskDocumentsSize is how many live tasks have their documents indexed (session.ts documentCacheSizes.taskDocuments).
func (s *Session) TaskDocumentsSize() int { return len(s.taskDocuments) }

// TaskDocuments is the current documents of a live task the Session created, ascending by ID; known is false for any other task,
// whose documents are scanned.
func (s *Session) TaskDocuments(taskID ID) (records []*jv.Object, known bool) {
	docs, known := s.taskDocuments[taskID]
	if !known {
		return nil, false
	}
	records = make([]*jv.Object, 0, len(docs))
	for _, rec := range docs {
		records = append(records, rec)
	}
	slices.SortFunc(records, func(a, b *jv.Object) int { return int(mustInt(a, "id") - mustInt(b, "id")) })
	return records, true
}

// indexTaskDocuments keeps the index in step with a stored commit: the tasks it creates start with no documents, task documents it
// creates join their task, ones it retires leave, and tasks it ends leave the index (session.ts #indexTaskDocuments).
func (s *Session) indexTaskDocuments(pub *Publication) {
	for _, id := range pub.Created {
		if s.taskDocuments == nil {
			s.taskDocuments = map[ID]map[ID]*jv.Object{}
		}
		s.taskDocuments[id] = nil // the task's map is made with its first document
	}
	if len(s.taskDocuments) == 0 {
		return
	}
	changes := pub.StoredChanges()
	for i := range changes {
		c := &changes[i]
		if c.Kind != ChangeDocument {
			continue
		}
		scope := getObj(c.Record, "scope")
		if kind, _ := getStr(scope, "kind"); kind != "task" {
			continue
		}
		taskID := mustInt(scope, "taskId")
		known, indexed := s.taskDocuments[taskID]
		if !indexed {
			continue
		}
		id := mustInt(c.Record, "id")
		if c.Value == nil {
			delete(known, id)
		} else if mustInt(c.Record, "createdAt") == pub.Seq {
			if known == nil {
				known = map[ID]*jv.Object{}
				s.taskDocuments[taskID] = known
			}
			known[id] = c.Record
		}
	}
	for i := range changes {
		if c := &changes[i]; c.Kind == ChangeTable && c.Table == WriteTask && taskStatus(c.Record) == StatusTerminal {
			delete(s.taskDocuments, mustInt(c.Record, "id"))
		}
	}
}
