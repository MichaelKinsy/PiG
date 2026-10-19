package sdk

import (
	"bytes"
	"sync"
	"weak"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// eventArgsMemo decodes an event payload once for the fused extensions of one process and hands every handler its own copy. Fused extensions (D31) run in the pig process and receive the same payload bytes, so without it K handlers of one turn_end each decoded the same args. The decoded master is never handed out: each caller gets cloneStreamValue(master), so a handler may change or keep its value and no other handler, extension or later event sees it (D20, D31).
//
// Lifetime: an entry is held strongly only while a handler that decoded it is running (pin until release). Otherwise the memo keeps weak pointers, so the collector frees an idle entry at its next cycle and the memo retains nothing between dispatches. An entry the collector freed between two handlers of one dispatch is decoded again. The memo holds its own copy of the bytes it compares, so a caller reusing its buffer cannot change an entry.
type eventArgsMemo struct {
	mu      sync.Mutex
	entries [eventArgsMemoSize]weak.Pointer[eventArgsMemoEntry]
	next    int
	// pinned counts the running handlers of each entry; a pinned entry is not collected.
	pinned map[*eventArgsMemoEntry]int
	// hits and misses count decodes served from an entry and decodes that built one.
	hits, misses uint64
}

// eventArgsMemoSize: a turn carries at most two distinct large payloads (before_agent_start and turn_end).
const eventArgsMemoSize = 2

type eventArgsMemoEntry struct {
	args   []byte
	master map[string]any
	isMap  bool
}

// fusedEventArgs is the memo of the fused extensions of this process. Extensions in their own process decode their args directly.
var fusedEventArgs eventArgsMemo

// decode returns what `var data map[string]any; json.Unmarshal(args, &data)` leaves in data: a fresh value the caller owns, or nil when the payload is not an object or does not decode. The caller calls release once its handler has returned.
func (m *eventArgsMemo) decode(args []byte) (data map[string]any, release func()) {
	data, _, release = m.decodeWithMaster(args)
	return data, release
}

// decodeWithMaster is decode that also returns the decoded master, the value data was copied from. The caller only reads the master, and only until it calls release: other handlers copy it concurrently.
func (m *eventArgsMemo) decodeWithMaster(args []byte) (data, master map[string]any, release func()) {
	if len(args) == 0 {
		return nil, nil, func() {}
	}
	m.mu.Lock()
	var entry *eventArgsMemoEntry
	for i := range m.entries {
		if candidate := m.entries[i].Value(); candidate != nil && bytes.Equal(candidate.args, args) {
			entry = candidate
			m.hits++
			break
		}
	}
	if entry == nil {
		m.misses++
		m.mu.Unlock()
		entry = &eventArgsMemoEntry{args: bytes.Clone(args)}
		entry.isMap = json.Unmarshal(args, &entry.master) == nil && entry.master != nil
		m.mu.Lock()
		m.entries[m.next] = weak.Make(entry)
		m.next = (m.next + 1) % eventArgsMemoSize
	}
	if m.pinned == nil {
		m.pinned = make(map[*eventArgsMemoEntry]int)
	}
	m.pinned[entry]++
	m.mu.Unlock()
	release = func() {
		m.mu.Lock()
		if m.pinned[entry]--; m.pinned[entry] == 0 {
			delete(m.pinned, entry)
		}
		m.mu.Unlock()
	}
	if !entry.isMap {
		return nil, nil, release
	}
	out, _ := cloneStreamValue(entry.master).(map[string]any)
	return out, entry.master, release
}

// held reports how many entries the collector has not freed.
func (m *eventArgsMemo) held() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for i := range m.entries {
		if m.entries[i].Value() != nil {
			n++
		}
	}
	return n
}
