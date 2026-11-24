//go:build !pig_strip_syntax_highlight

package tui

import (
	"errors"
	"strings"
	"sync"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/tui/internal/hljs"
)

// pig additive (D112): see hlCacheMax. A streamed code block gains text at its end, and highlight.js restarts from a state recorded in an earlier run (hljs.HighlightResumable) instead of scanning the whole block again; the lines are those a whole run returns.

// hlResumeMax bounds the blocks that keep a resume state, so a few blocks streaming at once each resume.
const hlResumeMax = 4

// hlResume is the resume state of the code last highlighted for a block: seen is the whole code of that run, code[:len(prefix)] the text before the resume point, lines the complete output lines before the line the point is on, and partial the output of that line up to where the HTML before the point last ends a tag or starts a line. pending is the HTML from there to the point, the text of one run with no tag in it, which the HTML of the next run continues, and scopes the spans open at the point (at most hljs.ResumeStackMax, three words each): the point can lie inside a span (a string or a comment over lines), and a formatter takes a run of text whole, so the next run renders pending again with the text that follows it, inside those spans. The run's decisions before the point read past it, up to the end of seen, so the point holds only for code that starts with seen.
type hlResume struct {
	lang    string
	theme   *Theme
	seen    string
	prefix  string
	lines   []string
	partial string
	pending string
	scopes  []scopeEntry
	// point is where the engine continues: its offsets in seen and in the units of seen, its iteration count, and the modes it lies inside.
	point hljs.ResumePoint
	// text is the units of seen, kept by a warm-up so the next one converts only the text it gained. A run that continues from the state takes it (hlFindResume), so it is held by one run at a time; a state without it converts seen again.
	text *hljs.Text
}

// hlResumes holds the states, least recently used first; hlMu guards it.
var hlResumes []hlResume

// hlTexts holds the units a run converted its code into, for the next run to convert into. A block that streams is highlighted once per frame, and each frame converts the whole code of the block (4 bytes for each code unit): into a new array every time, a stream allocates the square of the block's length.
var hlTexts = sync.Pool{New: func() any { return new(hljs.Text) }}

// hlPooledUnitsMax bounds the units of a Text that goes back to hlTexts (1 MiB): a pooled array keeps the size of the largest code it held (golang/go#23199).
// pig additive (D112): see hlCacheMax. The units are a function of the code; only where they are written differs.
const hlPooledUnitsMax = 1 << 18

// hlFormatters is cliHighlightTheme(hlFormattersTheme); hlMu guards both. A streamed block is highlighted once per frame, and the formatters of a theme are the same every time: each reads the theme when it runs.
var (
	hlFormatters      HighlightTheme
	hlFormattersTheme *Theme
)

// hlFormattersOf is cliHighlightTheme(t), built once for a theme. Its callers only read it.
func hlFormattersOf(t *Theme) HighlightTheme {
	hlMu.Lock()
	defer hlMu.Unlock()
	if hlFormattersTheme != t || hlFormatters == nil {
		hlFormatters, hlFormattersTheme = cliHighlightTheme(t), t
	}
	return hlFormatters
}

func resetHighlightResumesLocked() { hlResumes = nil }

