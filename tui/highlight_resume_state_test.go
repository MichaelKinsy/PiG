//go:build !pig_strip_syntax_highlight

package tui

import (
	"slices"
	"strconv"
	"strings"
	"testing"
)

// resumeStateTheme empties the highlight memo and the resume states and returns the theme the next calls highlight for.
func resumeStateTheme() *Theme {
	theme := ActiveTheme()
	hlMu.Lock()
	resetHighlightCacheLocked()
	hlTheme = theme
	hlMu.Unlock()
	return theme
}

// checkHighlightedFrame fails the test unless HighlightCode's lines for code are those of a whole run.
func checkHighlightedFrame(t *testing.T, name, code, lang string, theme *Theme) {
	t.Helper()
	got := highlightCodeLines(code, lang)
	want := highlightCodeUncached(highlightRegistry(), code, lang, theme)
	if !slices.Equal(got, want) {
		t.Fatalf("%s, %d bytes:\n got %q\nwant %q", name, len(code), got, want)
	}
}

// streamedBlock is the scoreboard's function numbered n, or with json a JSON document of the same size.
func streamedBlock(n int, json bool) string {
	if json {
		return "{\n  \"id\": " + strconv.Itoa(n) + ",\n  \"name\": \"item\",\n  \"tags\": [\"a\", \"b\"],\n  \"nested\": {\n    \"ok\": true,\n    \"list\": [1, 2.5, null]\n  }\n}\n"
	}
	return strings.ReplaceAll(md20000Function, "{n}", strconv.Itoa(n))
}

// TestStreamedBlockFramesAreAWholeRun streams a TypeScript block, a JSON document, and blocks whose frames resume inside a mode that runs over lines (a list of documents, a comment, a docstring) as an open fence delivers them: a frame at every line, a frame at every byte, and the frames of a fence that closes (the whole code, the code with one and then two backticks of the closing fence on a line after it, and the code alone again once the fence is closed, which is shorter than the frame before it). Every frame has the lines of a whole run.
func TestStreamedBlockFramesAreAWholeRun(t *testing.T) {
	for _, c := range []struct {
		name, lang, code string
	}{
		{"typescript", "typescript", streamedBlock(1, false)},
		{"json", "json", streamedBlock(1, true)},
		{"typescript with CRLF", "typescript", strings.ReplaceAll(streamedBlock(2, false), "\n", "\r\n")},
		{"json list of documents", "json", "[\n" + strings.Repeat("  {\"id\": 1, \"name\": \"an item\", \"tags\": [\"a\", \"b\"]},\n", 12) + "  {}\n]\n"},
		{"go comment over lines", "go", "package demo\n\n/* A comment: you will see\n   the lines it runs over, and the\n   words it marks. */\nvar x = \"text\"\nvar y = 1\nvar z = 2\n"},
		{"python docstring over lines", "python", "def f():\n    \"\"\"First line \\n of the\n    docstring that runs \\t over\n    the lines.\n    \"\"\"\n    return 1\n\nx = 2\ny = 3\n"},
		{"css comment over lines", "css", "a {\n  color: red; /* a comment: you will\n  see it run over the lines */\n  margin: 0;\n}\nb { top: 1px; }\nc { top: 2px; }\n"},
	} {
		theme := resumeStateTheme()
		for end := 1; end <= len(c.code); end++ {
			if c.code[end-1] == '\n' {
				checkHighlightedFrame(t, c.name+", a frame per line", c.code[:end], c.lang, theme)
			}
		}
		theme = resumeStateTheme()
		for end := 1; end <= len(c.code); end++ {
			checkHighlightedFrame(t, c.name+", a frame per byte", c.code[:end], c.lang, theme)
		}
		theme = resumeStateTheme()
		closed := strings.TrimSuffix(c.code, "\n")
		for _, frame := range []string{c.code[:len(c.code)/2], closed, closed + "\n`", closed + "\n``", closed, closed + "\n``", closed} {
			checkHighlightedFrame(t, c.name+", a fence that closes", frame, c.lang, theme)
		}
	}
}

// TestAnEditBeforeTheEndOfABlockTakesNoStateOfTheBlock: a resume state serves only a text that starts with the whole text it was recorded over. After a block is streamed, the block with one byte changed at any place has, as the state it would resume from, none recorded over a text that reaches the change, and its lines are those of a whole run.
func TestAnEditBeforeTheEndOfABlockTakesNoStateOfTheBlock(t *testing.T) {
	script := streamedBlock(1, false) + streamedBlock(2, false)
	for _, c := range []struct{ lang, code string }{
		{"typescript", script},
		{"javascript", script},
		// A JSON list is one mode from its first line to its last: its states lie inside the open modes.
		{"json", "[\n" + streamedBlock(1, true) + ",\n" + streamedBlock(2, true) + "]\n"},
	} {
		lang, code := c.lang, c.code
		theme := resumeStateTheme()
		for end := 40; end < len(code); end += 40 {
			checkHighlightedFrame(t, lang+", streamed", code[:end], lang, theme)
		}
		checkHighlightedFrame(t, lang+", streamed", code, lang, theme)
		if base, _ := hlFindResume(code+"x", lang, theme, false); base.prefix == "" {
			t.Fatalf("%s: the streamed block left no resume state", lang)
		}
		for at := 0; at < len(code); at += 13 {
			edited := code[:at] + "#" + code[at+1:]
			if edited == code {
				continue
			}
			if base, _ := hlFindResume(edited, lang, theme, false); len(base.seen) > at || !strings.HasPrefix(edited, base.seen) {
				t.Fatalf("%s: the block edited at byte %d would resume from a state recorded over %d bytes", lang, at, len(base.seen))
			}
			checkHighlightedFrame(t, lang+", edited at byte "+strconv.Itoa(at), edited, lang, theme)
			// The edited text leaves states of its own; the block streams again from none of them.
			checkHighlightedFrame(t, lang+", the block again", code, lang, theme)
		}
	}
}

// TestResumeStatesAreBounded: however many blocks stream at once, hlResumeMax states are kept, and every frame of every block has the lines of a whole run.
func TestResumeStatesAreBounded(t *testing.T) {
	theme := resumeStateTheme()
	const blocks = hlResumeMax + 3
	for end := 30; end <= len(streamedBlock(1, false)); end += 30 {
		for n := 1; n <= blocks; n++ {
			code := streamedBlock(n, n%2 == 0)
			lang := "typescript"
			if n%2 == 0 {
				lang = "json"
			}
			checkHighlightedFrame(t, "block "+strconv.Itoa(n), code[:min(end, len(code))], lang, theme)
			hlMu.Lock()
			held := len(hlResumes)
			hlMu.Unlock()
			if held > hlResumeMax {
				t.Fatalf("%d resume states are held, the bound is %d", held, hlResumeMax)
			}
		}
	}
	hlMu.Lock()
	held := len(hlResumes)
	hlMu.Unlock()
	if held == 0 {
		t.Fatal("the streamed blocks left no resume state")
	}
}
