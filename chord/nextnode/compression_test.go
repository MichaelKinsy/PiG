package nextnode_test

import (
	"bytes"
	"encoding/json"
	"math"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/nextnode"
)

// pi: packages/chord/src/next-node/compression.ts
// Ports packages/chord/test/node/compression.test.ts: raw deflate and the bounded inflate with its tagged
// outcomes, a limit of 0 included.

var infinity = nextnode.NoLimit

func bytesOf(t *testing.T, inflated nextnode.Inflated) []byte {
	t.Helper()
	if inflated.Kind != nextnode.InflatedBytes {
		t.Fatalf("inflate kind = %q, want bytes", inflated.Kind)
	}
	return inflated.Bytes
}

func TestNodeCompression(t *testing.T) {
	t.Run("deflates raw at level 1 and inflates back", func(t *testing.T) {
		encoded, err := json.Marshal(map[string]string{"text": strings.Repeat("lorem ipsum ", 5000)})
		if err != nil {
			t.Fatal(err)
		}
		deflated := nextnode.NodeCompression.Deflate(encoded)
		if len(deflated) >= len(encoded)/10 {
			t.Fatalf("deflated %d bytes of %d, want under a tenth", len(deflated), len(encoded))
		}
		if got := bytesOf(t, nextnode.NodeCompression.Inflate(deflated, float64(len(encoded)))); !bytes.Equal(got, encoded) {
			t.Fatal("inflate at the exact size differs from the input")
		}
		if got := bytesOf(t, nextnode.NodeCompression.Inflate(deflated, infinity)); !bytes.Equal(got, encoded) {
			t.Fatal("inflate without a limit differs from the input")
		}
	})

	t.Run("gives too_large past the limit, without inflating the rest (a bomb)", func(t *testing.T) {
		bomb := nextnode.NodeCompression.Deflate(make([]byte, 256*1024*1024))
		inflated := nextnode.NodeCompression.Inflate(bomb, 1024*1024)
		if inflated.Kind != nextnode.InflatedTooLarge || inflated.Bytes != nil || inflated.Error != nil {
			t.Fatalf("inflate = %+v, want only too_large", inflated)
		}
	})

	t.Run("takes a limit of 0: an empty stream inflates, anything else is too_large", func(t *testing.T) {
		if got := bytesOf(t, nextnode.NodeCompression.Inflate(nextnode.NodeCompression.Deflate(make([]byte, 0)), 0)); len(got) != 0 {
			t.Fatalf("empty stream inflated to %d bytes", len(got))
		}
		for _, size := range []int{1, 100} {
			inflated := nextnode.NodeCompression.Inflate(nextnode.NodeCompression.Deflate(make([]byte, size)), 0)
			if inflated.Kind != nextnode.InflatedTooLarge {
				t.Fatalf("%d bytes at limit 0: kind = %q, want too_large", size, inflated.Kind)
			}
		}
		if got := bytesOf(t, nextnode.NodeCompression.Inflate(nextnode.NodeCompression.Deflate(make([]byte, 1)), 1)); !bytes.Equal(got, make([]byte, 1)) {
			t.Fatalf("one byte at limit 1 = %v", got)
		}
	})

	t.Run("gives corrupt for data that is no raw deflate stream", func(t *testing.T) {
		inflated := nextnode.NodeCompression.Inflate([]byte{255, 1, 2, 3}, 100)
		if inflated.Kind != nextnode.InflatedCorrupt || inflated.Error == nil {
			t.Fatalf("inflate = %+v, want corrupt with an error", inflated)
		}
	})
}

func TestNodeCompressionInflateLimitEdges(t *testing.T) {
	deflated := nextnode.NodeCompression.Deflate([]byte("abc"))
	// Node takes its default max for a NaN maxOutputLength and `len > NaN` is false: the bytes come back.
	if got := nextnode.NodeCompression.Inflate(deflated, math.NaN()); got.Kind != nextnode.InflatedBytes || string(got.Bytes) != "abc" {
		t.Fatalf("NaN limit = %+v, want bytes abc", got)
	}
	// A negative or infinite-below limit is past by one byte, like a limit of 0.
	if got := nextnode.NodeCompression.Inflate(deflated, math.Inf(-1)); got.Kind != nextnode.InflatedTooLarge {
		t.Fatalf("-Inf limit kind = %q, want too_large", got.Kind)
	}
	if got := nextnode.NodeCompression.Inflate(deflated, -5); got.Kind != nextnode.InflatedTooLarge {
		t.Fatalf("-5 limit kind = %q, want too_large", got.Kind)
	}
}
