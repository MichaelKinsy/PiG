//go:build !race

package jsstring

import (
	"math"
	"math/rand"
	"strings"
	"testing"
)

// writeArguments is the arguments object of a write tool call that carries size bytes of source text (Pi-Bolt bench/fake_model_stress.py write:N).
func writeArguments(size int) map[string]any {
	return map[string]any{"path": "src/generated/step.ts", "content": budgetPayload(size)}
}

// lengthCorpusPieces holds every class of text JSON.stringify counts differently: plain ASCII, the characters it lengthens (the quote, the
// backslash, the controls with a short escape and those written as \u00xx), DEL, text outside ASCII (two, three and four byte characters, the
// line and paragraph separators), a lone high and a lone low surrogate, a surrogate pair written as two halves, and ill-formed bytes.
var lengthCorpusPieces = []string{
	"a", "path/to/file.ts", " ", "<", ">", "&", "-0", "1e21",
	"\"", "\\", "\\u0041", "\\ufffd",
	"\b", "\f", "\n", "\r", "\t", "\x00", "\x01", "\x1f", "\x7f",
	"é", "日本語", "😀", "\u2028", "\u2029", "\ufffd",
	"\xed\xa0\x80", "\xed\xb0\x80", "\xed\xa0\xbd\xed\xb8\x80",
	"\xff", "\xc3", "\xe2\x82", "\xf0\x9f\x98",
}

func lengthCorpusText(random *rand.Rand, ascii bool) string {
	var text strings.Builder
	for range random.Intn(8) {
		piece := lengthCorpusPieces[random.Intn(len(lengthCorpusPieces))]
		if ascii && strings.ContainsFunc(piece, func(r rune) bool { return r >= 0x80 }) {
			continue
		}
		text.WriteString(piece)
		if random.Intn(6) == 0 {
			text.WriteString(strings.Repeat("x", random.Intn(70)))
		}
	}
	return text.String()
}

func lengthCorpusValue(random *rand.Rand, depth int) any {
	switch kind := random.Intn(12); {
	case kind < 3:
		return lengthCorpusText(random, random.Intn(2) == 0)
	case kind < 7 && depth < 3:
		// An object: of ASCII strings only (the class counted from bytes), or with any member.
		ascii := random.Intn(2) == 0
		members := map[string]any{}
		for range random.Intn(5) {
			if ascii {
				members[lengthCorpusText(random, true)] = lengthCorpusText(random, true)
			} else {
				members[lengthCorpusText(random, random.Intn(2) == 0)] = lengthCorpusValue(random, depth+1)
			}
		}
		return members
	case kind == 7 && depth < 3:
		items := make([]any, random.Intn(4))
		for i := range items {
			items[i] = lengthCorpusValue(random, depth+1)
		}
		return items
	case kind == 8:
		return []float64{math.Copysign(0, -1), 0, 1.5, 1e21, 1e-7, math.NaN(), math.Inf(-1), -2, 9007199254740993}[random.Intn(9)]
	case kind == 9:
		return random.Intn(2000) - 1000
	case kind == 10:
		return random.Intn(2) == 0
	}
	return nil
}

// StringifyLength equals the count of the encoded text (stringifyLength, the path of every input before the count from bytes, and the
// path every input outside ASCII strings and objects of ASCII strings still takes) for every class of input, and the independent
// reference agrees. The corpus reaches both paths.
func TestStringifyLengthEqualsTheEncodedCountForEveryInputClass(t *testing.T) {
	fromBytes, encoded := 0, 0
	check := func(value any) {
		t.Helper()
		old, oldErr := stringifyLength(value)
		want, wantErr := referenceStringifyLength(value)
		got, gotErr := StringifyLength(value)
		if oldErr != nil || wantErr != nil || gotErr != nil || got != old || got != want {
			t.Fatalf("%#v: got %d, %v; the encoded count %d, %v; the reference %d, %v", value, got, gotErr, old, oldErr, want, wantErr)
		}
		counted := false
		switch value := value.(type) {
		case string:
			_, counted = quotedASCIILength(value)
		case map[string]any:
			_, counted = stringObjectLength(value)
		}
		if counted {
			fromBytes++
		} else {
			encoded++
		}
	}
	for _, piece := range lengthCorpusPieces {
		for _, text := range []string{piece, "ab" + piece + "cd", piece + piece, strings.Repeat("x", 40) + piece + strings.Repeat("y", 9)} {
			for _, value := range []any{text, map[string]any{"k": text}, map[string]any{text: "v", "other": text}, map[string]any{"k": text, "n": 1.5}, map[string]any{"k": map[string]any{"k": text}}, []any{text, text}} {
				check(value)
			}
		}
	}
	for c := range 256 {
		text := "ab" + string([]byte{byte(c)}) + "cd"
		check(text)
		check(map[string]any{text: text})
	}
	for _, value := range []any{"", map[string]any{}, map[string]any(nil), map[string]any{"a": nil}, map[string]any{"": ""}, writeArguments(4 << 10), writeArguments(200 << 10)} {
		check(value)
	}
	for seed := range int64(20000) {
		check(lengthCorpusValue(rand.New(rand.NewSource(seed)), 0))
	}
	if fromBytes < 1000 || encoded < 1000 {
		t.Fatalf("the corpus counted %d values from bytes and %d from the encoded text: both paths need coverage", fromBytes, encoded)
	}
}

// The arguments of a large write are counted without their text being encoded: no allocation, however large the text is.
func TestStringifyLengthOfWriteArgumentsAllocatesNothing(t *testing.T) {
	for _, size := range []int{4 << 10, 200 << 10} {
		arguments := writeArguments(size)
		if allocs := testing.AllocsPerRun(20, func() {
			if _, err := StringifyLength(arguments); err != nil {
				t.Fatal(err)
			}
		}); allocs != 0 {
			t.Errorf("%d bytes of text: %v allocations per call, want 0", size, allocs)
		}
	}
}

// BenchmarkStringifyLengthWriteArguments counts the arguments of write tool calls, as the context estimate does for every tool call of a
// session whose provider reports no usage.
func BenchmarkStringifyLengthWriteArguments(b *testing.B) {
	for _, bench := range []struct {
		name string
		size int
	}{{"4KB", 4 << 10}, {"200KB", 200 << 10}} {
		b.Run(bench.name, func(b *testing.B) {
			arguments := writeArguments(bench.size)
			b.SetBytes(int64(bench.size))
			b.ReportAllocs()
			for b.Loop() {
				if _, err := StringifyLength(arguments); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
