package tools

import (
	"fmt"
	"math/rand"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// The edit matcher normalizes the file once and reuses the result, which is exact only because normalizeForFuzzyMatch
// returns its own result unchanged when applied again.
func TestNormalizeForFuzzyMatchIsIdempotent(t *testing.T) {
	pieces := []string{"a", "b ", " ", "\t", "\r", "\n", "\u2018", "\u2019", "\u201c", "\u201d", "\u2013", "\u2014", "\u2212", "\u00a0", "\u2003", "\u202f", "\u3000", "\u0301", "\u0338", "=", "<", ">", "e", "\ufb01", "\u2126", "x\u2019\u0301", "-", "'", "\"", "\u212b", "\u1e9b\u0323"}
	for seed := range int64(20000) {
		random := rand.New(rand.NewSource(seed))
		var text strings.Builder
		for range 1 + random.Intn(12) {
			text.WriteString(pieces[random.Intn(len(pieces))])
		}
		once := normalizeForFuzzyMatch(text.String())
		if twice := normalizeForFuzzyMatch(once); twice != once {
			t.Fatalf("%q: once %q, twice %q", text.String(), once, twice)
		}
	}
}

func editBenchmarkFile(lines int, smartQuotes bool) string {
	var file strings.Builder
	for i := range lines {
		quote := "'"
		if smartQuotes && i%7 == 0 {
			quote = "\u2019"
		}
		fmt.Fprintf(&file, "line %d: token%d is here and it%ss   \n", i, i, quote)
	}
	return file.String()
}

// editsFor is count edits of distinct lines. With smartQuotes each oldText has a straight apostrophe where the file has a typographic one, so every edit needs the fuzzy match.
func editsFor(count int, smartQuotes bool) []editEntry {
	edits := make([]editEntry, count)
	for i := range edits {
		if smartQuotes {
			edits[i] = editEntry{OldText: fmt.Sprintf("token%d is here and it's", 35*i), NewText: fmt.Sprintf("changed%d", i)}
			continue
		}
		edits[i] = editEntry{OldText: fmt.Sprintf("token%d is here", 40*i+3), NewText: fmt.Sprintf("changed%d", i)}
	}
	return edits
}

// A call's matching cost follows the file once, not once per edit: 50 edits against a file allocate a small multiple of 5.
func TestApplyEditsNormalizesTheFileOnce(t *testing.T) {
	for _, smartQuotes := range []bool{false, true} {
		content := editBenchmarkFile(4000, smartQuotes)
		measure := func(edits int) uint64 {
			var before, after runtime.MemStats
			runtime.GC()
			runtime.ReadMemStats(&before)
			if _, err := applyEditsToNormalizedContent(content, editsFor(edits, smartQuotes), "f.txt"); err != nil {
				t.Fatalf("smartQuotes %v, %d edits: %v", smartQuotes, edits, err)
			}
			runtime.ReadMemStats(&after)
			return after.TotalAlloc - before.TotalAlloc
		}
		five, fifty := measure(5), measure(50)
		t.Logf("smartQuotes %v: %d bytes for 5 edits, %d for 50 (file %d bytes)", smartQuotes, five, fifty, len(content))
		if fifty > 3*five {
			t.Fatalf("smartQuotes %v: 50 edits allocated %d bytes against %d for 5: the file is normalized per edit", smartQuotes, fifty, five)
		}
	}
}

// referenceApplyReplacements is upstream's applyReplacements: each replacement spliced in from the end.
func referenceApplyReplacements(content string, replacements []matchedEdit, offset int) string {
	result := content
	for _, r := range slices.Backward(replacements) {

		i := r.matchIndex - offset
		result = result[:i] + r.newText + result[i+r.matchLength:]
	}
	return result
}

func TestApplyReplacementsMatchesSplicingFromTheEnd(t *testing.T) {
	for seed := range int64(3000) {
		random := rand.New(rand.NewSource(seed))
		content := strings.Repeat("abcdefghij\n", 1+random.Intn(8))
		offset := random.Intn(5)
		window := content[offset:]
		var replacements []matchedEdit
		for position := 0; position < len(window); {
			position += random.Intn(6)
			length := random.Intn(4) // zero-length replacements are inserts and may share an index
			if position+length > len(window) {
				break
			}
			replacements = append(replacements, matchedEdit{matchIndex: position + offset, matchLength: length, newText: strings.Repeat("N", random.Intn(5))})
			position += length
		}
		if got, want := applyReplacements(window, replacements, offset), referenceApplyReplacements(window, replacements, offset); got != want {
			t.Fatalf("seed %d: got %q want %q (replacements %+v)", seed, got, want, replacements)
		}
	}
}
