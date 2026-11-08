package codingagent

import (
	"sync"

	"github.com/MichaelKinsy/PiG/internal/sessionentry"
)

// messageEntryLimit bounds how many persisted messages a Session remembers; an extension process that has not asked for a pair this old no longer needs it.
const messageEntryLimit = 256

// messageEntryLog is the newest sessionentry.MessageSeq values in the order they were noted, in a ring of messageEntryLimit slots that fills as pairs arrive (a Session that notes none holds none). noted counts every pair ever noted, so a reader's cursor survives the oldest pairs dropping off, and pair n is in slot n % messageEntryLimit while it is held.
type messageEntryLog struct {
	mu    sync.Mutex
	ring  []sessionentry.MessageSeq
	noted int
}

// NoteMessageEntry records that the entry id persisted the message numbered seq, the sequence its message_end carried to the extension processes (agent-session.ts appendMessage stores the message object itself; a process holds a copy and needs the number to know which of its payloads that object was). A message no extension process was sent a number for (seq 0) has nothing to name.
func (s *Session) NoteMessageEntry(id string, seq uint64) {
	if seq == 0 {
		return
	}
	id = sessionentry.WireID(id)
	if id == "" {
		return
	}
	log := &s.messageEntries
	log.mu.Lock()
	defer log.mu.Unlock()
	pair := sessionentry.MessageSeq{ID: id, Seq: seq}
	if len(log.ring) < messageEntryLimit {
		log.ring = append(log.ring, pair)
	} else {
		log.ring[log.noted%messageEntryLimit] = pair
	}
	log.noted++
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
	cursor = max(cursor, log.noted-len(log.ring))
	pairs := make([]sessionentry.MessageSeq, log.noted-cursor)
	for i := range pairs {
		pairs[i] = log.ring[(cursor+i)%messageEntryLimit]
	}
	return pairs, log.noted
}
