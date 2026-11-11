package codingagent

// Ports packages/coding-agent/src/core/tools/renderers/write.ts
// WriteHighlightCache, refreshWriteHighlightPrefix, rebuildWriteHighlightCacheFull and
// updateWriteHighlightCacheIncremental: while a write call's content streams in, each argument update highlights the new
// lines, and the first lines again as a block, instead of the whole file.

import (
	"strings"
	"time"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/tui"
)

// writePartialFullHighlightLines is WRITE_PARTIAL_FULL_HIGHLIGHT_LINES: the leading lines highlighted together after every update, so a construct that spans lines near the top is coloured as one block.
const writePartialFullHighlightLines = 50 // upstream: packages/coding-agent/src/core/tools/renderers/write.ts:WRITE_PARTIAL_FULL_HIGHLIGHT_LINES

type writeHighlightCache struct {
	rawPath          string
	lang             string
	rawContent       string
	normalizedLines  []string
	highlightedLines []writeHighlightLine
	// prefixEpoch is the highlight epoch the leading block was last highlighted in, valid while prefixFresh: the block's
	// lines are then still what refreshPrefix would write.
	prefixEpoch tui.HighlightEpoch
	prefixFresh bool
}

// writeHighlightLine is one of upstream's highlightedLines. Upstream highlights each streamed line on its own as it
// arrives; here a line is highlighted when a frame first reads it (line), since a collapsed card draws only the first ten
// and the line count. While pending, text is the normalized line it is highlightSingleLine of.
// pig additive (D112): the lines read the same; only lines no frame draws are never highlighted.
type writeHighlightLine struct {
	text    string
	pending bool
}

// highlightedLine is a highlightedLines entry that is already highlighted.
func highlightedLine(text string) writeHighlightLine { return writeHighlightLine{text: text} }

// pendingLine is a highlightedLines entry that is highlightSingleLine of line, highlighted when first read.
func pendingLine(line string) writeHighlightLine {
	return writeHighlightLine{text: line, pending: true}
}

// writeHighlightCode is the one place the write cache highlights code; tests replace it to see which code is highlighted.
var writeHighlightCode = tui.HighlightCode

func highlightSingleLine(line, lang string) string {
	highlighted := writeHighlightCode(line, lang)
	if len(highlighted) == 0 {
		return ""
	}
	return highlighted[0]
}

// line is highlightedLines[i]: a pending line is highlighted on its first read and kept.
func (cache *writeHighlightCache) line(i int) string {
	entry := &cache.highlightedLines[i]
	if entry.pending {
		entry.text, entry.pending = highlightSingleLine(entry.text, cache.lang), false
	}
	return entry.text
}

// lineCount is len(trimTrailingEmptyLines(highlightedLines)): only the lines at the end are read. A pending line that is not
// empty is not highlighted to find out: highlighting keeps every character of a line, so the highlighted line is not empty
// either (TestHighlightedLineOfTextIsNotEmpty), and a card that draws ten lines never reads the last line of a long file.
// pig additive (D112): the count is the one upstream's highlighted lines give; only a line nothing draws is not highlighted.
func (cache *writeHighlightCache) lineCount() int {
	end := len(cache.highlightedLines)
	for end > 0 {
		if entry := &cache.highlightedLines[end-1]; entry.pending && entry.text != "" {
			break
		}
		if cache.line(end-1) != "" {
			break
		}
		end--
	}
	return end
}

// lines is highlightedLines with every line highlighted.
func (cache *writeHighlightCache) lines() []string {
	lines := make([]string, len(cache.highlightedLines))
	for i := range lines {
		lines[i] = cache.line(i)
	}
	return lines
}

func (cache *writeHighlightCache) refreshPrefix() {
	prefixCount := min(writePartialFullHighlightLines, len(cache.normalizedLines))
	if prefixCount == 0 {
		return
	}
	epoch := tui.CurrentHighlightEpoch()
	prefixHighlighted := writeHighlightCode(strings.Join(cache.normalizedLines[:prefixCount], "\n"), cache.lang)
	for i := range prefixCount {
		if i < len(prefixHighlighted) {
			cache.highlightedLines[i] = highlightedLine(prefixHighlighted[i])
		} else {
			cache.highlightedLines[i] = pendingLine(cache.normalizedLines[i])
		}
	}
	cache.prefixEpoch, cache.prefixFresh = epoch, true
}

