//go:build !pig_strip_syntax_highlight

package tui

import (
	"encoding/json"
	"hash/fnv"
	"math/rand"
	"os"
	"slices"
	"testing"
	"unicode/utf8"
)

// TestStreamedHighlightEqualsAWholeRun pins the resumable highlighter to the whole-run one: a code block streamed in growing steps, in every language of highlight.js's oracle corpus, returns at every step the lines highlightCodeUncached returns for the same text (highlight.js highlight, then the terminal theme), and most steps resume from an earlier step.
func TestStreamedHighlightEqualsAWholeRun(t *testing.T) {
	LoadAllHighlightLanguages()
	data, err := os.ReadFile("internal/hljs/testdata/corpus.json")
	if err != nil {
		t.Fatal(err)
	}
	var corpus []struct{ ID, Lang, Code string }
	if err := json.Unmarshal(data, &corpus); err != nil {
		t.Fatal(err)
	}
	theme := ActiveTheme()
	steps, resumed := 0, 0
	for _, c := range corpus {
		if !SupportsLanguage(c.Lang) {
			continue
		}
		hlMu.Lock()
		resetHighlightCacheLocked()
		hlTheme = theme
		hlMu.Unlock()
		h := fnv.New64a()
		h.Write([]byte(c.ID))
		rng := rand.New(rand.NewSource(int64(h.Sum64())))
		for end := 0; end < len(c.Code); {
			end = min(len(c.Code), end+1+rng.Intn(max(1, len(c.Code)/15)))
			for end < len(c.Code) && !utf8.RuneStart(c.Code[end]) {
				end++
			}
			code := c.Code[:end]
			if base, _ := hlFindResume(code, c.Lang, true, theme); base.prefix != "" {
				resumed++
				// A point follows at least one scan iteration; its count is where the resumed run continues counting.
				if base.iterations == 0 {
					t.Fatalf("%s (%s): the resume state at %d bytes holds no iteration count", c.ID, c.Lang, len(base.prefix))
				}
			}
			steps++
			got := highlightCodeLines(code, c.Lang, true)
			want := highlightCodeUncached(highlightRegistry(), code, c.Lang, true, theme)
			if !slices.Equal(got, want) {
				t.Fatalf("%s (%s) at %d of %d bytes:\n got %q\nwant %q", c.ID, c.Lang, end, len(c.Code), got, want)
			}
		}
	}
	t.Logf("%d steps, %d resumed", steps, resumed)
	if resumed < steps/4 {
		t.Fatalf("only %d of %d steps resumed", resumed, steps)
	}
}

// TestStreamedHighlightKeepsBlocksAndThemesApart streams two blocks of one language alternately, and switches the theme between steps: a resume state serves only a block whose text it prefixes, under the theme it rendered with.
func TestStreamedHighlightKeepsBlocksAndThemesApart(t *testing.T) {
	LoadAllHighlightLanguages()
	data, err := os.ReadFile("internal/hljs/testdata/corpus.json")
	if err != nil {
		t.Fatal(err)
	}
	var corpus []struct{ ID, Lang, Code string }
	if err := json.Unmarshal(data, &corpus); err != nil {
		t.Fatal(err)
	}
	orig := ActiveTheme().Name
	defer SetTheme(orig)
	for _, lang := range []string{"python", "go", "json", "bash"} {
		var blocks []string
		for _, c := range corpus {
			if c.Lang == lang && len(c.Code) > 80 && len(blocks) < 2 {
				blocks = append(blocks, c.Code)
			}
		}
		if len(blocks) < 2 {
			t.Fatalf("no two corpus samples of %s", lang)
		}
		hlMu.Lock()
		resetHighlightCacheLocked()
		hlMu.Unlock()
		const steps = 12
		for step := 1; step <= steps; step++ {
			if step == steps/2 {
				SetTheme("light")
			}
			for _, block := range blocks {
				end := len(block) * step / steps
				for end < len(block) && !utf8.RuneStart(block[end]) {
					end++
				}
				got := highlightCodeLines(block[:end], lang, true)
				want := highlightCodeUncached(highlightRegistry(), block[:end], lang, true, ActiveTheme())
				if !slices.Equal(got, want) {
					t.Fatalf("%s step %d theme %s:\n got %q\nwant %q", lang, step, ActiveTheme().Name, got, want)
				}
			}
		}
		SetTheme(orig)
	}
}