// highlightResumable highlights code as highlightCodeUncached does, restarting from the resume state of an earlier prefix of the same block when it has one, and leaves the state to resume from next. It reports false when the engine cannot highlight the language, so the caller highlights without resuming.
//
// keepUnits leaves the units of code with the state, for the next run over a longer text to continue from (a warm-up's text grows; the lines a frame draws are the last run's).
func highlightResumable(registry *hljs.Registry, code, lang string, t *Theme, keepUnits bool) ([]string, bool) {
	if lang == "" || !registry.SupportsLanguage(lang) {
		return nil, false
	}
	base, generation := hlFindResume(code, lang, t, true)
	source := base.text
	if source == nil {
		source = hlTexts.Get().(*hljs.Text).Reset(code)
	} else {
		source = source.Grow(code)
	}
	kept := false
	defer func() {
		// The units go to the next run unless a state holds them: the engine keeps none of them past its run.
		if !kept && source.Cap() <= hlPooledUnitsMax {
			hlTexts.Put(source.Reset(""))
		}
	}()
	html, points, err := registry.HighlightResumable(source, lang, true, base.point)
	if errors.Is(err, hljs.ErrResumeFailed) {
		base = hlResume{}
		html, points, err = registry.HighlightResumable(source, lang, true, hljs.ResumePoint{})
	}
	if err != nil {
		return nil, false
	}
	theme := hlFormattersOf(t)
	// end is the last point in the HTML, and cut the place where the HTML before it last ends a tag or starts a line. The HTML, which continues the text the last run left pending, renders as its parts before and after cut do, the second inside the spans the first leaves open (renderHighlightedHTMLFrom): the output before cut is kept, and the next run renders the rest again.
	end := len(html)
	if len(points) > 0 {
		end = points[len(points)-1].HTML
	}
	cut := strings.LastIndexAny(html[:end], ">\n") + 1
	head, scopes, pending := "", base.scopes, base.pending
	if cut > 0 {
		if head, scopes, err = renderHighlightedHTMLFrom(theme, t, base.scopes, pending, html[:cut]); err != nil {
			return nil, false
		}
		pending = ""
	}
	tail, _, err := renderHighlightedHTMLFrom(theme, t, scopes, pending, html[cut:])
	if err != nil {
		return nil, false
	}
	// The halves of a surrogate pair that a tag at a cut parts are one character in the output of a whole run.
	headText := jsstring.Canonical(base.partial + head)
	text := jsstring.Canonical(headText + tail)
	lines := make([]string, 0, len(base.lines)+strings.Count(text, "\n")+1)
	lines = append(lines, base.lines...)
	lines = append(lines, strings.Split(text, "\n")...)
	if len(points) > 0 {
		point := points[len(points)-1]
		complete := len(base.lines) + strings.Count(headText, "\n")
		// The text left pending is a copy: the HTML goes with its run.
		if pending == "" {
			pending = strings.Clone(html[cut:end])
		} else {
			pending += html[cut:end]
		}
		next := hlResume{
			lang:    lang,
			theme:   t,
			seen:    code,
			prefix:  code[:point.Byte],
			lines:   lines[:complete:complete],
			partial: headText[strings.LastIndexByte(headText, '\n')+1:],
			pending: pending,
			scopes:  scopes,
			point:   point,
		}
		if keepUnits {
			next.text = source
			kept = true
		}
		hlStoreResume(next, base, generation)
	}
	return lines, true
}

// highlightResumeHeld reports whether a resume state continues a text that starts with code: the state the last highlight of code left, or an earlier one the run of code did not replace.
func highlightResumeHeld(code, lang string, t *Theme) bool {
	found, _ := hlFindResume(code, lang, t, false)
	return len(found.prefix) > 0
}

// releaseHighlightUnits drops the units of the states of lang that hold them for code or for a text code starts with: what the warm-ups of code left. A run that continues from the longest resume point takes the units of one state, and states of equal resume points (the files of one directory share their first lines) are not told apart, so an earlier warm-up of the text can keep its units; they go with the last. A state of a longer or another text keeps its units, which a later text of its own continues from.
func releaseHighlightUnits(code, lang string) {
	hlMu.Lock()
	defer hlMu.Unlock()
	for i := range hlResumes {
		if r := &hlResumes[i]; r.text != nil && r.lang == lang && strings.HasPrefix(code, r.seen) {
			r.text = nil
		}
	}
}

// hlFindResume is the state with the longest prefix of code for the block among those whose whole code code starts with, or the zero state, and the generation of the loaded languages it was found under. take hands the state's units to the caller, which leaves the stored state without them.
func hlFindResume(code, lang string, t *Theme, take bool) (found hlResume, generation int) {
	hlMu.Lock()
	defer hlMu.Unlock()
	foundAt := -1
	for i, r := range hlResumes {
		if r.lang == lang && r.theme == t && len(r.prefix) > len(found.prefix) && strings.HasPrefix(code, r.seen) {
			found, foundAt = r, i
		}
	}
	if take && foundAt >= 0 {
		hlResumes[foundAt].text = nil
	}
	return found, hlGeneration
}

// hlStoreResume records the state, replacing the one it extends, unless the theme or the loaded languages changed since generation.
func hlStoreResume(next, replaced hlResume, generation int) {
	hlMu.Lock()
	defer hlMu.Unlock()
	if next.theme != hlTheme || generation != hlGeneration {
		return
	}
	for i, r := range hlResumes {
		if r.lang == replaced.lang && r.theme == replaced.theme && r.prefix == replaced.prefix && replaced.prefix != "" {
			hlResumes = append(hlResumes[:i], hlResumes[i+1:]...)
			break
		}
	}
	if len(hlResumes) >= hlResumeMax {
		hlResumes = hlResumes[1:]
	}
	hlResumes = append(hlResumes, next)
}
