package tui

import (
	"context"
	"math/rand/v2"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// markdownCoreSurvivorDocs are documents that a mutation of the block loop, the quote handling or the table layout of markdown.ts render turns into rows
// Pi does not render: each was found by comparing the rows of the mutated code with the unmutated code's over generated documents and then reduced.
var markdownCoreSurvivorDocs = []string{
	// A fence after a definition and a LaTeX start line, after an HTML comment, and quotes ending in a line of whitespace or a lazy indented line.
	"<!-->\n[0]:s\n```",
	">0\nn\n\t",
	">k\n)\n\t",
	"> #\n  ",
	"> #\n ",
	"> a\n\n \n",
	">\n \n> b",
	">1. y\n>  \n    e",
	"[ref0]:\"\n\\[6\\]\n[ref0]:s\n\n```",
	// A table delimiter row with no cells in the header row, and a table that overflows by one column width.
	"|\n-",
	"| --- | :-: |\n---\n",
	"- [2]:m\n  - y",
	"$x$|bb|[l]()|unbrokenwordthatislonger\n-|-|-|-",
	"b|b||unbrokenwordthatislonger\n-|-|-|-",
	"| $x$ | bb | [l](http://u.v) | unbrokenwordthatislonger |\n|-|--:|:-:|------------|\n\nafter\n",
	"| b | b |  | unbrokenwordthatislonger |\n|:-:|-|--:|---|\n\nafter\n",
	"| a | bb | x y | 日本語 |\n|---|---|:-:|------------|\n\nafter\n",
	"| [l](http://u.v) |   | long cell with many words in it |\n|------------|---|-|\n\nafter\n",
	"intro\n| 日本語 | $x$ |  | [l](http://u.v) | **b** | x y |\n|--:|-|--:|--:|--:|:--|\n",
	"|   |   | 🙂 | long cell with many words in it | $x$ |\n|:-:|------------|:-:|:-:|------------|\n",
	"| 日本語 | bb |   | bb |\n|------------|:--|-|:--|\n",
	"|   |   | b |\n|--:|---|------------|\n| a\\|b |   | 1234567890123456789012345678901234567 |   |\n| [l](http://u.v) |  | long cell with many words in it |\n| 日本語 | a\\|b | $x$ |\n",
}

// A quote that holds only a definition and ends in a line of spaces renders no rows of its own, at every width including the ones narrower than a border.
func TestMarkdownCoreQuoteOfDefinitionMatchesPi(t *testing.T) {
	var probes []markdownContractProbe
	for _, doc := range []string{"> [r]: u\n  ", "> [r]: u\n ", "> [r]: u\n\n  "} {
		for _, pad := range []int{0, 1} {
			probes = append(probes, markdownContractProbe{Text: doc, PaddingX: pad, PaddingY: pad, Theme: "tagged", Ops: markdownContractRenderOps(0, 1, 2, 3, 4, 5, 6, 8, 20, 80)})
		}
	}
	if failures := compareMarkdownContract(t, probes); failures > 0 {
		t.Errorf("%d of %d probes differ from Pi", failures, len(probes))
	}
}

func TestMarkdownCoreSurvivorDocsMatchPi(t *testing.T) {
	widths := make([]int, 0, 91)
	for w := 0; w <= 90; w++ {
		widths = append(widths, w)
	}
	var probes []markdownContractProbe
	for _, doc := range markdownCoreSurvivorDocs {
		for _, pad := range []int{0, 1} {
			probes = append(probes, markdownContractProbe{Text: doc, PaddingX: pad, PaddingY: pad, Theme: "tagged", Ops: markdownContractRenderOps(widths...)})
		}
	}
	if failures := compareMarkdownContract(t, probes); failures > 0 {
		t.Errorf("%d of %d probes differ from Pi", failures, len(probes))
	}
}

// TestMarkdownRandomOperationsEqualAFreshRender drives one component through a random sequence of the operations a caller has (a longer text, a replaced text, a
// render at another width, invalidate, a change of the active theme, a render after the text is finished) and requires every render to equal the render of a
// component built for the same text, width, constructor arguments and theme. This is the invariant of the cache, the block reuse and the resume points that
// markdown.ts keeps in cachedLines and a WeakRef (D220): they change how much is computed, never the rows.
func TestMarkdownRandomOperationsEqualAFreshRender(t *testing.T) {
	previous := GetCapabilities()
	SetCapabilities(TerminalCapabilities{TrueColor: true})
	t.Cleanup(func() {
		SetCapabilities(previous)
		SetTheme("dark")
	})
	widths := []int{0, 1, 3, 7, 12, 24, 40, 80}
	themes := []string{"dark", "light", "tagged"}
	transforms := []string{"", "", "", "append", "width"}
	rng := rand.New(rand.NewPCG(31, 41))
	for n := range 400 {
		var doc string
		if n%2 == 0 {
			doc = resumeGeneratedDocument(rng, n%4 == 0)
		} else {
			doc = markdownRandomDocs(uint64(n), 1)[0]
		}
		probe := markdownContractProbe{PaddingX: rng.IntN(3), PaddingY: rng.IntN(2), Theme: themes[rng.IntN(len(themes))], Highlight: rng.IntN(2) == 0}
		probe.Options.Transform = transforms[rng.IntN(len(transforms))]
		if rng.IntN(3) == 0 {
			probe.Style = &markdownContractStyle{Color: true, Bold: rng.IntN(2) == 0}
		}
		var log markdownContractLog
		SetTheme("dark")
		md := probe.build(&log)
		active := probe.Theme
		if active == "tagged" {
			active = "dark"
		}
		width := widths[rng.IntN(len(widths))]
		text, end := "", 0
		for step := range 60 {
			switch rng.IntN(12) {
			case 0:
				width = widths[rng.IntN(len(widths))]
			case 1:
				md.Invalidate()
			case 2:
				if probe.Theme != "tagged" {
					active = []string{"dark", "light"}[rng.IntN(2)]
					SetTheme(active)
				}
			case 3:
				if rng.IntN(4) == 0 {
					md.FinishStreaming()
				}
			case 4:
				end = rng.IntN(len(doc) + 1)
				text = doc[:end]
				md.SetText(text)
			default:
				end = min(len(doc), end+1+rng.IntN(1+rng.IntN(40)))
				text = doc[:end]
				md.SetText(text)
			}
			got := md.Render(width)
			want := probe
			want.Text = text
			if probe.Theme != "tagged" {
				want.Theme = active
			}
			var wantLog markdownContractLog
			wantRows := want.build(&wantLog).Render(width)
			if !slices.Equal(got, wantRows) {
				t.Fatalf("component %d step %d: text %q (%d bytes) at width %d, probe %+v theme %s:\n got %q\nwant %q", n, step, text, len(text), width, probe, active, got, wantRows)
			}
			if again := md.Render(width); !slices.Equal(again, got) {
				t.Fatalf("component %d step %d: a second render of the same text and width differs:\n got %q\nwant %q", n, step, again, got)
			}
		}
	}
}

// A negative width renders as width 0 (Pi throws a RangeError from " ".repeat(width)).
func TestMarkdownNegativeWidthRendersAsZero(t *testing.T) {
	for _, doc := range []string{"text", "# h\n\n- a\n- b", "```\ncode\n```", "| a |\n|-|\n| b |", "> q"} {
		for _, pad := range []int{0, 1} {
			for _, negative := range []int{-1, -2, -7} {
				want := NewMarkdown(doc)
				want.paddingX, want.paddingY = pad, pad
				got := NewMarkdown(doc)
				got.paddingX, got.paddingY = pad, pad
				if rows, zero := got.Render(negative), want.Render(0); !slices.Equal(rows, zero) {
					t.Errorf("%q pad %d width %d: %q, width 0 renders %q", doc, pad, negative, rows, zero)
				}
			}
		}
	}
}

// Render keeps its rows (markdown.ts cachedLines, cachedText, cachedWidth) until the text, the width, the transform's state, the active theme (when the
// component has none of its own), a text change or an invalidation, or the owner's async result changes. The transform runs once for each new render.
func TestMarkdownRenderCacheKey(t *testing.T) {
	t.Cleanup(func() { SetTheme("dark") })
	SetTheme("dark")
	calls, state := 0, "a"
	options := &MarkdownOptions{Transform: func(text string, _ int) string { calls++; return text }}
	md := NewMarkdownWithOptions("hello", 0, 0, nil, nil, options)
	md.TransformState = func() string { return state }
	steps := []struct {
		name  string
		apply func()
		width int
		calls int
	}{
		{"first render", func() {}, 40, 1},
		{"same input", func() {}, 40, 1},
		{"content assigned", func() { md.Content = "other" }, 40, 2},
		{"same content again", func() {}, 40, 2},
		{"another width", func() {}, 41, 3},
		{"transform state", func() { state = "b" }, 41, 4},
		{"same state again", func() {}, 41, 4},
		{"invalidate", md.Invalidate, 41, 5},
		{"set text", func() { md.SetText("third") }, 41, 6},
		{"active theme", func() { SetTheme("light") }, 41, 7},
		{"same theme again", func() {}, 41, 7},
	}
	for _, step := range steps {
		step.apply()
		md.Render(step.width)
		if calls != step.calls {
			t.Fatalf("%s: the transform ran %d times, want %d", step.name, calls, step.calls)
		}
	}
	// A component with its own theme does not follow the active theme.
	own := GetMarkdownTheme()
	calls = 0
	owned := NewMarkdownWithOptions("hello", 0, 0, &own, nil, options)
	owned.Render(40)
	SetTheme("dark")
	owned.Render(40)
	if calls != 1 {
		t.Fatalf("a component with its own theme rendered again after the active theme changed (%d transform calls)", calls)
	}
	// A blank text renders to a non-nil empty slice, at width 0 as at any other, and a second render returns the same rows.
	for _, text := range []string{"", "  \n"} {
		blank := NewMarkdown(text)
		first := blank.Render(0)
		if first == nil || len(first) != 0 {
			t.Fatalf("blank text %q: %#v, want an empty non-nil slice", text, first)
		}
	}
}

// FinishStreaming drops everything a streamed render kept for the next delta, whether it is called before or after the last render.
func TestMarkdownFinishStreamingDropsTheMemos(t *testing.T) {
	pauseGC(t) // the memos are inspected between renders, as a WeakRef target is kept until its job ends
	text := "# heading\n\nparagraph with **bold** and a very long line that wraps when it is narrow enough to need it\n\n```go\ncode\n```\n\nlast"
	for _, before := range []bool{false, true} {
		md := NewMarkdown(text)
		if before {
			md.FinishStreaming()
		}
		md.Render(20)
		if !before {
			if memosOf(md).resume == nil && memosOf(md).wraps == nil && memosOf(md).inlines == nil {
				t.Fatal("a streaming render kept no memos, so the test proves nothing")
			}
			md.FinishStreaming()
		}
		if memosOf(md).resume != nil || memosOf(md).wraps != nil || memosOf(md).nextWraps != nil || memosOf(md).inlines != nil || memosOf(md).nextInlines != nil || memosOf(md).highlights != nil || memosOf(md).nextHighlights != nil {
			t.Fatalf("finished (before the render: %v) but memos remain: resume %v wraps %v inlines %v highlights %v", before, memosOf(md).resume != nil, memosOf(md).wraps != nil, memosOf(md).inlines != nil, memosOf(md).highlights != nil)
		}
		// A render after the last text change resumes from the memos once and drops them again.
		md.SetText(text + "\n\nmore")
		md.Render(20)
		if memosOf(md).resume != nil || memosOf(md).wraps != nil || memosOf(md).inlines != nil || memosOf(md).highlights != nil {
			t.Fatalf("memos kept by a render after FinishStreaming (before the render: %v)", before)
		}
	}
}

// While an async rewrite is pending, a render returns the frame completed at the same width and none at another width.
func TestMarkdownAsyncPendingFrameIsKeptOnlyAtItsWidth(t *testing.T) {
	var workers sync.WaitGroup
	release := make(chan struct{})
	blocked := atomic.Bool{}
	md := NewMarkdown("first")
	md.AsyncTransform = &AsyncMarkdownTransform{
		Context: t.Context(), Start: workers.Go,
		Prepare: func(text string, _ int) func(context.Context) string {
			return func(context.Context) string {
				if blocked.Load() {
					<-release
				}
				return text + " done"
			}
		},
	}
	if rows := md.Render(40); rows != nil {
		t.Fatalf("a render before the first rewrite completed: %q", rows)
	}
	workers.Wait()
	done := md.Render(40)
	if len(done) != 1 || !strings.HasPrefix(done[0], "first done") {
		t.Fatalf("completed render: %q", done)
	}
	blocked.Store(true)
	md.Content = "second"
	if rows := md.Render(40); !slices.Equal(rows, done) {
		t.Fatalf("pending render at the completed width: %q, want the completed frame %q", rows, done)
	}
	if rows := md.Render(30); rows != nil {
		t.Fatalf("pending render at another width: %q, want none", rows)
	}
	close(release)
	workers.Wait()
	if rows := md.Render(30); len(rows) != 1 || !strings.HasPrefix(rows[0], "second done") {
		t.Fatalf("render after the rewrite completed: %q", rows)
	}
}
