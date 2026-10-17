//go:build !pig_strip_syntax_highlight

package tui

import (
	"errors"
	"strings"

	"github.com/MichaelKinsy/PiG/tui/internal/hljs"
)

// pig additive (D112): see hlCacheMax. A streamed code block gains text at its end, and highlight.js restarts from a state recorded in an earlier run (hljs.HighlightResumable) instead of scanning the whole block again; the lines are those a whole run returns.

// hlResumeMax bounds the blocks that keep a resume state, so a few blocks streaming at once each resume.
const hlResumeMax = 4

// hlResume is the resume state of the code last highlighted for a block: code[:len(prefix)] is the text before the resume point, lines the complete output lines before it, and partial the output of the line the point is on, up to the point.
type hlResume struct {
	lang             string
	codeBlockOnError bool
	theme            *Theme
	prefix           string
	lines            []string
	partial          string
	iterations       int
}

// hlResumes holds the states, least recently used first; hlMu guards it.
var hlResumes []hlResume

func resetHighlightResumesLocked() { hlResumes = nil }

// highlightResumable highlights code as highlightCodeUncached does, restarting from the resume state of an earlier prefix of the same block when it has one, and leaves the state to resume from next. It reports false when the engine cannot highlight the language, so the caller highlights without resuming.
func highlightResumable(registry *hljs.Registry, code, lang string, codeBlockOnError bool, t *Theme) ([]string, bool) {
	if lang == "" || !registry.SupportsLanguage(lang) {
		return nil, false
	}
	base, generation := hlFindResume(code, lang, codeBlockOnError, t)
	html, points, err := registry.HighlightResumable(code, lang, true, hljs.ResumePoint{Byte: len(base.prefix), Iterations: base.iterations})
	if errors.Is(err, hljs.ErrResumeFailed) {
		base = hlResume{}
		html, points, err = registry.HighlightResumable(code, lang, true, hljs.ResumePoint{})
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
	headText := base.partial + terminalText(head)
	text := headText + terminalText(tail)
	lines := make([]string, 0, len(base.lines)+strings.Count(text, "\n")+1)
	lines = append(lines, base.lines...)
	lines = append(lines, strings.Split(text, "\n")...)
	if len(points) > 0 {
		point := points[len(points)-1]
		complete := len(base.lines) + strings.Count(headText, "\n")
		hlStoreResume(hlResume{
			lang:             lang,
			codeBlockOnError: codeBlockOnError,
			theme:            t,
			prefix:           code[:point.Byte],
			lines:            lines[:complete:complete],
			partial:          headText[strings.LastIndexByte(headText, '\n')+1:],
			iterations:       point.Iterations,
		}, base, generation)
	}
	return lines, true
}

// hlFindResume is the state with the longest prefix of code for the block, or the zero state, and the generation of the loaded languages it was found under.
func hlFindResume(code, lang string, codeBlockOnError bool, t *Theme) (found hlResume, generation int) {
	hlMu.Lock()
	defer hlMu.Unlock()
	for _, r := range hlResumes {
		if r.lang == lang && r.codeBlockOnError == codeBlockOnError && r.theme == t && len(r.prefix) > len(found.prefix) && strings.HasPrefix(code, r.prefix) {
			found = r
		}
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
		if r.lang == replaced.lang && r.theme == replaced.theme && r.prefix == replaced.prefix && r.codeBlockOnError == replaced.codeBlockOnError && replaced.prefix != "" {
			hlResumes = append(hlResumes[:i], hlResumes[i+1:]...)
			break
		}
	}
	if len(hlResumes) >= hlResumeMax {
		hlResumes = hlResumes[1:]
	}
	hlResumes = append(hlResumes, next)
}
