// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package history

// A conversation that lives for many turns must not keep every record's text in memory: Pi's Session keeps no per-record
// state and reads records through Storage on demand, and a core whose memory is bounded (a 96 MiB Wasm linear memory)
// would otherwise grow with the transcript. The index (entries, messages, spans) stays; the record text is the only thing
// the store lets go of. The core calls Evict when every record in the store is in storage; an evicted chunk of the arena
// is read back through the Loader the first time a record in it is read, a few at a time, so a scan over the whole
// transcript holds a few chunks.

// Loader reads the stored entries of conversation conv with ids in [lo, hi], ascending, and gives each one's id, commit
// sequence and record text to fn until it returns false. The record text is valid for the call only.
type Loader func(conv, lo, hi int64, fn func(id, seq int64, rec []byte) bool) error

const (
	// evictAbove is the record text the store holds before Evict releases any; it then keeps evictKeep.
	evictAbove = 1 << 20
	evictKeep  = 256 << 10
	// reloadKeep is the number of evicted chunks read back that a chain holds at once.
	reloadKeep = 4
)

const (
	// ErrNoLoader reports a read of an evicted record from a store that has no loader. It cannot happen: Evict does
	// nothing without one.
	ErrNoLoader = jsonError("history: evicted record read without a loader")
	// ErrReloaded reports that storage returned different records than the ones the chain indexed.
	ErrReloaded = jsonError("history: stored entries differ from the loaded ones")
)

// SetLoader sets the loader that makes Evict possible; nil makes it do nothing.
func (st *Store) SetLoader(l Loader) { st.loader = l }

// Err is the first failed read of an evicted chunk. The bytes of a record that could not be read are zero; the caller
// treats the store as unusable.
func (st *Store) Err() error { return st.loadErr }

// IndexBytes approximates the memory the fixed-width index of every chain uses.
func (st *Store) IndexBytes() int {
	n := 0
	for _, c := range st.chainList {
		n += c.IndexBytes()
	}
	return n
}

// ResidentBytes is the record text the store holds in memory.
func (st *Store) ResidentBytes() int { return st.resident }

func (c *Chain) residentBytes() int {
	n := 0
	for _, ch := range c.arena {
		n += len(ch)
	}
	return n
}

// Evict releases the text of the oldest chunks of every chain, keeping the newest evictKeep bytes, once the store holds
// more than evictAbove. The caller guarantees every record in the store is in storage: no commit it wrote is pending.
// The newest chunk of a chain, which the next append extends, is never released.
func (st *Store) Evict() {
	if st.loader == nil || st.resident < evictAbove {
		return
	}
	for _, c := range st.chainList {
		for k := 0; k+1 < len(c.arena) && st.resident > evictKeep; k++ {
			if c.arena[k] != nil {
				st.resident -= len(c.arena[k])
				c.arena[k] = nil
			}
		}
		c.reloaded = c.reloaded[:0]
	}
}

// EvictLoaded releases the text of the older chunks of conversation conv's chain, keeping the newest evictKeep bytes, when
// the chain holds more than evictAbove. The caller guarantees every record the chain holds is in storage: it is a load that
// has added only rows it read from storage.
func (st *Store) EvictLoaded(conv int64) {
	c := st.chainOf(conv, false)
	if st.loader == nil || c == nil {
		return
	}
	if resident := c.residentBytes(); resident > evictAbove {
		for k := 0; k+1 < len(c.arena) && resident > evictKeep; k++ {
			if c.arena[k] != nil {
				resident -= len(c.arena[k])
				st.resident -= len(c.arena[k])
				c.arena[k] = nil
			}
		}
		c.reloaded = c.reloaded[:0]
	}
}

// reload reads evicted chunk k back and returns it.
func (c *Chain) reload(k uint32) []byte {
	st := c.st
	first := int(c.chunkEnts[k])
	last := len(c.ents) - 1
	if int(k)+1 < len(c.chunkEnts) {
		last = int(c.chunkEnts[k+1]) - 1
	}
	chunk := make([]byte, c.ents[last].off+c.ents[last].n)
	if st.loadErr == nil {
		st.loadErr = c.fill(chunk, first, last)
	}
	c.arena[k] = chunk
	st.resident += len(chunk)
	c.reloaded = append(c.reloaded, k)
	if len(c.reloaded) > reloadKeep {
		old := c.reloaded[0]
		c.reloaded = append(c.reloaded[:0], c.reloaded[1:]...)
		st.resident -= len(c.arena[old])
		c.arena[old] = nil
	}
	return chunk
}

// fill reads entries first..last of the chain from storage into chunk at their offsets.
func (c *Chain) fill(chunk []byte, first, last int) error {
	if c.st.loader == nil {
		return ErrNoLoader
	}
	i := first
	var bad bool
	err := c.st.loader(c.Conv, c.ents[first].id, c.ents[last].id, func(id, _ int64, rec []byte) bool {
		if i > last || c.ents[i].id != id || c.ents[i].n != uint32(len(rec)) {
			bad = true
			return false
		}
		copy(chunk[c.ents[i].off:], rec)
		i++
		return true
	})
	switch {
	case err != nil:
		return err
	case bad || i != last+1:
		return ErrReloaded
	}
	return nil
}
