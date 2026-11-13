// SPDX-License-Identifier: MIT

// Package sharedtext keeps one copy of a large text that the process holds more than once. A tool that reads the same file again, lists
// the same directory again or runs the same search again returns a text equal to one the conversation already holds; Share hands back
// the string that is already held, so the new copy is garbage at once and the conversation holds the text one time however often it
// was produced. Pi keeps a JavaScript string per result (read.ts returns a new string for each read); a Go string never changes, so which
// of two equal strings a message holds shows nowhere but in the memory the process keeps.
//
// The table refers to a text weakly: it keeps no text alive, and a text nothing else holds is collected and its entry dropped. It is
// independent of the request caches (ai.ReleaseRequestCaches), so an idle session that lets its encodings go still shares its texts.
package sharedtext

import (
	"hash/maphash"
	"sync"
	"unsafe"
	"weak"
)

// MinBytes is the smallest text worth an entry: below it the copy costs less than the lookup saves.
const MinBytes = 4 << 10

// minSweep is the table size under which dead entries are left in place.
const minSweep = 64

type entry struct {
	// data is the first byte of the text, held weakly; size is its length.
	data weak.Pointer[byte]
	size int
}

type table struct {
	seed    maphash.Seed
	mu      sync.Mutex
	entries map[uint64]entry
	// sweepAt is the number of entries at which the next call drops those whose text was collected.
	sweepAt int
}

var shared = newTable()

func newTable() *table {
	return &table{seed: maphash.MakeSeed(), entries: map[uint64]entry{}, sweepAt: minSweep}
}

// Share returns the string equal to text that an earlier call returned and that is still held somewhere, or text itself, which later
// calls with an equal text then return. A text shorter than MinBytes is returned as it is. text must be memory the Go runtime manages
// (a string the program built), which is what a tool's output is.
func Share(text string) string { return shared.share(text) }

func (t *table) share(text string) string {
	if len(text) < MinBytes {
		return text
	}
	hash := maphash.String(t.seed, text)
	t.mu.Lock()
	defer t.mu.Unlock()
	if held, ok := t.entries[hash]; ok && held.size == len(text) {
		if data := held.data.Value(); data != nil {
			//nolint:gosec // G103: the entry was made from a string of this length at this address, the weak pointer returned it alive, and a Go string never changes; the bytes are compared before the string is used.
			if earlier := unsafe.String(data, held.size); earlier == text {
				return earlier
			}
		}
	}
	// A different text with the same hash, or one whose earlier copy was collected, takes the entry: the table shares the latest.
	//nolint:gosec // G103: the address identifies the text's bytes for a weak reference; nothing is read or written through it here.
	t.entries[hash] = entry{data: weak.Make(unsafe.StringData(text)), size: len(text)}
	if len(t.entries) >= t.sweepAt {
		for key, held := range t.entries {
			if held.data.Value() == nil {
				delete(t.entries, key)
			}
		}
		t.sweepAt = max(minSweep, 2*len(t.entries))
	}
	return text
}
