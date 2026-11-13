//go:build !pig_strip_syntax_highlight

package tui

import (
	"slices"
	"strconv"
	"strings"
	"testing"
	"unsafe"

	"github.com/MichaelKinsy/PiG/tui/internal/hljs"
)

// heldResumeStates is a copy of the resume states.
func heldResumeStates() []hlResume {
	hlMu.Lock()
	defer hlMu.Unlock()
	return slices.Clone(hlResumes)
}

// TestAResumeStateInsideOpenModesIsBounded: a state whose resume point lies inside open modes keeps, besides what every state keeps (the code, its lines, the output of the point's line), the frames of the point (at most hljs.ResumeStackMax, four words each: tui/internal/hljs TestAPointHoldsAtMostResumeStackMaxFrames), the spans open at the point (as many at most, three words each) and the HTML of one run of text before the point, with no tag and no line break in it. A document of lists nested 100 deep and a script with lines under 70 open spans stream with the lines of a whole run at every frame; no state exceeds the bounds, and hlResumeMax states are kept.
func TestAResumeStateInsideOpenModesIsBounded(t *testing.T) {
	if got, want := unsafe.Sizeof(scopeEntry{}), 3*unsafe.Sizeof(uintptr(0)); got != want {
		t.Errorf("a span of a state is %d bytes; want three words, %d", got, want)
	}
	var lists strings.Builder
	for n := range 100 {
		lists.WriteString("[\"level " + strconv.Itoa(n) + "\",\n")
	}
	lists.WriteString("\"deepest\"\n" + strings.Repeat("]\n", 100))
	// Each level is a string and a substitution inside it, two modes and two spans. The first six lines of commands lie under 70 of them, where no point is taken; the next six under 62.
	commands := strings.Repeat("echo \"c\" d\n", 6)
	spans := strings.Repeat("\"$(", 35) + "\n" + commands + strings.Repeat(")\"", 4) + " b\n" + commands + strings.Repeat(")\"", 31) + "\necho done\necho more\n"
	for _, c := range []struct {
		name, lang, code string
		spans            int
	}{
		{"lists inside lists", "json", lists.String(), 0},
		{"substitutions inside strings", "bash", spans, 32},
	} {
		theme := resumeStateTheme()
		deepest, states := 0, 0
		for end := 1; end <= len(c.code); end++ {
			if c.code[end-1] != '\n' {
				continue
			}
			checkHighlightedFrame(t, c.name+", a frame per line", c.code[:end], c.lang, theme)
			held := heldResumeStates()
			if len(held) > hlResumeMax {
				t.Fatalf("%s: %d resume states are held, the bound is %d", c.name, len(held), hlResumeMax)
			}
			for _, state := range held {
				states++
				deepest = max(deepest, len(state.scopes))
				if len(state.scopes) > hljs.ResumeStackMax {
					t.Fatalf("%s: a state keeps %d open spans, the bound is %d", c.name, len(state.scopes), hljs.ResumeStackMax)
				}
				if strings.ContainsAny(state.pending, "<>\n") {
					t.Fatalf("%s: a state keeps the HTML of more than one run of text: %q", c.name, state.pending)
				}
			}
		}
		if states == 0 || deepest < c.spans {
			t.Fatalf("%s: %d states seen, at most %d open spans in one; want a state under %d spans or more", c.name, states, deepest, c.spans)
		}
		t.Logf("%s: %d states seen, at most %d open spans in one", c.name, states, deepest)
	}
}

// TestAResumeStateOfAnotherRegistryHighlightsTheWholeCode: the frames a state inside open modes holds are of the Registry that offered its point. Another Registry does not continue them (hljs.ErrResumeFailed), and highlightResumable highlights the whole code: the lines are those of a whole run.
func TestAResumeStateOfAnotherRegistryHighlightsTheWholeCode(t *testing.T) {
	theme := resumeStateTheme()
	code := "[\n" + strings.Repeat("  {\"id\": 1, \"name\": \"an item\", \"tags\": [\"a\", \"b\"]},\n", 12) + "  {}\n]\n"
	half := code[:len(code)/2+strings.IndexByte(code[len(code)/2:], '\n')+1]
	checkHighlightedFrame(t, "half of the list", half, "json", theme)
	if base, _ := hlFindResume(code, "json", theme, false); base.prefix == "" {
		t.Fatal("half of the list left no resume state")
	}
	other := hljs.NewRegistry()
	other.LoadAllLanguages()
	got, ok := highlightResumable(other, code, "json", theme, false)
	if want := highlightCodeUncached(highlightRegistry(), code, "json", theme); !ok || !slices.Equal(got, want) {
		t.Fatalf("the list highlighted by another Registry from the state of the first:\n got %q\nwant %q", got, want)
	}
}
