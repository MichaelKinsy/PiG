package tui

import (
	"fmt"
	"maps"
	"math/rand/v2"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// The memos a Markdown keeps between renders (the inline renderings, the highlighted code, the wraps and the resume point) change how much a render computes and
// never what it returns, so the state a streamed component holds after a render is a function of the text and the arguments alone: it equals the state of a new
// component given the same text, except that a render that resumed did not render the blocks before its point and so holds only the entries of the blocks after it.
// These tests drive the component through the operations a caller has and compare that state with a new component's.

// markdownResumeSnapshot is what a render keeps to resume the next one, without the position of a point in its state and the output it holds a view of.
type markdownResumeSnapshot struct {
	key      resumeKey
	raw      string
	stable   int
	out      []string
	rows     []string
	rowStart []int
	points   []resumePoint
}

// markdownResumeOf returns the snapshot of m's resume state, or false when the component keeps none. It fails the test when a kept point holds a view of the output.
func markdownResumeOf(t *testing.T, m *Markdown) (markdownResumeSnapshot, bool) {
	t.Helper()
	r := memosOf(m).resume
	if r == nil {
		return markdownResumeSnapshot{}, false
	}
	snapshot := markdownResumeSnapshot{key: r.key, raw: r.raw, stable: r.stable, out: slices.Clone(r.out), rows: slices.Clone(r.rows), rowStart: slices.Clone(r.rowStart)}
	if r.points == nil {
		t.Fatalf("resume state with a nil point list for %q", m.Content)
	}
	snapshot.points = []resumePoint{}
	for i, p := range r.points {
		if p.prefix != nil {
			t.Fatalf("resume point %d of %q holds a view of the output it was picked with", i, m.Content)
		}
		p.index = 0
		snapshot.points = append(snapshot.points, p)
	}
	return snapshot, true
}

// markdownResumeSnapshotDifference names what differs between a kept state and a new component's, or returns "" when the kept state is the one a render of the
// text keeps. The points of a resumed render may be more than a new component's: a link definition that arrives after a point leaves the points before it,
// as the render found no reference they could resolve, while a render of the whole text, which knows the definition from its first block, records none. Every
// point must still be what its line holds, and when a new component records points the kept ones are equal to them.
func markdownResumeSnapshotDifference(a, b markdownResumeSnapshot) string {
	pointEqual := func(x, y resumePoint) bool {
		return x.line == y.line && x.rawOff == y.rawOff && x.outLen == y.outLen && x.lexedBracket == y.lexedBracket && x.flags == y.flags
	}
	switch {
	case a.key != b.key:
		return "key"
	case a.raw != b.raw:
		return "raw"
	case a.stable != b.stable:
		return fmt.Sprintf("stable %d, %d", a.stable, b.stable)
	case !slices.Equal(a.out, b.out):
		return "out"
	case !slices.Equal(a.rows, b.rows):
		return "rows"
	case !slices.Equal(a.rowStart, b.rowStart):
		return "rowStart"
	case len(a.rowStart) != len(a.out)+1 || a.rowStart[len(a.out)] != len(a.rows):
		return "rowStart does not end at the rows"
	case len(b.points) > 0 && !slices.EqualFunc(a.points, b.points, pointEqual):
		return fmt.Sprintf("points\n  %+v\n  %+v", a.points, b.points)
	}
	for i, p := range a.points {
		source := normalizeSource(a.raw[:p.rawOff])
		switch {
		case p.rawOff <= 0 || p.rawOff > a.stable || a.raw[p.rawOff-1] != '\n' || a.raw[p.rawOff] == '\n':
			return fmt.Sprintf("point %d at %d is not the start of a block (stable %d)", i, p.rawOff, a.stable)
		case p.line != strings.Count(source, "\n"):
			return fmt.Sprintf("point %d at %d is on line %d, the text has %d lines before it", i, p.rawOff, p.line, strings.Count(source, "\n"))
		case i > 0 && (p.rawOff <= a.points[i-1].rawOff || p.outLen < a.points[i-1].outLen):
			return fmt.Sprintf("point %d does not follow point %d", i, i-1)
		case p.outLen > len(a.out):
			return fmt.Sprintf("point %d has %d lines before it of %d", i, p.outLen, len(a.out))
		}
	}
	return ""
}

// requireMarkdownStateOfAFreshComponent fails unless m, which has just rendered its text at width, holds what a new component given the same text and arguments holds.
// subset is true when m may have resumed: its inline renderings and highlighted code then hold the entries of the blocks it rendered, a subset of the new component's.
func requireMarkdownStateOfAFreshComponent(t *testing.T, label string, m *Markdown, width int, subset bool, paddingX, paddingY int) {
	t.Helper()
	fresh := NewMarkdownWithOptions(m.Content, paddingX, paddingY, nil, nil, nil)
	want := fresh.Render(width)
	if got := m.Render(width); !slices.Equal(got, want) {
		t.Fatalf("%s: rows of %q at width %d:\n got %q\nwant %q", label, m.Content, width, got, want)
	}
	gotResume, gotOK := markdownResumeOf(t, m)
	wantResume, wantOK := markdownResumeOf(t, fresh)
	if difference := markdownResumeSnapshotDifference(gotResume, wantResume); gotOK != wantOK || gotOK && difference != "" {
		t.Fatalf("%s: resume state of %q at width %d: kept %v, a new component keeps %v; differs in %s", label, m.Content, width, gotOK, wantOK, difference)
	}
	if widthx.JSTrim(m.Content) == "" {
		// A blank text renders no blocks and leaves the memos of the render before it for the next one.
		return
	}
	if len(memosOf(m).nextInlines) != 0 || len(memosOf(m).nextHighlights) != 0 || len(memosOf(m).nextWraps) != 0 {
		t.Fatalf("%s: %q keeps scratch entries after a render (%d inline renderings, %d highlighted blocks, %d wraps)", label, m.Content, len(memosOf(m).nextInlines), len(memosOf(m).nextHighlights), len(memosOf(m).nextWraps))
	}
	if m.inlinesOn || m.highlightsOn {
		t.Fatalf("%s: %q still fills its memos after a render", label, m.Content)
	}
	if memosOf(m).inlinesContext != memosOf(fresh).inlinesContext || memosOf(m).highlightsEpoch != memosOf(fresh).highlightsEpoch {
		t.Fatalf("%s: %q keeps the context of another render", label, m.Content)
	}
	for key, rendering := range memosOf(m).inlines {
		// A key a new component does not hold is one of a lexer state the new component's render never enters: a document whose link definition
		// arrives after a reference is rendered twice, the second time from another state.
		if freshRendering, ok := memosOf(fresh).inlines[key]; ok && freshRendering != rendering || !ok && !subset {
			t.Fatalf("%s: inline rendering of %q in %q is %v, a new component holds %v (%v)", label, key.src, m.Content, rendering, freshRendering, ok)
		}
	}
	for key, lines := range memosOf(m).highlights {
		if freshLines, ok := memosOf(fresh).highlights[key]; !ok || !slices.Equal(freshLines, lines) {
			t.Fatalf("%s: highlighted code %q in %q is %q, a new component holds %q (%v)", label, key.code, m.Content, lines, freshLines, ok)
		}
	}
	for line, wrapped := range memosOf(m).wraps {
		if freshWrapped, ok := memosOf(fresh).wraps[line]; !ok || !slices.Equal(freshWrapped, wrapped) {
			t.Fatalf("%s: wrap of %q in %q is %q, a new component holds %q (%v)", label, line, m.Content, wrapped, freshWrapped, ok)
		}
	}
	if !subset {
		if !maps.EqualFunc(memosOf(m).inlines, memosOf(fresh).inlines, func(a, b inlineRendering) bool { return a == b }) {
			var missing, extra []inlineKey
			for key := range memosOf(fresh).inlines {
				if _, ok := memosOf(m).inlines[key]; !ok {
					missing = append(missing, key)
				}
			}
			for key := range memosOf(m).inlines {
				if _, ok := memosOf(fresh).inlines[key]; !ok {
					extra = append(extra, key)
				}
			}
			t.Fatalf("%s: inline renderings of %q hold %d entries, a new component holds %d; missing %+v, extra %+v", label, m.Content, len(memosOf(m).inlines), len(memosOf(fresh).inlines), missing, extra)
		}
		if !maps.EqualFunc(memosOf(m).highlights, memosOf(fresh).highlights, slices.Equal[[]string]) {
			t.Fatalf("%s: highlighted code of %q holds %d blocks, a new component holds %d", label, m.Content, len(memosOf(m).highlights), len(memosOf(fresh).highlights))
		}
		if !maps.EqualFunc(memosOf(m).wraps, memosOf(fresh).wraps, slices.Equal[[]string]) {
			t.Fatalf("%s: wraps of %q hold %d lines, a new component holds %d", label, m.Content, len(memosOf(m).wraps), len(memosOf(fresh).wraps))
		}
	}
}

// TestMarkdownStreamedStateEqualsAFreshComponents streams random documents in chunks of a few bytes, replacing the text, changing the width, invalidating and
// blanking it now and then, and requires the state after every render to equal a new component's (see requireMarkdownStateOfAFreshComponent).
func TestMarkdownStreamedStateEqualsAFreshComponents(t *testing.T) {
	pauseGC(t) // the memos are inspected between renders, as a WeakRef target is kept until its job ends
	mdCaseSetup(t)
	rng := rand.New(rand.NewPCG(31, 41))
	documents := 120
	if testing.Short() {
		documents = 30
	}
	for index := range documents {
		document := resumeGeneratedDocument(rng, index%2 == 1)
		if index%5 == 4 {
			document = resumeDependentDocuments[rng.IntN(len(resumeDependentDocuments))]
		}
		paddingX, paddingY := rng.IntN(3), rng.IntN(2)
		width := 10 + rng.IntN(60)
		streamed := NewMarkdownWithOptions("", paddingX, paddingY, nil, nil, nil)
		for end := 0; end < len(document); {
			end = min(len(document), end+1+rng.IntN(1+rng.IntN(40)))
			text := document[:end]
			label := "extend"
			switch rng.IntN(30) {
			case 0:
				text, label = document[:rng.IntN(end+1)]+"REPLACED\n\n"+document[:end], "replace"
			case 1:
				width, label = width+1+rng.IntN(40), "width"
			case 2:
				streamed.Invalidate()
				label = "invalidate"
			case 3:
				text, label = " \n\t\n", "blank"
			}
			// A render resumes when the kept state is for this render's arguments and keeps the lines of the new text.
			key, keyed := streamed.resumeKey(width, max(1, width-2*paddingX))
			resumes := memosOf(streamed).resume != nil && keyed && memosOf(streamed).resume.key == key && memosOf(streamed).resume.pick(text) != nil
			streamed.SetText(text)
			// A new component is the reference for every state but the memos of a resumed render, which keep fewer entries.
			requireMarkdownStateOfAFreshComponent(t, label, streamed, width, resumes, paddingX, paddingY)
		}
	}
}

// TestMarkdownFinishedStateKeepsNoMemos requires a component that finished streaming to hold no memo after any render, whatever the text and the operations before it.
func TestMarkdownFinishedStateKeepsNoMemos(t *testing.T) {
	pauseGC(t) // the memos are inspected between renders, as a WeakRef target is kept until its job ends
	mdCaseSetup(t)
	rng := rand.New(rand.NewPCG(5, 6))
	for index := range 40 {
		document := resumeGeneratedDocument(rng, index%2 == 1)
		m := NewMarkdown("")
		for end := 0; end < len(document); end += 1 + rng.IntN(200) {
			m.SetText(document[:end])
			m.Render(40)
		}
		m.SetText(document)
		if index%2 == 0 {
			m.Render(40)
			m.FinishStreaming()
		} else {
			m.FinishStreaming()
			m.Render(40)
		}
		m.SetText(document + "\n\nmore")
		m.Render(50)
		if memosOf(m).resume != nil || len(memosOf(m).inlines) != 0 || len(memosOf(m).nextInlines) != 0 || len(memosOf(m).highlights) != 0 || len(memosOf(m).nextHighlights) != 0 || len(memosOf(m).wraps) != 0 || len(memosOf(m).nextWraps) != 0 {
			t.Fatalf("a finished component keeps memos of %q: resume %v, %d inline renderings, %d highlighted blocks, %d wraps", document, memosOf(m).resume != nil, len(memosOf(m).inlines), len(memosOf(m).highlights), len(memosOf(m).wraps))
		}
	}
}

// A paragraph of a Markdown with a link definition renders the same text differently when the definition changes, so the renderings of text with a "[" are not kept
// across renders of documents that define links. Every pair below has one paragraph that a definition of the first text resolves and the second does not or
// resolves to another target.
func TestMarkdownInlineMemoIsNotUsedForReferencesAcrossDefinitions(t *testing.T) {
	mdCaseSetup(t)
	pairs := [][2]string{
		{"[r]: http://a.b\n\nsee [r] here", "[r]: http://c.d\n\nsee [r] here"},
		{"[r]: http://a.b\n\nsee [r] here", "see [r] here"},
		{"see [r] here", "[r]: http://a.b\n\nsee [r] here"},
		{"[r]: http://a.b\n[s]: http://e.f\n\nsee [r] and [s]", "[r]: http://a.b\n[s]: http://g.h\n\nsee [r] and [s]"},
		{"[r]: http://a.b\n[s]: http://e.f\n\nsee [r] and [s]", "[r]: http://a.b\n\nsee [r] and [s]"},
		{"[r]: http://a.b\n\nsee [r] here\n\nand [r] again", "[r]: http://c.d\n\nsee [r] here\n\nand [r] again"},
		{"> [r]: http://a.b\n\nsee [r] here", "> [r]: http://c.d\n\nsee [r] here"},
		{"- [r]: http://a.b\n- item\n\nsee [r] here", "- [r]: http://c.d\n- item\n\nsee [r] here"},
		{"[r]: http://a.b\n\n| [r] | b |\n|---|---|\n| [r] | c |", "[r]: http://c.d\n\n| [r] | b |\n|---|---|\n| [r] | c |"},
		{"[r]: http://a.b\n\n- see [r]\n\n> see [r]", "[r]: http://c.d\n\n- see [r]\n\n> see [r]"},
	}
	for _, pair := range pairs {
		for _, finish := range []bool{false, true} {
			m := NewMarkdown(pair[0])
			if finish {
				m.FinishStreaming()
			}
			m.Render(40)
			m.SetText(pair[1])
			got := m.Render(40)
			want := NewMarkdown(pair[1]).Render(40)
			if !slices.Equal(got, want) {
				t.Errorf("%q then %q (finished %v):\n got %q\nwant %q", pair[0], pair[1], finish, got, want)
			}
			m.Invalidate()
			m.SetText(strings.TrimSuffix(pair[0], "\n"))
			if got, want := m.Render(40), NewMarkdown(pair[0]).Render(40); !slices.Equal(got, want) {
				t.Errorf("%q after %q:\n got %q\nwant %q", pair[0], pair[1], got, want)
			}
		}
	}
}
