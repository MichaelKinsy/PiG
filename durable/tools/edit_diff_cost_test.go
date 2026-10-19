package tools

import (
	"errors"
	"fmt"
	"math/rand"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// editBenchmarkFile is lines lines of about 45 bytes; with smartQuotes every seventh holds a typographic apostrophe.
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

// editsFor is count edits of distinct lines spread over the file's lines. With smartQuotes each oldText has a straight apostrophe where the
// file has a typographic one, so every edit needs the fuzzy match.
func editsFor(count, lines int, smartQuotes bool) []Edit {
	edits := make([]Edit, count)
	for i := range edits {
		n := i * (lines / count)
		if smartQuotes {
			n -= n % 7
			edits[i] = Edit{OldText: fmt.Sprintf("token%d is here and it's", n), NewText: fmt.Sprintf("changed%d", i)}
			continue
		}
		edits[i] = Edit{OldText: fmt.Sprintf("token%d is here", n), NewText: fmt.Sprintf("changed%d", i)}
	}
	return edits
}

// A call's matching cost follows the file once, not once per edit: 160 edits on a 900 KB file allocate a small multiple of 20, where
// normalizing and copying the file per edit allocated eight times as much.
func TestApplyEditsAllocationDoesNotGrowWithEditCount(t *testing.T) {
	const lines = 20000
	for _, smartQuotes := range []bool{false, true} {
		content := editBenchmarkFile(lines, smartQuotes)
		measure := func(edits int) uint64 {
			var before, after runtime.MemStats
			runtime.GC()
			runtime.ReadMemStats(&before)
			if _, err := applyEditsToNormalizedContent(content, editsFor(edits, lines, smartQuotes), "f.txt"); err != nil {
				t.Fatalf("smartQuotes %v, %d edits: %v", smartQuotes, edits, err)
			}
			runtime.ReadMemStats(&after)
			return after.TotalAlloc - before.TotalAlloc
		}
		few, many := measure(20), measure(160)
		t.Logf("smartQuotes %v: %d bytes for 20 edits, %d for 160 (file %d bytes)", smartQuotes, few, many, len(content))
		if many > 2*few {
			t.Fatalf("smartQuotes %v: 160 edits allocated %d bytes against %d for 20: the file is normalized or copied per edit", smartQuotes, many, few)
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

// referenceReplacementLineRange is the linear scan getReplacementLineRange replaced.
func referenceReplacementLineRange(lines []lineSpan, r matchedEdit) (int, int, error) {
	replacementStart := r.matchIndex
	replacementEnd := r.matchIndex + r.matchLength
	startLine := -1
	for i, line := range lines {
		if replacementStart >= line.start && replacementStart < line.end {
			startLine = i
			break
		}
	}
	if startLine == -1 {
		return 0, 0, errReplacementOutsideBase
	}
	endLine := startLine
	for endLine < len(lines) && lines[endLine].end < replacementEnd {
		endLine++
	}
	if endLine >= len(lines) {
		return 0, 0, errReplacementOutsideBase
	}
	return startLine, endLine + 1, nil
}

func TestReplacementLineRangeMatchesLinearScan(t *testing.T) {
	random := rand.New(rand.NewSource(7))
	for range 3000 {
		var text strings.Builder
		for range random.Intn(8) {
			text.WriteString(strings.Repeat("x", random.Intn(4)))
			text.WriteString("\n")
		}
		text.WriteString(strings.Repeat("y", random.Intn(3)))
		lines := getLineSpans(text.String())
		for index := -1; index <= text.Len()+1; index++ {
			for size := 0; size <= text.Len()+2; size++ {
				r := matchedEdit{matchIndex: index, matchLength: size}
				gs, ge, gerr := getReplacementLineRange(lines, r)
				ws, we, werr := referenceReplacementLineRange(lines, r)
				if gs != ws || ge != we || !errors.Is(gerr, werr) {
					t.Fatalf("%q range [%d,+%d): got %d,%d,%v want %d,%d,%v", text.String(), index, size, gs, ge, gerr, ws, we, werr)
				}
			}
		}
	}
}

func BenchmarkApplyEdits(b *testing.B) {
	const lines = 20000
	for _, smartQuotes := range []bool{false, true} {
		content := editBenchmarkFile(lines, smartQuotes)
		for _, edits := range []int{10, 20, 40, 80, 160, 320} {
			list := editsFor(edits, lines, smartQuotes)
			b.Run(fmt.Sprintf("fuzzy=%v/edits=%d", smartQuotes, edits), func(b *testing.B) {
				b.ReportAllocs()
				for range b.N {
					if _, err := applyEditsToNormalizedContent(content, list, "f.txt"); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}
