//go:build pig_strip_syntax_highlight

package tui

// pig additive (D92): a Piglet Binary built with pig_strip_syntax_highlight links no highlight.js, so no code resumes.

func resetHighlightResumesLocked() {}

func highlightResumable(highlightEngine, string, string, *Theme, bool) ([]string, bool) {
	return nil, false
}

func highlightResumeHeld(string, string, *Theme) bool { return false }

func releaseHighlightUnits(string, string) {}
