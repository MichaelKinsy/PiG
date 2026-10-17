package env

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Ports packages/durable/src/env/decode.ts StreamDecoder (class, :23-35): the pinned upstream class decodes the same chunkings under Node, and the Go StreamDecoder returns the same text for each, so a leading
// byte-order mark is dropped only at the stream's start, a U+FEFF after a chunk boundary or an invalid sequence is kept, and a split character is held back until its bytes arrive.
// mutation-checked: always dropping a leading U+FEFF, never dropping it, and not flushing the held bytes at the end each fail it.
func TestStreamDecoderMatchesPi(t *testing.T) {
	var chunkings [][][]byte
	add := func(chunks ...[]byte) { chunkings = append(chunkings, chunks) }
	add([]byte("\xef\xbb\xbfabc"))
	add([]byte("\xef\xbb"), []byte("\xbfabc"))
	add([]byte("\xef"), []byte("\xbb"), []byte("\xbf"), []byte("a"))
	add([]byte("a"), []byte("\xef\xbb\xbfb"))
	add([]byte("\xe2\x82"), []byte("\xef\xbb\xbf"), []byte("a"))
	add([]byte("\xef\xbb\xbf"), []byte("\xef\xbb\xbfa"))
	add([]byte("\xef\xbb\xbf"))
	add([]byte{})
	add([]byte{}, []byte("\xef\xbb\xbfa"))
	add([]byte("a\xf0"), []byte("\x9f"), []byte("\x98\x80b"), []byte("\x80"), []byte("\xe4\xb8"))
	add([]byte("\xf0\x9f"))
	for seed := uint32(1); seed <= 400; seed++ {
		next := mulberry32(seed)
		file := randomScanFile(next)
		var chunks [][]byte
		for offset := 0; offset < len(file); {
			size := 1 + int(next()*7)
			chunks = append(chunks, file[offset:min(offset+size, len(file))])
			offset += size
		}
		add(chunks...)
	}
	type request struct {
		Chunks []string `json:"chunks"`
	}
	requests := make([]request, len(chunkings))
	for i, chunks := range chunkings {
		requests[i].Chunks = make([]string, len(chunks))
		for j, chunk := range chunks {
			requests[i].Chunks[j] = hex.EncodeToString(chunk)
		}
	}
	input, err := json.Marshal(requests)
	if err != nil {
		t.Fatal(err)
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/stream_decoder.mjs", root)
	cmd.Stdin = bytes.NewReader(input)
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v", err)
	}
	var want []string
	if err := json.Unmarshal(output, &want); err != nil {
		t.Fatal(err)
	}
	if len(want) != len(chunkings) {
		t.Fatalf("Pi answered %d chunkings, sent %d", len(want), len(chunkings))
	}
	for i, chunks := range chunkings {
		decoder := NewStreamDecoder()
		var text strings.Builder
		for _, chunk := range chunks {
			text.WriteString(decoder.Decode(chunk))
		}
		text.WriteString(decoder.End())
		if text.String() != want[i] {
			t.Fatalf("chunking %d (%x): Go %q, Pi %q", i, chunks, text.String(), want[i])
		}
	}
}