// rebuildWriteHighlightCacheFull is rebuildWriteHighlightCacheFull; nil is undefined (no language for the path).
func rebuildWriteHighlightCacheFull(rawPath, fileContent string) *writeHighlightCache {
	lang := ""
	if rawPath != "" {
		lang = tui.LanguageFromPath(rawPath)
	}
	if lang == "" {
		return nil
	}
	normalized := normalizeWriteText(fileContent)
	block := writeHighlightCode(normalized, lang)
	lines := make([]writeHighlightLine, len(block))
	for i, line := range block {
		lines[i] = highlightedLine(line)
	}
	return &writeHighlightCache{
		rawPath:          rawPath,
		lang:             lang,
		rawContent:       fileContent,
		normalizedLines:  strings.Split(normalized, "\n"),
		highlightedLines: lines,
	}
}

// updateWriteHighlightCacheIncremental is updateWriteHighlightCacheIncremental: content that extends the cached content
// highlights its new text; anything else rebuilds.
func updateWriteHighlightCacheIncremental(cache *writeHighlightCache, rawPath, fileContent string) *writeHighlightCache {
	lang := ""
	if rawPath != "" {
		lang = tui.LanguageFromPath(rawPath)
	}
	if lang == "" {
		return nil
	}
	if cache == nil || cache.lang != lang || cache.rawPath != rawPath || !strings.HasPrefix(fileContent, cache.rawContent) {
		return rebuildWriteHighlightCacheFull(rawPath, fileContent)
	}
	if len(fileContent) == len(cache.rawContent) {
		return cache
	}
	// The new lines are copied out of the content, so they keep alive only their own bytes and not each version of the
	// whole content they arrived in.
	deltaNormalized := strings.Clone(normalizeWriteText(fileContent[len(cache.rawContent):]))
	cache.rawContent = fileContent
	if len(cache.normalizedLines) == 0 {
		cache.normalizedLines = append(cache.normalizedLines, "")
		cache.highlightedLines = append(cache.highlightedLines, highlightedLine(""))
	}
	segments := strings.Split(deltaNormalized, "\n")
	lastIndex := len(cache.normalizedLines) - 1
	cache.normalizedLines[lastIndex] += segments[0]
	// A highlighted line array shorter than the lines (highlightCode returned fewer rows) is padded so the last line has a slot.
	for len(cache.highlightedLines) <= lastIndex {
		cache.highlightedLines = append(cache.highlightedLines, highlightedLine(""))
	}
	cache.highlightedLines[lastIndex] = pendingLine(cache.normalizedLines[lastIndex])
	for _, segment := range segments[1:] {
		cache.normalizedLines = append(cache.normalizedLines, segment)
		cache.highlightedLines = append(cache.highlightedLines, pendingLine(segment))
	}
	// Once the lines grow past the leading block, the block's text no longer changes, and highlighting it again in the same
	// epoch writes the lines it already holds. write.ts refreshWriteHighlightPrefix reaches the same result through the row's
	// CodeHighlighter, which returns its last lines for the same prefix, theme and grammars; prefixEpoch is that check here.
	if lastIndex < writePartialFullHighlightLines || !cache.prefixFresh || cache.prefixEpoch != tui.CurrentHighlightEpoch() {
		cache.refreshPrefix()
	}
	return cache
}

// A warm-up highlights the text the content gained since the last one, and costs the same again for what it reads of the text before
// (about a millisecond a 100 KB), so the steps are a sixteenth of the content, between writeWarmMinStep and writeWarmMaxStep: the
// frame a warm-up falls in absorbs it (TypeScript: about 10 ms for the smallest step, 30 ms for the largest), and a file of 400 KB
// warms in a few dozen steps.
// pig additive (D112): the steps move when the highlight is computed, not what it is.
const (
	writeWarmMinStep = 8 << 10
	writeWarmMaxStep = 32 << 10
)

// A warm-up is cut into slices so that no frame carries more than about writeWarmBudget of it. highlight.js runs over a slice of the text to its end
// and leaves a state the next slice resumes from, so the slices of a step together leave the state one run over the step leaves (the resume state a
// run over a prefix holds is the one the whole file's run continues from, and each of the steps already ran over a prefix of the text); only the frame
// each part is computed in moves. Without the cut, the frame a 16-32 KB step fell in took 20-30 ms of highlighting on top of its own work, a stall of
// the owner loop that showed as a frame gap twice the throttle's. A slice is sized from the time the last one took, between writeWarmMinSlice and
// writeWarmMaxStep bytes.
// pig additive (D112): the slices move when the highlight is computed, not what it is.
var writeWarmBudget = 4 * time.Millisecond

