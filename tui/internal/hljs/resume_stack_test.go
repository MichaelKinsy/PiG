package hljs

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"unsafe"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// openDocuments are texts that one mode holds open from their first lines to their last, so a run passes no point between top-level nodes before their end: a JSON object, a JSON list, and a Go comment.
func openDocuments() []streamedSnippet {
	var object, list, comment strings.Builder
	object.WriteString("{\n  \"items\": [\n")
	list.WriteString("[\n")
	comment.WriteString("package demo\n\n/*\n")
	for n := 1; n <= 40; n++ {
		fmt.Fprintf(&object, "    {\n      \"id\": %d,\n      \"name\": \"item %d\",\n      \"tags\": [\"a\", \"b\"],\n      \"ok\": true\n    },\n", n, n)
		fmt.Fprintf(&list, "  [%d, \"item %d\", [\"a\", \"b\"], true, null],\n", n, n)
		fmt.Fprintf(&comment, "   Line %d: the parser reads one token, and you will see it keep a small stack.\n", n)
	}
	object.WriteString("    {}\n  ]\n}\n")
	list.WriteString("  []\n]\n")
	comment.WriteString("*/\nvar x = 1\n")
	return []streamedSnippet{{"object", "json", object.String()}, {"list", "json", list.String()}, {"comment", "go", comment.String()}}
}

// TestADocumentThatStaysOpenIsNotReadAgainAtEveryFrame: a text that one mode holds open (a JSON document is one object or one list) streams as any other does. A frame resumes inside the open modes, a few lines before its end, so the units handed to the engine grow with the text and not with its square. Every frame has the HTML of a whole run.
func TestADocumentThatStaysOpenIsNotReadAgainAtEveryFrame(t *testing.T) {
	r := NewRegistry()
	r.LoadAllLanguages()
	for _, doc := range openDocuments() {
		for _, schedule := range []struct {
			name string
			cuts []int
		}{
			{"a frame per line", lineCuts(doc.code)},
			{"frames of 175 bytes", stepCuts(doc.code, streamedFrameBytes)},
		} {
			var stats frameStats
			if !checkFrames(t, r, doc.name+", "+schedule.name, doc.lang, doc.code, schedule.cuts, &stats) {
				continue
			}
			// A frame reads what it added, and again the lines a pattern before its resume point could have read (resumeLimit): two lines and a part of one, under 200 units here.
			frames, engine, text := stats.frames.Load(), stats.engine.Load(), int64(len(doc.code))
			if most := 2*text + 200*frames; engine > most {
				t.Errorf("%s (%s), %s: %d units handed to the engine for %d units of text in %d frames; want at most %d", doc.name, doc.lang, schedule.name, engine, text, frames, most)
			}
		}
	}
}

// stackSamples are the corpus samples and the snippets, every fourth under the race detector.
func stackSamples(t *testing.T) []corpusSample {
	t.Helper()
	all := distinctSamples(t)
	for _, s := range streamedSnippets {
		all = append(all, corpusSample{ID: "snippet " + s.name, Lang: s.lang, Code: s.text()})
	}
	if !raceEnabled {
		return all
	}
	var samples []corpusSample
	for i, c := range all {
		if i%4 == 0 {
			samples = append(samples, c)
		}
	}
	return samples
}

// openSpans is the classes of the spans open at the end of html, outermost first.
func openSpans(html string) []string {
	const tag = `<span class="`
	var open []string
	for at := 0; at < len(html); {
		switch {
		case strings.HasPrefix(html[at:], tag):
			end := at + strings.IndexByte(html[at:], '>')
			open = append(open, html[at+len(tag):end-1])
			at = end + 1
		case strings.HasPrefix(html[at:], "</span>"):
			open = open[:len(open)-1]
			at += len("</span>")
		default:
			at++
		}
	}
	return open
}

