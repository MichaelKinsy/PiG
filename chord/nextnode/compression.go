// Package nextnode ports packages/chord/src/next-node: compression for Chord v2 hosts (§22, slice 4b), the
// synchronous compressor `createHost({ compression })` takes. Upstream keeps it outside `src/next` because `next`
// imports no Node module; Go's compress/flate plays the role of Node's zlib, so the split carries no constraint and
// the package exists to mirror the upstream layout.
package nextnode

import (
	"bytes"
	"compress/flate"
	"io"
	"math"
)

// Ports packages/chord/src/next-node/compression.ts (nodeCompression) and the Inflated outcome of
// packages/chord/src/next/link/part.ts it returns.

// InflatedKind tags an Inflated outcome.
type InflatedKind string

const (
	// InflatedBytes is an inflate that fit the limit.
	InflatedBytes InflatedKind = "bytes"
	// InflatedTooLarge is an inflate whose output would be larger than the limit.
	InflatedTooLarge InflatedKind = "too_large"
	// InflatedCorrupt is data that is no raw deflate stream.
	InflatedCorrupt InflatedKind = "corrupt"
)

// Inflated is the outcome of Compression.Inflate, tagged by Kind: Bytes is set for InflatedBytes, Error for
// InflatedCorrupt.
type Inflated struct {
	Kind  InflatedKind
	Bytes []byte
	Error error
}

// NoLimit is `Number.POSITIVE_INFINITY`: an inflate limit that never bounds the output.
var NoLimit = math.Inf(1)

// Compression is the synchronous compressor `createHost({ compression })` takes (packages/chord/src/next/link/part.ts).
// Deflate is raw deflate at level 1. Inflate gives at most limit bytes (NoLimit: no limit; 0 allowed), too_large when
// the output would be larger, corrupt for data that is no raw deflate stream.
type Compression interface {
	Deflate(bytes []byte) []byte
	Inflate(bytes []byte, limit float64) Inflated
}

type nodeCompression struct{}

// NodeCompression is upstream's `nodeCompression`: raw deflate at level 1 and inflate bounded by the limit.
var NodeCompression Compression = nodeCompression{}

// Deflate is raw deflate at level 1.
func (nodeCompression) Deflate(input []byte) []byte {
	var out bytes.Buffer
	writer, err := flate.NewWriter(&out, 1)
	if err != nil {
		panic(err) // level 1 is valid
	}
	if _, err := writer.Write(input); err != nil {
		panic(err) // a bytes.Buffer does not fail
	}
	if err := writer.Close(); err != nil {
		panic(err)
	}
	return out.Bytes()
}

// Inflate inflates raw deflate data, reading at most limit+1 bytes so a decompression bomb never inflates past the
// bound. Data that is no raw deflate stream, or ends early, is corrupt.
func (nodeCompression) Inflate(input []byte, limit float64) Inflated {
	reader := flate.NewReader(bytes.NewReader(input))
	var source io.Reader = reader
	if limit < math.MaxInt64 {
		// One byte past the limit proves the output is larger.
		source = io.LimitReader(reader, int64(limit)+1)
	}
	inflated, err := io.ReadAll(source)
	if err != nil {
		return Inflated{Kind: InflatedCorrupt, Error: err}
	}
	if float64(len(inflated)) > limit {
		return Inflated{Kind: InflatedTooLarge}
	}
	return Inflated{Kind: InflatedBytes, Bytes: inflated}
}
