package tools

import (
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// TestLsSortWeighsEachEntryOnce pins TO-T13: sorting a directory lowercases and weighs each name once, not twice per comparison (a
// 3000-entry directory of mixed-case names made about 167k allocations, lowercased copies and collation scratch per comparison).
func TestLsSortWeighsEachEntryOnce(t *testing.T) {
	dir := t.TempDir()
	const entries = 3000
	for i := range entries {
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("File_%05d_Name.TXT", (i*7919)%entries)), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	tool := &LsTool{CWD: dir}
	args := []byte(`{"path":".","limit":1}`)
	if result, err := tool.Execute(t.Context(), "id", args, nil); err != nil || result.IsError {
		t.Fatalf("ls failed: %+v, %v", result, err)
	}
	allocs := testing.AllocsPerRun(3, func() {
		_, _ = tool.Execute(t.Context(), "id", args, nil)
	})
	if budget := float64(4 * entries); allocs > budget {
		t.Fatalf("ls of %d entries made %.0f allocations, want <= %.0f", entries, allocs, budget)
	}
}

// TestLsCollationKeysOrderLikeCollator checks that sorting by collation keys gives jsstring.Collator's order on randomized names drawn
// from characters that collation treats specially: accents (precomposed and combining), ignorables, punctuation, digits, contractions
// and expansions, ligatures, Hangul, CJK, emoji, and names that are prefixes of each other or equal but for case or accent.
func TestLsCollationKeysOrderLikeCollator(t *testing.T) {
	rng := rand.New(rand.NewPCG(20261012, 13))
	pieces := []string{"a", "A", "b", "z", "é", "e\u0301", "E", "ß", "ss", "æ", "ae", "ﬁ", "fi", "-", "_", ".", " ", "1", "10", "2", "\u00ad", "\u200b", "\x00", "\t", "ch", "c", "h", "l·l", "ŀ", "ı", "İ", "가", "각", "ᄀ", "漢", "字", "😀", "👍🏽", "ǅ", "Ω", "ω", "ͅ", "ø", "o\u0338", "ä", "a\u0308", "～", "~", "＃", "#", "\uFFFF", "\U0001F600"}
	collator := jsstring.NewCollator()
	for range 2000 {
		names := make([]string, 2+rng.IntN(40))
		for i := range names {
			var b strings.Builder
			for range 1 + rng.IntN(5) {
				b.WriteString(pieces[rng.IntN(len(pieces))])
			}
			names[i] = b.String()
		}
		want := slices.Clone(names)
		slices.SortStableFunc(want, func(a, b string) int {
			return collator.CompareString(jsstring.ToLower(a), jsstring.ToLower(b))
		})
		got := slices.Clone(names)
		sortEntriesLikeLocaleCompare(got)
		if !slices.Equal(got, want) {
			t.Fatalf("sortEntriesLikeLocaleCompare(%q) = %q, want %q", names, got, want)
		}
	}
}