// TestARunResumedAtACheckpointContinuesTheWholeRun: a run that starts at a checkpoint of a whole run, on the stack and inside the nodes the checkpoint holds, writes the rest of the whole run's HTML. Every checkpoint of every corpus sample and snippet is resumed, those inside open modes included, in every language: the text is the same, so what the language's patterns read past a point takes no part. The nodes a resumed run reopens have the classes of the spans open in the whole run's HTML at the checkpoint, with the language's aliases; highlight.js 10.7.3 writes a class name as it stands (it has no scope of several classes, and no beginScope or endScope), so a node has one class.
func TestARunResumedAtACheckpointContinuesTheWholeRun(t *testing.T) {
	var mu sync.Mutex
	inside, aliased := map[string]int{}, map[string]int{}
	var checked atomic.Int64
	parallelEach(t, stackSamples(t), func() (func(corpusSample), func(int)) {
		r := NewRegistry()
		r.LoadAllLanguages()
		return func(c corpusSample) {
			if !r.SupportsLanguage(c.Lang) {
				return
			}
			units := stringToUnits(c.Code)
			whole := r.highlightFrom(c.Lang, units, true, nil, checkpoint{}, recordStack)
			if whole.failed {
				return
			}
			html, offsets, placed := whole.emitter.checkpointHTML(whole.checkpoints)
			if !placed || jsstring.Canonical(html) != whole.Value {
				t.Errorf("%s: the walk placed %d of %d checkpoints, and its HTML is the run's: %v", c.ID, len(offsets), len(whole.checkpoints), jsstring.Canonical(html) == whole.Value)
				return
			}
			aliases := r.getLanguage(c.Lang).lang().classNameAliases
			open, alias := 0, 0
			for i, point := range whole.checkpoints {
				resumed := r.highlightFrom(c.Lang, units, true, nil, point, recordNone)
				if resumed.failed {
					t.Errorf("%s: the run resumed at unit %d failed", c.ID, point.unit)
					return
				}
				rest, _, _ := resumed.emitter.checkpointHTML(nil)
				if got := html[:offsets[i]] + rest; got != html {
					at := 0
					for at < len(got) && at < len(html) && got[at] == html[at] {
						at++
					}
					t.Errorf("%s (%s): the run resumed at unit %d (HTML byte %d) does not write the rest of the whole run; they differ at HTML byte %d\n got %q\nwant %q", c.ID, c.Lang, point.unit, offsets[i], at, htmlWindow(got, at), htmlWindow(html, at))
					return
				}
				if point.top == nil {
					continue
				}
				open++
				var reopened []string
				node := resumed.emitter.root
				for range resumed.emitter.continued {
					node = node.children[0].node
					reopened = append(reopened, "hljs-"+node.kind)
				}
				if spans := openSpans(html[:offsets[i]]); !slices.Equal(reopened, spans) {
					t.Errorf("%s (%s): the run resumed at unit %d reopened the nodes %q; the spans open there are %q", c.ID, c.Lang, point.unit, reopened, spans)
					return
				}
				for f := point.top; !f.language; f = f.parent {
					if aliases[f.mode.className] != "" {
						alias++
						break
					}
				}
			}
			checked.Add(int64(len(whole.checkpoints)))
			mu.Lock()
			inside[c.Lang] += open
			aliased[c.Lang] += alias
			mu.Unlock()
		}, nil
	})
	if t.Failed() {
		return
	}
	total := 0
	for _, n := range inside {
		total += n
	}
	t.Logf("%d checkpoints resumed, %d of them inside open modes", checked.Load(), total)
	for _, lang := range slices.Sorted(maps.Keys(aliased)) {
		if aliased[lang] > 0 {
			t.Logf("%s: %d checkpoints inside a mode whose class is an alias", lang, aliased[lang])
		}
	}
	if raceEnabled {
		return
	}
	for _, lang := range []string{"json", "css", "python", "go", "bash", "xml", "sql", "javascript", "typescript"} {
		if inside[lang] == 0 {
			t.Errorf("no checkpoint of %s lies inside an open mode: its samples no longer exercise the saved stack", lang)
		}
	}
}

