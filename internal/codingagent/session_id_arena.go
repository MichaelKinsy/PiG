package codingagent

import "strings"

// idArena holds the ids of a Session's slots, each after its length in a byte, in chunks that are never moved or written again, so the index keeps
// no string header and no allocation of its own for an id. An id longer than a byte holds, or one the chunks have no room for, is kept as a string.
// A chunk is a strings.Builder grown to its full size and never written past it, so a string of it stays valid and is not copied.
// pig additive (D112): Pi keeps each id as a string of its entry's object; where the Go index keeps the bytes changes its size, never an id.
type idArena struct {
	chunks []*strings.Builder
	long   []string
}

const (
	// idChunkBits is the bits of a reference that locate an id in its chunk; a chunk holds at most 1<<idChunkBits bytes.
	idChunkBits = 16
	// idLongRef marks a reference to the Session's long ids.
	idLongRef = 1 << 31
	// idFirstChunk is the size of the first chunk; each next one doubles it up to a full chunk, so a session of a few entries costs little.
	idFirstChunk = 512
	// maxIDChunks is the number of chunks a reference can name.
	maxIDChunks = 1 << (31 - idChunkBits)
	// maxArenaID is the longest id kept in a chunk.
	maxArenaID = 255 // pig divergence (D112): a one-byte length in the arena, not an upstream limit; a longer id is kept as a string.
)

// add stores id and returns its reference.
func (a *idArena) add(id string) uint32 {
	if len(id) <= maxArenaID {
		last := len(a.chunks) - 1
		if last < 0 || a.chunks[last].Len()+1+len(id) > a.chunks[last].Cap() {
			if len(a.chunks) < maxIDChunks {
				chunk := new(strings.Builder)
				chunk.Grow(min(int(idFirstChunk)<<min(len(a.chunks), 7), 1<<idChunkBits))
				a.chunks = append(a.chunks, chunk)
				last++
			} else {
				last = -1
			}
		}
		if last >= 0 {
			chunk := a.chunks[last]
			at := chunk.Len()
			chunk.WriteByte(byte(len(id)))
			chunk.WriteString(id)
			return uint32(last)<<idChunkBits | uint32(at)
		}
	}
	a.long = append(a.long, id)
	return idLongRef | uint32(len(a.long)-1)
}

// get returns the id at ref.
func (a *idArena) get(ref uint32) string {
	if ref&idLongRef != 0 {
		return a.long[ref&^idLongRef]
	}
	chunk, at := a.chunks[ref>>idChunkBits].String(), ref&(1<<idChunkBits-1)
	return chunk[at+1 : at+1+uint32(chunk[at])]
}
