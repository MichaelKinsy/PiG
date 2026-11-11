//go:build !pig_strip_syntax_highlight

package tui

import (
	"errors"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/tui/internal/hljs"
)

// pig additive (D112): see hlCacheMax. A streamed code block gains text at its end, and highlight.js restarts from a state recorded in an earlier run (hljs.HighlightResumable) instead of scanning the whole block again; the lines are those a whole run returns.

// hlResumeMax bounds the blocks that keep a resume state, so a few blocks streaming at once each resume.
const hlResumeMax = 4

// hlResume is the resume state of the code last highlighted for a block: seen is the whole code of that run, code[:len(prefix)] the text before the resume point, lines the complete output lines before it, and partial the output of the line the point is on, up to the point. The run's decisions before the point read past it, up to the end of seen, so the point holds only for code that starts with seen.
type hlResume struct {
	lang       string
	theme      *Theme
	seen       string
	prefix     string
	lines      []string
	partial    string
	iterations int
	// text is the units of seen, kept by a warm-up so the next one converts only the text it gained. A run that continues from the state takes it (hlFindResume), so it is held by one run at a time; a state without it converts seen again.
	text *hljs.Text
	// unit is the offset of the resume point in the units of seen.
	unit int
}

// hlResumes holds the states, least recently used first; hlMu guards it.
var hlResumes []hlResume

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
		source = hljs.NewText(code)
	} else {
		source = source.Grow(code)
	}
	html, points, err := registry.HighlightResumable(source, lang, true, hljs.ResumePoint{Byte: len(base.prefix), Unit: base.unit, Iterations: base.iterations})
	if errors.Is(err, hljs.ErrResumeFailed) {
		base = hlResume{}
		html, points, err = registry.HighlightResumable(source, lang, true, hljs.ResumePoint{})
	}
	if err != nil {
		return nil, false
	}
	theme := cliHighlightTheme(t)
	split := len(html)
	if len(points) > 0 {
		split = points[len(points)-1].HTML
	}
	head, err := renderHighlightedHTML(html[:split], theme)
	if err != nil {
		return nil, false
	}
	tail, err := renderHighlightedHTML(html[split:], theme)
	if err != nil {
		return nil, false
	}
	headText := base.partial + jsstring.Canonical(head)
	text := headText + jsstring.Canonical(tail)
	lines := make([]string, 0, len(base.lines)+strings.Count(text, "\n")+1)
	lines = append(lines, base.lines...)
	lines = append(lines, strings.Split(text, "\n")...)
	if len(points) > 0 {
		point := points[len(points)-1]
		complete := len(base.lines) + strings.Count(headText, "\n")
		next := hlResume{
			lang:       lang,
			theme:      t,
			seen:       code,
			prefix:     code[:point.Byte],
			lines:      lines[:complete:complete],
			partial:    headText[strings.LastIndexByte(headText, '\n')+1:],
			iterations: point.Iterations,
			unit:       point.Unit,
		}
		if keepUnits {
			next.text = source
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