// TestALanguageWithTextChecksTakesNoPointInsideAMode: the text checks that bound the patterns of javascript and typescript (guardedSources and the callback of xmlTagBegin) read the text after a point as it stands between top-level nodes. Every language that has such a check (reach.guards), whatever its name, is offered those points only; the others are offered points inside open modes as well.
func TestALanguageWithTextChecksTakesNoPointInsideAMode(t *testing.T) {
	var mu sync.Mutex
	checked := map[string]int{}
	var inside atomic.Int64
	parallelEach(t, stackSamples(t), func() (func(corpusSample), func(int)) {
		r := NewRegistry()
		r.LoadAllLanguages()
		return func(c corpusSample) {
			if !r.SupportsLanguage(c.Lang) {
				return
			}
			_, points, err := r.HighlightResumable(NewText(c.Code), c.Lang, true, ResumePoint{})
			if err != nil {
				return
			}
			open := 0
			for _, point := range points {
				if point.top != nil {
					open++
				}
			}
			if languageReach(compileLanguage(r.getLanguage(c.Lang))).guards == 0 {
				inside.Add(int64(open))
				return
			}
			if open > 0 {
				t.Errorf("%s (%s): %d of %d points lie inside a mode, where the text checks of the language do not hold", c.ID, c.Lang, open, len(points))
			}
			mu.Lock()
			checked[c.Lang] += len(points)
			mu.Unlock()
		}, nil
	})
	t.Logf("languages with text checks and their points: %v; points inside modes of the other languages: %d", checked, inside.Load())
	for _, lang := range []string{"javascript", "typescript"} {
		if !t.Failed() && checked[lang] == 0 {
			t.Errorf("%s has no text check or was offered no point: the test no longer covers it", lang)
		}
	}
	if !t.Failed() && inside.Load() == 0 {
		t.Error("no language without text checks was offered a point inside a mode")
	}
}

// TestAStackOfAnotherRegistryOrLanguageIsNotContinued: a point inside open modes holds frames of the Registry and the language it was recorded in. HighlightResumable refuses it for any other (ErrResumeFailed), and a stream that moves to another Registry starts again there: every frame has the HTML of a whole run.
func TestAStackOfAnotherRegistryOrLanguageIsNotContinued(t *testing.T) {
	first, second := NewRegistry(), NewRegistry()
	first.LoadAllLanguages()
	second.LoadAllLanguages()
	doc := openDocuments()[0]
	cuts := lineCuts(doc.code)
	_, points, err := first.HighlightResumable(NewText(doc.code[:cuts[len(cuts)/2]]), doc.lang, true, ResumePoint{})
	if err != nil || len(points) == 0 || points[len(points)-1].top == nil {
		t.Fatalf("half of the document offers no point inside its open modes: %d points, %v", len(points), err)
	}
	point := points[len(points)-1]
	if _, _, err := second.HighlightResumable(NewText(doc.code), doc.lang, true, point); !errors.Is(err, ErrResumeFailed) {
		t.Errorf("another Registry continued the stack: %v; want ErrResumeFailed", err)
	}
	if _, _, err := first.HighlightResumable(NewText(doc.code), "css", true, point); !errors.Is(err, ErrResumeFailed) {
		t.Errorf("another language continued the stack: %v; want ErrResumeFailed", err)
	}
	if _, _, err := first.HighlightResumable(NewText(doc.code), doc.lang, true, point); err != nil {
		t.Errorf("the Registry that offered the point did not continue it: %v", err)
	}
	chain := frameChain{r: first, lang: doc.lang}
	for i, cut := range cuts {
		moved := i == len(cuts)/2
		if moved {
			if chain.point.top == nil {
				t.Fatalf("the frame before the move left no point inside open modes")
			}
			chain.r = second
		}
		text := doc.code[:cut]
		want, err := second.Highlight(text, doc.lang, true)
		got, ok := chain.frame(text)
		if err != nil || !ok || got != want {
			t.Fatalf("the frame of %d bytes (moved: %v) is not the HTML of a whole run: ok=%v, %v", cut, moved, ok, err)
		}
		if moved && chain.resumedAt != 0 {
			t.Fatalf("the frame after the move resumed at byte %d of a stack of the other Registry", chain.resumedAt)
		}
	}
}

// nestedLists is a JSON document of lists inside lists, levels deep, a level to a line.
func nestedLists(levels int) string {
	var doc strings.Builder
	for n := range levels {
		fmt.Fprintf(&doc, "[\"level %d\",\n", n)
	}
	doc.WriteString("\"deepest\"\n")
	for range levels {
		doc.WriteString("]\n")
	}
	return doc.String()
}

