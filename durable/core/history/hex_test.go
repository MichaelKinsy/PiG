// SPDX-License-Identifier: MIT

package history

import (
	"encoding/hex"
	"os"
	"strings"
	"testing"
)

// unhex decodes the oracle corpus encoding: "." is the empty string.
func unhex(t testing.TB, s string) []byte {
	t.Helper()
	if s == "." {
		return nil
	}
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatalf("corpus: %v", err)
	}
	return b
}

// corpusLines reads a testdata corpus file as lines of space-separated fields.
func corpusLines(t testing.TB, name string) [][]string {
	t.Helper()
	raw, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	var out [][]string
	for line := range strings.SplitSeq(strings.TrimRight(string(raw), "\n"), "\n") {
		out = append(out, strings.Split(line, " "))
	}
	return out
}
