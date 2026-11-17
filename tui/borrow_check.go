//go:build pigborrowcheck

package tui

import (
	"hash/maphash"
	"sync"
)

// The slice Container.Render and Box.Render return is borrowed: it is shared with every caller until the lines change, and Go cannot refuse
// an index write into it (Pi's `readonly string[]` is a compile-time check only). Under -tags pigborrowcheck every hand-out records the
// checksum of the lines, and the next render of the same container, before it hands the lines out again, panics when the checksum no longer
// matches: some caller wrote into the borrowed array. The check sees an index write that ends before the container renders again and that
// changes a string; it cannot see a write that is undone before then, a write into a string's bytes (strings are immutable in Go), or a write into
// an array no container retains. The default build compiles borrow_nocheck.go, whose empty functions are inlined away: no cost.

var borrowSeed = maphash.MakeSeed()

// borrowSums holds the checksum of the lines last handed out, by the container or the box cache that handed them out.
var borrowSums sync.Map

func borrowSum(lines []string) uint64 {
	var h maphash.Hash
	h.SetSeed(borrowSeed)
	for _, line := range lines {
		h.WriteString(line)
		h.WriteByte(0)
	}
	h.WriteString(string(rune(len(lines))))
	return h.Sum64()
}

func (c *Container) sealPublicLocked() { borrowSums.Store(c, borrowSum(c.public)) }

func (c *Container) verifyPublicLocked() {
	if sum, ok := borrowSums.Load(c); ok && c.publicValid && borrowSum(c.public) != sum.(uint64) {
		panic("tui: a caller wrote into the lines a Container's Render returned; copy them first (slices.Clone)")
	}
}

func (b *Box) sealCacheLines() {
	if b.cache != nil {
		borrowSums.Store(b.cache, borrowSum(b.cache.lines))
	}
}

func (b *Box) verifyCacheLines() {
	if b.cache == nil {
		return
	}
	if sum, ok := borrowSums.Load(b.cache); ok && borrowSum(b.cache.lines) != sum.(uint64) {
		panic("tui: a caller wrote into the lines a Box's Render returned; copy them first (slices.Clone)")
	}
}
