package tui

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"
)

// Pi's ProcessTerminal calls process.stdin.setEncoding("utf8"), so its stdin text comes from Node's StringDecoder. The golden holds the text Node returns for each write of a chunk sequence (utf8-decoder-oracle.mjs, Node 24). It covers a trailing C0, C1, F5-F7, E0/ED/F0/F4 followed by an out-of-range second byte, and a held lead byte followed by a non-continuation.
// Pi: packages/tui/src/terminal.ts ProcessTerminal start (stdin.setEncoding).
func TestUTF8DecoderMatchesNodeStringDecoder(t *testing.T) {
	raw, err := os.ReadFile("testdata/utf8-decoder-oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Chunks []string `json:"chunks"`
		Out    []string `json:"out"`
	}
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) < 500 {
		t.Fatalf("golden has %d cases", len(cases))
	}
	for _, tc := range cases {
		var d utf8Decoder
		for i, h := range tc.Chunks {
			chunk, _ := hex.DecodeString(h)
			got, ok := d.write(chunk)
			if got != tc.Out[i] || ok != (tc.Out[i] != "") {
				t.Fatalf("chunks %v, write %d: got %q (ok %v), Node returned %q", tc.Chunks, i, got, ok, tc.Out[i])
			}
		}
	}
}
