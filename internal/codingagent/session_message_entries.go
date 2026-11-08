package codingagent

import (
	"sync"

	"github.com/MichaelKinsy/PiG/internal/sessionentry"
)

// messageEntryLimit bounds how many persisted messages a Session remembers; an extension process that has not asked for a pair this old no longer needs it.
const messageEntryLimit = 256

// messageEntryLog is the newest sessionentry.MessageSeq values in the order they were noted. noted counts every pair ever noted, so a reader's cursor survives the oldest pairs dropping off.
type messageEntryLog struct {
	mu      sync.Mutex
	entries []sessionentry.MessageSeq
	noted   int
}

// NoteMessageEntry records that the entry id persisted the message numbered seq, the sequence its message_end carried to the extension processes (agent-session.ts appendMessage stores the message object itself; a process holds a copy and needs the number to know which of its payloads that object was).
func (s *Session) NoteMessageEntry(id string, seq uint64) {
	id = sessionentry.WireID(id)
	if id == "" || seq == 0 {
		return
	}
	log := &s.messageEntries
	log.mu.Lock()
	defer log.mu.Unlock()
	log.entries = append(log.entries, sessionentry.MessageSeq{ID: id, Seq: seq})
	log.noted++
	if len(log.entries) > messageEntryLimit {
		kept := make([]sessionentry.MessageSeq, messageEntryLimit)
		copy(kept, log.entries[len(log.entries)-messageEntryLimit:])
		log.entries = kept
	}
}

// MessageEntriesAfter returns the pairs noted since a reader's cursor and the cursor after them. A cursor beyond the pairs noted so far (a reader of an earlier session) starts again from the pairs held.
func (s *Session) MessageEntriesAfter(cursor int) ([]sessionentry.MessageSeq, int) {
	log := &s.messageEntries
	log.mu.Lock()
	defer log.mu.Unlock()
	if cursor < 0 || cursor > log.noted {
		cursor = 0
	}
	if cursor == log.noted {
		return nil, cursor
	}
	first := log.noted - len(log.entries)
	if cursor < first {
		cursor = first
	}
	return append([]sessionentry.MessageSeq(nil), log.entries[cursor-first:]...), log.noted
}