// TestAPointHoldsAtMostResumeStackMaxFrames: what a resume point keeps of the run is its stack, a frame of four words for each open mode, and a point is taken only where the stack has at most ResumeStackMax modes. A document nested deeper is offered points up to that depth and none below it, and streams with the HTML of a whole run at every frame.
func TestAPointHoldsAtMostResumeStackMaxFrames(t *testing.T) {
	if got, want := unsafe.Sizeof(frame{}), 4*unsafe.Sizeof(uintptr(0)); got != want {
		t.Errorf("a frame is %d bytes; want four words, %d", got, want)
	}
	r := NewRegistry()
	r.LoadAllLanguages()
	const levels = 100
	code := nestedLists(levels)
	_, points, err := r.HighlightResumable(NewText(code), "json", true, ResumePoint{})
	if err != nil {
		t.Fatal(err)
	}
	deepest := 0
	for _, point := range points {
		frames := 0
		for f := point.top; f != nil && !f.language; f = f.parent {
			frames++
		}
		deepest = max(deepest, frames)
	}
	// A list is two modes: the list and the value inside it.
	if deepest > ResumeStackMax || deepest < ResumeStackMax-1 {
		t.Errorf("the deepest stack among the %d points of %d nested lists has %d frames; want the bound, %d", len(points), levels, deepest, ResumeStackMax)
	}
	var stats frameStats
	checkFrames(t, r, "nested lists, a frame per line", "json", code, lineCuts(code), &stats)
}

// TestStreamsInTurnsDoNotShareWhatAModeKeeps: the callbacks of END_SAME_AS_BEGIN keep the delimiter of a here document on the mode object, which every run of a Registry shares. Two here documents with different delimiters, each with the other's delimiter on a line of its body, stream in turns through one Registry. No frame resumes inside either, where the delimiter the other left would end it: every frame has the HTML of a whole run.
func TestStreamsInTurnsDoNotShareWhatAModeKeeps(t *testing.T) {
	r := NewRegistry()
	r.LoadAllLanguages()
	document := func(delimiter, other string) string {
		return "cat <<" + delimiter + "\nfirst $a line\n" + other + "\nsecond $b line\nthird $c line\nfourth $d line\nfifth $e line\nsixth $f line\n" + delimiter + "\necho done\necho more\n"
	}
	texts := [2]string{document("EOF", "END"), document("END", "EOF")}
	chains := [2]frameChain{{r: r, lang: "bash"}, {r: r, lang: "bash"}}
	cuts := [2][]int{lineCuts(texts[0]), lineCuts(texts[1])}
	for i := range cuts[0] {
		var wants [2]string
		for n := range texts {
			want, err := r.Highlight(texts[n][:cuts[n][i]], "bash", true)
			if err != nil {
				t.Fatal(err)
			}
			wants[n] = want
		}
		// The whole run of the second text ran last, so the mode holds its delimiter when the first text's frame runs.
		for n := range texts {
			if got, ok := chains[n].frame(texts[n][:cuts[n][i]]); !ok || got != wants[n] {
				t.Fatalf("text %d, the frame of %d lines, resumed at byte %d, is not the HTML of a whole run:\n got %q\nwant %q", n, i+1, chains[n].resumedAt, got, wants[n])
			}
		}
	}
	if chains[0].resumed == 0 {
		t.Fatalf("no frame of the first text resumed: the texts no longer exercise resumption")
	}
}

// aliasedLanguage is a language whose classes are aliases, each a class with a dot, on modes that stay open over lines with modes inside them. No bundled grammar has one: the aliased classes of arduino, c-like, cpp, css, mathematica and vbnet are of keywords and of modes that end where they begin, which no checkpoint lies inside.
func aliasedLanguage() *Mode {
	word := &Mode{present: keyBegin | keyClassName, begin: regExpPattern(`[a-z]+`), className: "word"}
	group := &Mode{present: keyBegin | keyEnd | keyClassName | keyContains, begin: regExpPattern(`\(`), end: regExpPattern(`\)`), className: "group.inner", contains: []modeRef{{mode: word}}}
	block := &Mode{present: keyBegin | keyEnd | keyClassName | keyContains, begin: regExpPattern(`<<`), end: regExpPattern(`>>`), className: "block.outer", contains: []modeRef{{mode: group}, {mode: word}}}
	return &Mode{
		present:  keyClassNameAliases | keyContains,
		language: &languageProps{classNameAliases: map[string]string{"block.outer": "section", "group.inner": "subst"}},
		contains: []modeRef{{mode: block}},
	}
}

