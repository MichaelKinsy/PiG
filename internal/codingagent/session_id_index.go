package codingagent

import "hash/maphash"

// idIndex maps an entry id to its newest slot (session-manager.ts byId) without a key of its own: the table holds slot indexes, plus one,
// and a lookup compares the id with the slot's. A history of many entries costs four bytes per table cell, at most three quarters full,
// where a map[string]int kept a key and a value per entry.
type idIndex struct {
	seed  maphash.Seed
	table []int32
	n     int
}

// idIndexMinTable is the size of an index's first table.
// pig additive (D112): Pi's byId is a Map with no table to size; the first table's size changes how often the index grows, never what a lookup returns.
const idIndexMinTable = 16

// find returns the slot of id in slots, and false when no slot has it.
func (x *idIndex) find(slots []entrySlot, id string) (int, bool) {
	if x.n == 0 {
		return 0, false
	}
	mask := uint64(len(x.table) - 1)
	for h := maphash.String(x.seed, id) & mask; ; h = (h + 1) & mask {
		v := x.table[h]
		if v == 0 {
			return 0, false
		}
		if slots[v-1].id == id {
			return int(v - 1), true
		}
	}
}

// set makes idx, a slot of slots, the slot of its id.
func (x *idIndex) set(slots []entrySlot, idx int) {
	if 4*(x.n+1) > 3*len(x.table) {
		x.grow(slots)
	}
	x.put(slots, slots[idx].id, idx)
}

func (x *idIndex) put(slots []entrySlot, id string, idx int) {
	mask := uint64(len(x.table) - 1)
	for h := maphash.String(x.seed, id) & mask; ; h = (h + 1) & mask {
		v := x.table[h]
		if v == 0 {
			x.table[h] = int32(idx + 1)
			x.n++
			return
		}
		if slots[v-1].id == id {
			x.table[h] = int32(idx + 1)
			return
		}
	}
}

func (x *idIndex) grow(slots []entrySlot) {
	if x.table == nil {
		x.seed = maphash.MakeSeed()
	}
	old := x.table
	x.table = make([]int32, max(idIndexMinTable, 2*len(old)))
	x.n = 0
	for _, v := range old {
		if v != 0 {
			x.put(slots, slots[v-1].id, int(v-1))
		}
	}
}

// slotOf is the newest slot whose entry has id, and false when there is none. The caller holds s.mu.
func (s *Session) slotOf(id string) (int, bool) { return s.byID.find(s.slots, id) }

// slotOrZero is slotOf's slot, 0 when there is none, as a read of the map it replaces returned.
func (s *Session) slotOrZero(id string) int {
	idx, _ := s.slotOf(id)
	return idx
}
