package jsdiff

import (
	"fmt"
	"math/rand/v2"
	"reflect"
	"strings"
	"testing"
)

// tokenizeLines is the line tokenizer of jsdiff's diffLines: each token keeps its terminating "\n" (so a "\r\n" stays whole), and the
// last token has none when the text does not end in a newline.
func tokenizeLines(text string) []string {
	var tokens []string
	start := 0
	for i := range len(text) {
		if text[i] == '\n' {
			tokens = append(tokens, text[start:i+1])
			start = i + 1
		}
	}
	if start < len(text) {
		tokens = append(tokens, text[start:])
	}
	return tokens
}

// referenceDiffLines is diffLines as a full Myers search over every line token, the way jsdiff runs it.
func referenceDiffLines(oldStr, newStr string) []Change {
	return toChanges(myers(tokenizeLines(oldStr), tokenizeLines(newStr), func(a, b string) bool { return a == b }, func(tokens []string) string { return strings.Join(tokens, "") }))
}

// TestDiffLinesMatchesFullSearch compares the windowed search with the full one on randomized texts: mostly a long shared prefix and
// suffix around a small change (the edit tool's case), with lines drawn from a small alphabet so that ambiguous alignments, which the
// lines after a change decide, are common.
func TestDiffLinesMatchesFullSearch(t *testing.T) {
	rng := rand.New(rand.NewPCG(20261012, 1))
	alphabet := []string{"a\n", "b\n", "c\n", "\n", "a\r\n", "b", "a", "\r\n", "x y\n", "é\n"}
	lines := func(n int, ending bool) string {
		var b strings.Builder
		for range n {
			line := alphabet[rng.IntN(len(alphabet))]
			if !strings.HasSuffix(line, "\n") {
				line += "\n"
			}
			b.WriteString(line)
		}
		s := b.String()
		if !ending && len(s) > 0 && rng.IntN(2) == 0 {
			s = strings.TrimSuffix(s, "\n")
			if rng.IntN(2) == 0 {
				s += alphabet[rng.IntN(len(alphabet))]
			}
		}
		return s
	}
	var pairs [][2]string
	for range 20000 {
		switch rng.IntN(4) {
		case 0:
			pairs = append(pairs, [2]string{lines(rng.IntN(8), false), lines(rng.IntN(8), false)})
		default:
			prefix, suffix := lines(rng.IntN(12), true), lines(rng.IntN(12), false)
			mid := func() string {
				s := lines(rng.IntN(4), true)
				if rng.IntN(4) == 0 {
					// A change inside a line, so the common bytes run past a line start.
					s += alphabet[rng.IntN(len(alphabet))][:1]
				}
				return s
			}
			pairs = append(pairs, [2]string{prefix + mid() + suffix, prefix + mid() + suffix})
		}
	}
	pairs = append(pairs, [2]string{"", ""}, [2]string{"", "a"}, [2]string{"a", ""}, [2]string{"a\n", "a"}, [2]string{"a", "a\n"}, [2]string{"b\nc\nb\n", "c\nb\nb\n"}, [2]string{"a\n", "a\na\n"}, [2]string{"x\r\ny\r\n", "x\ny\n"}, [2]string{"same\n", "same\n"})
	differences := 0
	for _, pair := range pairs {
		want := referenceDiffLines(pair[0], pair[1])
		d := DiffLineRuns(pair[0], pair[1])
		got := DiffLines(pair[0], pair[1])
		if !reflect.DeepEqual(got, want) && (len(got) != 0 || len(want) != 0) {
			if differences++; differences <= 5 {
				t.Errorf("DiffLines(%q, %q):\n  got:  %+v\n  want: %+v", pair[0], pair[1], got, want)
			}
			continue
		}
		oldPos, newPos := 0, 0
		oldTokens, newTokens := tokenizeLines(pair[0]), tokenizeLines(pair[1])
		for r, run := range d.Runs {
			tokens, start := newTokens, newPos
			if run.Removed {
				tokens, start = oldTokens, oldPos
			}
			if run.Start != start || run.Count != want[r].Count || d.Value(run) != want[r].Value {
				t.Fatalf("DiffLineRuns(%q, %q) run %d = %+v, want start %d of %+v", pair[0], pair[1], r, run, start, want[r])
			}
			// Read lines backward as well as forward, as the patch's leading context does.
			for i := run.Count - 1; i >= 0; i-- {
				if line := d.Line(run, i); line != tokens[start+i] {
					t.Fatalf("DiffLineRuns(%q, %q) run %d line %d = %q, want %q", pair[0], pair[1], r, i, line, tokens[start+i])
				}
			}
			switch {
			case run.Removed:
				oldPos += run.Count
			case run.Added:
				newPos += run.Count
			default:
				oldPos += run.Count
				newPos += run.Count
			}
		}
	}
	if differences > 0 {
		t.Fatalf("%d of %d pairs differ from the full search", differences, len(pairs))
	}
}

