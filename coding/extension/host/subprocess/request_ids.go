package subprocess

import "strconv"

// idChunkSize is the number of consecutive request IDs one chunk spells. A power of two, so a chunk starts at a multiple of it.
//
// pig additive (D112): Pi loads extensions in process and has no IPC request IDs; the chunk size only sizes a host-side string table.
const idChunkSize = 32

// idChunk holds the IDs "r<first>" to "r<first+idChunkSize-1>" as substrings of one string, so handing one out allocates nothing.
type idChunk struct {
	first uint64
	ids   [idChunkSize]string
}

func newIDChunk(first uint64) *idChunk {
	text := make([]byte, 0, idChunkSize*8)
	var ends [idChunkSize]int
	for i := range ends {
		text = append(text, 'r')
		text = strconv.AppendUint(text, first+uint64(i), 10)
		ends[i] = len(text)
	}
	spelled := string(text)
	chunk := &idChunk{first: first}
	start := 0
	for i, end := range ends {
		chunk.ids[i] = spelled[start:end]
		start = end
	}
	return chunk
}

// newRequestID returns a request ID unique on this connection: "r" and the next number of the connection's counter. Callers racing across a chunk's end each store the chunk of their own number, and the last store wins; every ID is still spelled from its own number.
func (c *Conn) newRequestID() string {
	n := c.nextID.Add(1)
	chunk := c.idChunk.Load()
	if chunk == nil || n-chunk.first >= idChunkSize {
		chunk = newIDChunk(n &^ (idChunkSize - 1))
		c.idChunk.Store(chunk)
	}
	return chunk.ids[n-chunk.first]
}