// TestReopenedNodesTakeTheAliasesOfTheirClasses: a run that continues inside modes whose classes are aliases reopens nodes of the aliases, as startNewMode opened them, the outermost first. A class with a dot is one class: highlight.js 10.7.3 writes a class name as it stands. Every prefix of the text streams with the HTML of a whole run.
func TestReopenedNodesTakeTheAliasesOfTheirClasses(t *testing.T) {
	r := NewRegistry()
	r.registerLanguage("aliased", aliasedLanguage())
	code := "start\n<<\nalpha (beta\ngamma delta) epsilon\nzeta (eta)\ntheta\n>>\nend one\nend two\n"
	units := stringToUnits(code)
	whole := r.highlightFrom("aliased", units, true, nil, checkpoint{}, recordStack)
	html, offsets, placed := whole.emitter.checkpointHTML(whole.checkpoints)
	if whole.failed || !placed {
		t.Fatalf("the whole run failed (%v) or its checkpoints have no place in its HTML (%v)", whole.failed, !placed)
	}
	if !strings.Contains(html, `<span class="hljs-section">`) || !strings.Contains(html, `<span class="hljs-subst">`) || strings.Contains(html, "block.outer") {
		t.Fatalf("the whole run does not write the aliases of its classes: %q", html)
	}
	seen := map[string]bool{}
	for i, point := range whole.checkpoints {
		resumed := r.highlightFrom("aliased", units, true, nil, point, recordNone)
		rest, _, _ := resumed.emitter.checkpointHTML(nil)
		if resumed.failed || html[:offsets[i]]+rest != html {
			t.Fatalf("the run resumed at unit %d does not write the rest of the whole run: %q", point.unit, rest)
		}
		var reopened []string
		node := resumed.emitter.root
		for range resumed.emitter.continued {
			node = node.children[0].node
			reopened = append(reopened, "hljs-"+node.kind)
		}
		if spans := openSpans(html[:offsets[i]]); !slices.Equal(reopened, spans) {
			t.Fatalf("the run resumed at unit %d reopened the nodes %q; the spans open there are %q", point.unit, reopened, spans)
		}
		seen[strings.Join(reopened, " ")] = true
	}
	for _, want := range []string{"hljs-section", "hljs-section hljs-subst"} {
		if !seen[want] {
			t.Errorf("no checkpoint lies inside %q: the text no longer exercises the aliases", want)
		}
	}
	var stats frameStats
	checkFrames(t, r, "aliased classes, every prefix", "aliased", code, everyCut(code), &stats)
	if !t.Failed() && stats.resumed.Load() == 0 {
		t.Error("no frame of the text resumed")
	}
}

// TestAStackResumesUnlessAModeOnItKeepsWhatARunGaveIt: each rule that keeps a point out of a stack is read from the modes on it, whatever the language: endSameAsBegin, an end pattern copied from a begin, on:begin, on:end, a sublanguage, what a callback left on a frame, and a depth over ResumeStackMax. A stack of plain modes resumes; with one such mode at any place on it, it does not.
func TestAStackResumesUnlessAModeOnItKeepsWhatARunGaveIt(t *testing.T) {
	root := &frame{mode: &Mode{}, language: true}
	stack := func(modes ...*Mode) *frame {
		top := root
		for _, mode := range modes {
			top = &frame{mode: mode, parent: top}
		}
		return top
	}
	plain := func() *Mode { return &Mode{} }
	keeps := func(*enhancedMatch, *response) {}
	copied, begins, ends, embeds := plain(), plain(), plain(), plain()
	copied.setComp().endFromBegin = true
	begins.setRare().onBegin = keeps
	ends.setRare().onEnd = keeps
	embeds.setRare().subLanguage = subLanguage{set: true, name: "xml"}
	if !stackResumes(root) || !stackResumes(stack(plain(), plain(), plain())) {
		t.Fatal("a stack of plain modes does not resume")
	}
	for name, mode := range map[string]*Mode{
		"endSameAsBegin":                     {endSameAsBegin: true},
		"an end pattern copied from a begin": copied,
		"on:begin":                           begins,
		"on:end":                             ends,
		"a sublanguage":                      embeds,
	} {
		for place := range 3 {
			modes := []*Mode{plain(), plain(), plain()}
			modes[place] = mode
			if stackResumes(stack(modes...)) {
				t.Errorf("a stack with a mode that has %s at place %d resumes", name, place)
			}
		}
	}
	held := stack(plain(), plain())
	held.parent.data = &responseData{}
	if stackResumes(held) {
		t.Error("a stack with a frame a callback left data on resumes")
	}
	deep := make([]*Mode, ResumeStackMax)
	for i := range deep {
		deep[i] = plain()
	}
	if !stackResumes(stack(deep...)) {
		t.Errorf("a stack of %d modes does not resume", ResumeStackMax)
	}
	if stackResumes(stack(append(deep, plain())...)) {
		t.Errorf("a stack of %d modes resumes", ResumeStackMax+1)
	}
}