// largeEditPair is a 230 KB source file and the same file with one line changed near its middle.
func largeEditPair() (string, string) {
	var b strings.Builder
	for i := 0; b.Len() < 230_000; i++ {
		fmt.Fprintf(&b, "\tfmt.Println(\"line %d of a large generated source file\")\n", i)
	}
	oldStr := b.String()
	at := strings.Index(oldStr, "line 2000 ")
	return oldStr, oldStr[:at] + "changed " + oldStr[at:]
}

// TestDiffLinesLargeFileAllocBudget pins the windowed search: a one-line change in a 230 KB file must not tokenize or copy the file
// (the full search allocated about 1 MB for this pair).
func TestDiffLinesLargeFileAllocBudget(t *testing.T) {
	oldStr, newStr := largeEditPair()
	var changes []Change
	bytes := testing.Benchmark(func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			changes = DiffLines(oldStr, newStr)
		}
	}).AllocedBytesPerOp()
	if len(changes) != 4 {
		t.Fatalf("DiffLines returned %d changes, want 4", len(changes))
	}
	if bytes > 4<<10 {
		t.Fatalf("DiffLines on a one-line change in a %d-byte file allocated %d bytes/op, want <= 4096", len(oldStr), bytes)
	}
}

func BenchmarkDiffLinesLargeFile(b *testing.B) {
	oldStr, newStr := largeEditPair()
	b.ReportAllocs()
	for b.Loop() {
		DiffLines(oldStr, newStr)
	}
}

// scatteredPair is n lines in which about a tenth are changed, inserted or deleted at random places.
func scatteredPair(rng *rand.Rand, n, alphabet int) (string, string) {
	var oldText, newText strings.Builder
	for i := range n {
		line := fmt.Sprintf("line %d\n", rng.IntN(alphabet))
		oldText.WriteString(line)
		switch rng.IntN(20) {
		case 0:
			fmt.Fprintf(&newText, "changed %d\n", i)
		case 1:
		case 2:
			newText.WriteString(line)
			fmt.Fprintf(&newText, "inserted %d\n", i)
		default:
			newText.WriteString(line)
		}
	}
	return oldText.String(), newText.String()
}

// TestDiffLinesScatteredChangesMatchFullSearch compares the search with the full one where many lines change, so that many paths are live
// and the arena frees and reuses components.
func TestDiffLinesScatteredChangesMatchFullSearch(t *testing.T) {
	rng := rand.New(rand.NewPCG(20261018, 8))
	for range 600 {
		n := 1 + rng.IntN(300)
		oldText, newText := scatteredPair(rng, n, 1+rng.IntN(40))
		if rng.IntN(4) == 0 {
			newText = strings.TrimSuffix(newText, "\n")
		}
		if rng.IntN(4) == 0 {
			oldText, newText = newText, oldText
		}
		if got, want := DiffLines(oldText, newText), referenceDiffLines(oldText, newText); !reflect.DeepEqual(got, want) {
			t.Fatalf("diff differs from the full search for\nold %q\nnew %q", oldText, newText)
		}
	}
	for _, n := range []int{1000, 3000} {
		oldText, newText := scatteredPair(rng, n, 50)
		if got, want := DiffLines(oldText, newText), referenceDiffLines(oldText, newText); !reflect.DeepEqual(got, want) {
			t.Fatalf("%d lines: diff differs from the full search", n)
		}
	}
}

// TestDiffLinesScatteredChangesAllocsStayFlat pins the allocation per changed line: the search allocates its slots and arena by doubling,
// so 4x the lines with the same share changed must not allocate more than about 4x (jsdiff's per-step path and component allocation
// grew with the square of the changes).
func TestDiffLinesScatteredChangesAllocsStayFlat(t *testing.T) {
	rng := rand.New(rand.NewPCG(20261018, 9))
	allocs := func(n int) float64 {
		oldText, newText := scatteredPair(rng, n, 1000000)
		return testing.AllocsPerRun(3, func() { DiffLineRuns(oldText, newText) })
	}
	small, large := allocs(1000), allocs(4000)
	if large > 8*small+100 {
		t.Fatalf("allocations per diff: %.0f at 1000 lines, %.0f at 4000 lines; want growth far below the 16x of a quadratic search", small, large)
	}
}

func BenchmarkDiffLinesScattered(b *testing.B) {
	for _, n := range []int{1000, 2000, 4000, 8000, 16000} {
		oldText, newText := scatteredPair(rand.New(rand.NewPCG(1, 2)), n, 1000000)
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				DiffLineRuns(oldText, newText)
			}
		})
	}
}