const writeWarmMinSlice = 1 << 10

// writeWarmStep is how many bytes a streaming write call's content of length n grows before its next warm-up.
//
//portlint:allow numbers a byte count of pig-only pacing (D112): no JS expression to match, and a step a byte short moves only when a warm-up runs
func writeWarmStep(n int) int { return min(max(n/16, writeWarmMinStep), writeWarmMaxStep) }

// warmWholeHighlight keeps the highlighting of the whole file of a streaming write call continuing from the text so far. The
// call's last frame draws the highlighter's lines of the complete file (upstream formatWriteCall: highlighter.highlight(fileContent)),
// which highlight.js computes from the first line unless a run over a prefix left a state to resume from; one run over a file of
// 400 KB stalled the frame that completes the call for 300 ms. Each warm-up leaves such a state, so the last frame highlights only
// what the last step left. The lines it draws are those of a run over the whole file; only when they are computed moves. A language
// that cannot resume stops the warm-ups after the first.
// pig additive (D112): upstream highlights the complete file once, when the call completes.
func (state *builtInRenderState) warmWholeHighlight(content, lang string) {
	if state.writeWarmOff {
		return
	}
	if state.writeWarmTarget <= state.writeWarmed {
		// No step is under way: one starts when the content has grown by a step.
		if len(content) < state.writeWarmed+writeWarmStep(len(content)) {
			return
		}
		state.writeWarmTarget = len(content)
	}
	end := state.writeWarmTarget
	if writeWarmBudget > 0 {
		slice := state.writeWarmSlice
		if slice == 0 {
			slice = writeWarmMinSlice
		}
		// The warm-up never falls more than two steps behind the content, however slow a frame is (the steps before the slices left at most one): the
		// frame that completes the call highlights what is left of it.
		if behind := len(content) - state.writeWarmed - 2*writeWarmStep(len(content)); behind > 0 {
			state.writeWarmTarget = len(content)
			end = state.writeWarmTarget
			slice = max(slice, behind)
		}
		end = min(end, state.writeWarmed+slice)
	}
	// The slice ends on a character start: a prefix that cuts one is not text the later runs start with.
	for end < state.writeWarmTarget && end > state.writeWarmed && !utf8.RuneStart(content[end]) {
		end--
	}
	started := time.Now()
	held := tui.WarmHighlight(normalizeWriteText(content[:end]), lang)
	elapsed := time.Since(started)
	if !held {
		if end >= state.writeWarmTarget {
			state.writeWarmOff = true
			return
		}
		// A short prefix may hold no resume point yet (few lines): the language is not known to lack them until the step's whole text ran.
		state.writeWarmSlice = min(2*max(state.writeWarmSlice, writeWarmMinSlice), writeWarmMaxStep)
		return
	}
	if writeWarmBudget > 0 && elapsed > 0 {
		// The next slice is what the budget buys at the rate of this one.
		state.writeWarmSlice = int(min(max(float64(end-state.writeWarmed)*float64(writeWarmBudget)/float64(elapsed), writeWarmMinSlice), writeWarmMaxStep))
	}
	state.writeWarmed = end
}

// normalizeWriteText is replaceTabs(normalizeDisplayText(text)) for the content of a write call. A lone surrogate, which
// the streaming parser keeps as one UTF-16 unit in WTF-8 (ED A0-BF 80-BF), reads U+FFFD, as it does once the arguments'
// JSON text is decoded and as Pi's terminal writes it.
func normalizeWriteText(text string) string {
	return replaceTabs(strings.ReplaceAll(replaceLoneSurrogates(text), "\r", ""))
}

// replaceLoneSurrogates replaces each WTF-8 lone surrogate in text with U+FFFD.
func replaceLoneSurrogates(text string) string {
	if !strings.Contains(text, "\xed") {
		return text
	}
	var out strings.Builder
	out.Grow(len(text))
	for i := 0; i < len(text); i++ {
		if text[i] == 0xed && i+2 < len(text) && text[i+1] >= 0xa0 && text[i+1] <= 0xbf && text[i+2] >= 0x80 && text[i+2] <= 0xbf {
			out.WriteString("\uFFFD")
			i += 2
			continue
		}
		out.WriteByte(text[i])
	}
	return out.String()
}
