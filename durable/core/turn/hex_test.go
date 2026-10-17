package turn

import (
	"encoding/hex"
	"os"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/durable/core/jv"
)

func unhex(t testing.TB, s string) string {
	t.Helper()
	if s == "." {
		return ""
	}
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatalf("corpus: %v", err)
	}
	return string(b)
}

// corpusLines reads an oracle corpus (testdata/*.hex) as lines of space-separated fields.
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

func parseObject(t testing.TB, text string) *jv.Object {
	t.Helper()
	v, err := jv.ParseString(text)
	if err != nil {
		t.Fatalf("parse %q: %v", text, err)
	}
	o, ok := v.(*jv.Object)
	if !ok {
		t.Fatalf("%q is not an object", text)
	}
	return o
}
