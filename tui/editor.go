package tui

import (
	"context"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/internal/lazyregexp"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

var pasteMarkerRegex = lazyregexp.New(`\[paste #(\d+)( (\+\d+ lines|\d+ chars))?\]`)
var pasteMarkerSingle = lazyregexp.New(`^\[paste #(\d+)( (\+\d+ lines|\d+ chars))?\]$`)
var pasteCtrlCSIU = lazyregexp.New(`\x1b\[(\d+);5u`)

const attachmentAutocompleteDebounce = 20 * time.Millisecond

// Editor is a multi-line text input with undo/kill-ring support.
type Editor struct {
	invalidatable
	// EmbedWorkingStatus opts into the coding-agent status border.
	EmbedWorkingStatus     bool
	workingStatusIndicator *StatusIndicator
	// BorderColor colors a complete border after layout and truncation. Nil selects the application-derived default.
	BorderColor func(string) string
	// Focused emits widthx.CursorMarker so the TUI can position the hardware cursor for IME candidate windows.
	Focused    bool
	lines      []string
	cursor     [2]int // [logical line, UTF-16 column]
	jumpMode   string // "forward" | "backward" | ""
	history    []editorState
	killRing   KillRing
	lastAction string // "kill" | "yank" | "type-word" | ""

	preferredVisualCol   *int
	snappedFromCursorCol *int

	// Slash-command autocomplete suggestions.
	// Mirrors upstream `editor.ts` autocomplete state
	// (autocompleteProvider / autocompleteList / autocompletePrefix).
	autocomplete                  AutocompleteProvider
	autocompleteItems             []AutocompleteItem
	autocompleteTriggerCharacters []rune
	// Complete provider callbacks run on owned workers and publish only on the editor loop.
	asyncAutocomplete             *AsyncAutocompleteProvider
	autocompleteLifetime          context.Context
	startAutocompleteTask         func(func())
	startAutocompleteInput        func(AutocompleteWork)
	autocompleteError             func(error)
	autocompleteTaskDone          <-chan struct{}
	autocompleteChanged           func(AutocompleteProvider)
	asyncCancel                   context.CancelFunc
	asyncSeq                      uint64
	scheduleAsyncApply            func(func())
	autocompleteRequestID         uint64
	autocompleteCursor            int    // selected row index
	autocompletePrefix            string // buffer slice the popup is matching against
	autocompleteMousePressedIndex *int
	// autocompleteQueryCursor is the text cursor at the moment
	// autocompleteItems/autocompletePrefix were last populated. The local
	// and async suggestion applies are themselves deferred (scheduleAsyncApply
	// posts them to run on the host's main loop, single-threaded with
	// keystroke handling, so a fast keystroke burst can dispatch more inserts
	// -- including Enter -- before an already-computed apply for an earlier
	// keystroke actually runs). AutocompleteAccept compares this against the
	// live cursor before trusting autocompletePrefix's length, so an accept
	// that arrives after the buffer moved on treats the popup as stale
	// instead of splicing the suggestion's full value into the wrong offset.
	autocompleteQueryCursor [2]int
	autocompleteForced      bool
	autocompleteFromAwaited bool
	autocompleteMax         int // max visible rows (default 5)
	paddingX                int

	// Thinking-level border color.
	// Mirrors upstream `editor.ts::updateEditorBorderColor` which maps
	// the thinking level to a per-level theme color. "off" uses borderMuted.
	// Bash mode overrides this (bashHeaderColor takes precedence).
	ThinkingLevel string // "off" | "low" | "medium" | "high"

	// Submission history for Up/Down arrow navigation.
	// Distinct from the undo history (editorState stack above).
	// Mirrors upstream editor.ts::history / historyIndex
	// (components/editor.ts:261-390).
	//
	// inputHistory stores submitted texts, newest first (index 0 = most recent).
	// inputHistIdx is the current browse position: -1 = not browsing.
	// inputHistSaved holds the editor state captured when browsing starts,
	// restored when the user navigates back past index 0.
	inputHistory   []string
	inputHistIdx   int // -1 = live, ≥0 = browsing
	inputHistSaved *editorHistoryDraft

	// Bracketed paste state: buffers content between \x1b[200~ and \x1b[201~.
	// Mirrors upstream editor.ts::isInPaste / pasteBuffer.
	isInPaste   bool
	pasteBuffer string

	// Compact paste markers. Mirrors upstream editor.ts::pastes / pasteCounter.
	// When a paste is >10 lines or >1000 chars, the editor inserts a marker
	// like "[paste #N +K lines]" and stashes the actual content keyed by N.
	// On submit / GetExpandedText, markers are expanded back to their content.
	pastes       map[int]string
	pasteCounter int

	// renderWidth is the last width passed to Render; used for visual-line movement.
	renderWidth                int
	renderedVisibleLineCount   int
	renderedAutocompleteHeight int

	// wrapCache memoizes per-logical-line word wrapping (see wrappedChunks).
	// wrapCacheLines is a content snapshot of e.lines at cache time and
	// wrapCacheChunks the matching wrapped chunks, keyed by wrapCacheWidth, so an
	// unchanged line is not re-wrapped every render/keystroke. The expensive
	// grapheme segmentation + wrapping dominates editor render cost on large
	// pasted buffers, where it otherwise runs over the whole buffer twice per
	// render (layoutText + buildVisualLineMap).
	wrapCacheWidth    int
	wrapCacheLines    []string
	wrapCacheChunks   [][]textChunk
	wrapCachePasteIDs map[int]bool

	// Scroll offset + visible-line cap. Mirrors upstream editor.ts
	// scrollOffset / maxVisibleLines. When the visual layout exceeds
	// maxVisibleLines, the editor renders a window plus
	// "─── ↑/↓ N more " indicators in the top/bottom borders.
	// maxVisibleLines defaults to 5: interactive mode updates it from
	// `max(5, floor(terminalRows * 0.3))` on resize.
	scrollOffset    int
	maxVisibleLines int

	// Mirrors upstream editor.ts public hooks / flags.
	OnSubmit      func(string)
	OnChange      func(string)
	DisableSubmit bool

	// remote is an extension's editor component standing in for this
	// editor (editor_remote.go), or nil.
	remote *editorRemoteState
}

type editorState struct {
	lines  []string
	cursor [2]int
	// pastes/pasteCounter travel with the snapshot so undo restores the
	// paste registry alongside the text (mirrors upstream EditorSnapshot).
	pastes       map[int]string
	pasteCounter int
}

type editorHistoryDraft struct {
	lines  []string
	cursor [2]int
}

type editorVisualLine struct {
	logicalLine int
	startCol    int
	length      int
}

type textChunk struct {
	text       string
	startIndex int
	endIndex   int
}

type layoutLine struct {
	text      string
	hasCursor bool
	cursorPos int
}

func NewEditor() *Editor {
	return &Editor{lines: []string{""}, inputHistIdx: -1, maxVisibleLines: 5, renderWidth: 80, wrapCacheWidth: -1}
}

// SetMaxVisibleLines updates the editor's maximum visible visual-line
// count. Mirrors upstream editor.ts maxVisibleLines (set from
// `max(5, floor(terminalRows * 0.3))` on resize). Clamped to ≥ 1.
func (e *Editor) SetMaxVisibleLines(n int) {
	if n < 1 {
		n = 1
	}
	if e.maxVisibleLines == n {
		return
	}
	e.maxVisibleLines = n
	e.Invalidate()
}

// MaxVisibleLines returns the current visible-line cap (mostly for tests).
func (e *Editor) MaxVisibleLines() int { return e.maxVisibleLines }

// PaddingX returns the editor's horizontal content padding.
func (e *Editor) PaddingX() int { return e.paddingX }

// SetFocused records whether the editor holds TUI focus; only a focused editor emits the hardware-cursor marker.
func (e *Editor) SetFocused(focused bool) {
	if e.Focused != focused {
		e.Focused = focused
		e.Invalidate()
	}
}

// SetPaddingX changes the editor's horizontal content padding.
func (e *Editor) SetPaddingX(padding int) {
	padding = max(0, padding)
	if e.paddingX == padding {
		return
	}
	e.paddingX = padding
	e.Invalidate()
	if e.remote != nil {
		e.remote.remote.StateChanged()
	}
}

// AutocompleteMaxVisible returns the maximum number of autocomplete rows.
func (e *Editor) AutocompleteMaxVisible() int {
	if e.autocompleteMax == 0 {
		return 5
	}
	return e.autocompleteMax
}

// SetAutocompleteMaxVisible changes the autocomplete row limit.
func (e *Editor) SetAutocompleteMaxVisible(maxVisible int) {
	maxVisible = max(3, min(20, maxVisible))
	if e.AutocompleteMaxVisible() == maxVisible {
		return
	}
	e.autocompleteMax = maxVisible
	e.refreshAutocomplete()
	e.Invalidate()
	if e.remote != nil {
		e.remote.remote.StateChanged()
	}
}

// Text returns the editor content as a single string.
func (e *Editor) Text() string { return strings.Join(e.lines, "\n") }

// SetText normalizes and replaces the document, resets paste/typing state, records a changed document for undo, and cancels completion without querying.
func (e *Editor) SetText(text string) {
	if e.remote != nil {
		e.remoteSetText(text)
		return
	}
	e.AutocompleteCancel()
	e.lastAction = ""
	e.inputHistIdx = -1
	e.inputHistSaved = nil
	normalized := normalizeEditorText(text)
	if e.Text() != normalized {
		e.saveHistory()
	}
	e.pastes = nil
	e.pasteCounter = 0
	e.lines = strings.Split(normalized, "\n")
	e.cursor[0] = len(e.lines) - 1
	e.setCursorCol(jsstring.Length(e.lines[e.cursor[0]]))
	e.Invalidate()
	if e.OnChange != nil {
		e.OnChange(e.Text())
	}
}

// Clear resets the editor.
func (e *Editor) Clear() {
	if e.remote != nil {
		e.remoteSetText("")
		return
	}
	if e.Text() != "" {
		e.saveHistory()
	}
	e.lastAction = ""
	e.lines = []string{""}
	e.cursor = [2]int{0, 0}
	e.jumpMode = ""
	e.preferredVisualCol = nil
	e.snappedFromCursorCol = nil
	e.AutocompleteCancel()
	e.inputHistIdx = -1 // exit history browsing mode on clear
	e.inputHistSaved = nil
	e.pastes = nil
	e.pasteCounter = 0
	e.scrollOffset = 0
	e.Invalidate()
	if e.OnChange != nil {
		e.OnChange("")
	}
}

// GetExpandedText returns the editor content with any compact paste
// markers (`[paste #N +K lines]` / `[paste #N M chars]`) expanded back
// to their stored content. Mirrors upstream editor.ts::getExpandedText
// (.upstream/v0.69.0/packages/tui/src/components/editor.ts:929).
func (e *Editor) GetExpandedText() string {
	if e.remote != nil {
		return e.remoteExpandedText()
	}
	return e.expandPasteMarkers(e.Text())
}

// expandPasteMarkers replaces every "[paste #N ...]" marker in text
// with the content stored under id N. Mirrors upstream editor.ts:
// expandPasteMarkers (line 915–921).
func (e *Editor) expandPasteMarkers(text string) string {
	if len(e.pastes) == 0 {
		return text
	}
	result := text
	for id, content := range e.pastes {
		re := pasteMarkerRegexp(id)
		result = re.ReplaceAllLiteralString(result, content)
	}
	return result
}

// ClearPastes drops the compact-paste store. Callers that consume the
// editor's text via GetExpandedText (e.g. submit) should call this
// after expanding so subsequent edits don't re-expand stale markers.
// Mirrors upstream submitValue() resetting pastes + pasteCounter.
func (e *Editor) ClearPastes() {
	e.pastes = nil
	e.pasteCounter = 0
}

// AddToHistory trims JavaScript whitespace and adds a submitted prompt for Up/Down navigation. It skips empty strings and consecutive duplicates and retains at most 100 entries.
func (e *Editor) AddToHistory(text string) {
	if e.remote != nil {
		e.remote.remote.AddToHistory(text)
		return
	}
	trimmed := widthx.JSTrim(text)
	if trimmed == "" {
		return
	}
	if len(e.inputHistory) > 0 && e.inputHistory[0] == trimmed {
		return // no consecutive duplicates
	}
	e.inputHistory = append([]string{trimmed}, e.inputHistory...)
	if len(e.inputHistory) > 100 {
		e.inputHistory = e.inputHistory[:100]
	}
}

// navigateHistory moves through input history.
// direction: -1 = older (Up), +1 = newer (Down).
// Mirrors upstream editor.ts::navigateHistory (line 369-390).
func (e *Editor) navigateHistory(direction int) {
	e.lastAction = ""
	if len(e.inputHistory) == 0 {
		return
	}
	newIdx := e.inputHistIdx - direction // Up(-1) increases idx, Down(+1) decreases
	if newIdx < -1 || newIdx >= len(e.inputHistory) {
		return
	}
	entering := e.inputHistIdx == -1 && newIdx >= 0
	if entering {
		e.saveHistory()
		// Capture current editor state before entering browse mode.
		e.inputHistSaved = &editorHistoryDraft{lines: append([]string(nil), e.lines...), cursor: e.cursor}
	}
	e.inputHistIdx = newIdx
	if e.inputHistIdx == -1 {
		if e.inputHistSaved == nil {
			e.setTextNoHistReset("", false)
			e.notifyChange()
			return
		}
		e.lines = append([]string(nil), e.inputHistSaved.lines...)
		e.cursor = e.inputHistSaved.cursor
		e.inputHistSaved = nil
		e.preferredVisualCol = nil
		e.snappedFromCursorCol = nil
		e.scrollOffset = 0
		e.Invalidate()
		e.notifyChange()
		return
	}
	e.setTextNoHistReset(e.inputHistory[e.inputHistIdx], direction == -1)
	e.notifyChange()
}

// notifyChange reports the current text to OnChange, as upstream's
// setTextInternal and draft restore call onChange.
func (e *Editor) notifyChange() {
	if e.OnChange != nil {
		e.OnChange(e.Text())
	}
}

// setTextNoHistReset sets text without touching inputHistIdx: used by
// navigateHistory so undo-history undo doesn't interfere with browse state.
func (e *Editor) setTextNoHistReset(text string, cursorAtStart bool) {
	e.lines = strings.Split(text, "\n")
	if len(e.lines) == 0 {
		e.lines = []string{""}
	}
	if cursorAtStart {
		e.cursor[0] = 0
		e.setCursorCol(0)
	} else {
		e.cursor[0] = len(e.lines) - 1
		e.setCursorCol(jsstring.Length(e.lines[e.cursor[0]]))
	}
	e.scrollOffset = 0
	e.Invalidate()
}

func (e *Editor) setCursorCol(col int) {
	e.cursor[1] = col
	e.preferredVisualCol = nil
	e.snappedFromCursorCol = nil
}

func isPasteMarker(segment string) bool {
	return len(segment) >= 10 && pasteMarkerSingle.MatchString(segment)
}

func (e *Editor) validPasteIDs() map[int]bool {
	ids := make(map[int]bool, len(e.pastes))
	for id := range e.pastes {
		ids[id] = true
	}
	return ids
}

func wordWrapLine(line string, maxWidth int, preSegmented []editorSegment) []textChunk {
	if line == "" || maxWidth <= 0 {
		return []textChunk{{text: "", startIndex: 0, endIndex: 0}}
	}
	length := jsstring.Length(line)
	if widthx.VisibleWidth(line) <= maxWidth {
		return []textChunk{{text: line, startIndex: 0, endIndex: length}}
	}
	segments := preSegmented
	if segments == nil {
		segments = editorSegments(graphemeSegmentData(line))
	}
	// Decode once: wrapping many chunks must not repeatedly scan the entire logical line.
	units := jsstring.ToUTF16(line)
	chunk := func(start, end int) textChunk {
		return textChunk{text: jsstring.FromUTF16(units[start:end]), startIndex: start, endIndex: end}
	}
	chunks := make([]textChunk, 0, len(segments))
	currentWidth, chunkStart := 0, 0
	wrapOppIndex, wrapOppWidth := -1, 0
	for i, seg := range segments {
		gWidth, charIndex := seg.Width, seg.Start
		isWs := !isPasteMarker(seg.Text) && isWhitespaceChar(seg.Text)
		if currentWidth+gWidth > maxWidth {
			if wrapOppIndex >= 0 && currentWidth-wrapOppWidth+gWidth <= maxWidth {
				chunks = append(chunks, chunk(chunkStart, wrapOppIndex))
				chunkStart = wrapOppIndex
				currentWidth -= wrapOppWidth
			} else if chunkStart < charIndex {
				chunks = append(chunks, chunk(chunkStart, charIndex))
				chunkStart, currentWidth = charIndex, 0
			}
			wrapOppIndex = -1
		}
		if gWidth > maxWidth {
			subChunks := wordWrapLine(seg.Text, maxWidth, nil)
			for j := range len(subChunks) - 1 {
				sc := subChunks[j]
				chunks = append(chunks, textChunk{text: sc.text, startIndex: charIndex + sc.startIndex, endIndex: charIndex + sc.endIndex})
			}
			last := subChunks[len(subChunks)-1]
			chunkStart = charIndex + last.startIndex
			currentWidth = widthx.VisibleWidth(last.text)
			wrapOppIndex = -1
			continue
		}
		currentWidth += gWidth
		if i+1 < len(segments) {
			next := segments[i+1]
			if isWs && (isPasteMarker(next.Text) || !isWhitespaceChar(next.Text)) {
				wrapOppIndex, wrapOppWidth = next.Start, currentWidth
			} else if !isWs && !isWhitespaceChar(next.Text) {
				isCJK := !isPasteMarker(seg.Text) && widthx.IsCJKBreak(seg.Text)
				nextIsCJK := !isPasteMarker(next.Text) && widthx.IsCJKBreak(next.Text)
				if isCJK || nextIsCJK {
					wrapOppIndex, wrapOppWidth = next.Start, currentWidth
				}
			}
		}
	}
	return append(chunks, chunk(chunkStart, length))
}

// isEditorEmpty returns true when the editor contains only a single empty line.
func (e *Editor) isEditorEmpty() bool {
	return len(e.lines) == 1 && e.lines[0] == ""
}

func (e *Editor) saveHistory() {
	state := editorState{
		lines:        make([]string, len(e.lines)),
		cursor:       e.cursor,
		pastes:       clonePastes(e.pastes),
		pasteCounter: e.pasteCounter,
	}
	copy(state.lines, e.lines)
	e.history = append(e.history, state)
}

// clonePastes returns a shallow copy of a paste registry so an undo snapshot
// is not mutated by later edits. Returns nil for an empty/nil registry.
func clonePastes(m map[int]string) map[int]string {
	if len(m) == 0 {
		return nil
	}
	c := make(map[int]string, len(m))
	maps.Copy(c, m)
	return c
}

// thinkingBorderSGR maps a thinking level string to a truecolor foreground
// SGR prefix for the editor's top divider. Colors mirror upstream
// theme/dark.json thinkingLow/Medium/High values, used as fg on the border
// rule (not bg-tints: no rendering-model adjustment needed).
//
//	thinkingLow    #5f87af : upstream dark.json:73
//	thinkingMedium #81a2be : upstream dark.json:74
//	thinkingHigh   #b294bb : upstream dark.json:75
//
// Returns "" for "off" so the caller falls back to borderMuted.
func thinkingBorderSGR(level string) string {
	switch level {
	case "low":
		return ThemeHexFg("#5f87af")
	case "medium":
		return ThemeHexFg("#81a2be")
	case "high":
		return ThemeHexFg("#b294bb")
	}
	return ""
}

// wrappedChunks returns the word-wrapped textChunk list for every logical line
// at the given width, memoizing per line. A line whose content is unchanged
// from the previous call at the same width and with the same valid paste IDs reuses its cached chunks instead of
// re-running wordWrapLine (grapheme segmentation + wrapping). Output is
// identical to calling wordWrapLine(line, width, e.segmentLine(line)) for each
// line; the reuse is invisible. The comparison is by content, so SetText
// rebuilding the buffer still reuses chunks for lines whose text is unchanged.
func (e *Editor) wrappedChunks(width int) [][]textChunk {
	if width < 1 {
		width = 1
	}
	reuse := e.wrapCacheWidth == width && len(e.wrapCachePasteIDs) == len(e.pastes)
	if reuse {
		for id := range e.wrapCachePasteIDs {
			if _, ok := e.pastes[id]; !ok {
				reuse = false
				break
			}
		}
	}
	out := make([][]textChunk, len(e.lines))
	for i, line := range e.lines {
		if reuse && i < len(e.wrapCacheLines) && e.wrapCacheLines[i] == line {
			out[i] = e.wrapCacheChunks[i]
			continue
		}
		out[i] = wordWrapLine(line, width, e.segmentLine(line))
	}
	snap := make([]string, len(e.lines))
	copy(snap, e.lines)
	e.wrapCacheWidth = width
	e.wrapCacheLines = snap
	e.wrapCacheChunks = out
	if !reuse {
		e.wrapCachePasteIDs = e.validPasteIDs()
	}
	return out
}

// layoutText walks logical lines through the word-wrap cache and applies the
// cursor decoration to the appropriate visual chunk, returning the flat list
// of visual lines that Render emits between the top and bottom borders, before
// scroll-windowing. Mirrors upstream editor.ts::layoutText (line 823) folded
// with the cursor-splice loop in render (line 467-510).
func (e *Editor) layoutText(contentWidth int) []layoutLine {
	if len(e.lines) == 0 || (len(e.lines) == 1 && e.lines[0] == "") {
		return []layoutLine{{text: "", hasCursor: true, cursorPos: 0}}
	}

	// wordWrapLine returns a single chunk (startIndex 0) for a line that fits,
	// so the chunk loop below reproduces the former short-line fast path
	// exactly while letting wrappedChunks memoize the expensive wrapping.
	layoutLines := make([]layoutLine, 0, len(e.lines))
	for i, chunks := range e.wrappedChunks(contentWidth) {
		isCurrentLine := i == e.cursor[0]
		for ci, chunk := range chunks {
			cursorPos := e.cursor[1]
			isLastChunk := ci == len(chunks)-1
			hasCursorInChunk := false
			adjustedCursorPos := 0
			if isCurrentLine {
				if isLastChunk {
					hasCursorInChunk = cursorPos >= chunk.startIndex
					adjustedCursorPos = cursorPos - chunk.startIndex
				} else {
					hasCursorInChunk = cursorPos >= chunk.startIndex && cursorPos < chunk.endIndex
					if hasCursorInChunk {
						adjustedCursorPos = min(cursorPos-chunk.startIndex, jsstring.Length(chunk.text))
					}
				}
			}
			layoutLines = append(layoutLines, layoutLine{text: chunk.text, hasCursor: hasCursorInChunk, cursorPos: adjustedCursorPos})
		}
	}

	return layoutLines
}

// buildVisualLines lays out the text at width and decorates the cursor.
// rowWidth is the most cells a row may take: the content width plus the one
// right-padding cell upstream's cursorInPadding lets the cursor use.
func (e *Editor) buildVisualLines(width, rowWidth int) []string {
	var out []string
	emitCursorMarker := e.Focused && len(e.autocompleteItems) == 0
	for _, line := range e.layoutText(width) {
		if !line.hasCursor {
			out = append(out, line.text)
			continue
		}
		// pig divergence (D66): Upstream appends the end-of-line cursor as a
		// highlighted space even when the row has no cell left for it, which
		// happens only at width 1 without padding. PiG highlights the final
		// grapheme there instead of emitting a row wider than the terminal.
		if line.cursorPos == jsstring.Length(line.text) && widthx.VisibleWidth(line.text) >= rowWidth && len(line.text) > 0 {
			segments := graphemeSegments(line.text)
			last := segments[len(segments)-1]
			marker := ""
			if emitCursorMarker {
				marker = widthx.CursorMarker
			}
			out = append(out, line.text[:last.Start]+marker+"\033[7m"+last.Text+"\033[0m")
			continue
		}
		out = append(out, e.renderCursorAt(line.text, line.cursorPos, emitCursorMarker))
	}
	return out
}

func (e *Editor) buildVisualLineMap(width int) []editorVisualLine {
	if width < 1 {
		width = 1
	}
	if len(e.lines) == 0 {
		return []editorVisualLine{{logicalLine: 0, startCol: 0, length: 0}}
	}
	var out []editorVisualLine
	for i, chunks := range e.wrappedChunks(width) {
		for _, chunk := range chunks {
			out = append(out, editorVisualLine{logicalLine: i, startCol: chunk.startIndex, length: jsstring.Length(chunk.text)})
		}
	}
	if len(out) == 0 {
		return []editorVisualLine{{logicalLine: 0, startCol: 0, length: 0}}
	}
	return out
}

func (e *Editor) findVisualLineAt(visual []editorVisualLine, line, col int) int {
	for i, vl := range visual {
		if vl.logicalLine != line {
			continue
		}
		offset := col - vl.startCol
		isLastSegment := i == len(visual)-1 || visual[i+1].logicalLine != vl.logicalLine
		if offset >= 0 && (offset < vl.length || (isLastSegment && offset == vl.length)) {
			return i
		}
	}
	return max(0, len(visual)-1)
}

func (e *Editor) findCurrentVisualLine(visual []editorVisualLine) int {
	return e.findVisualLineAt(visual, e.cursor[0], e.cursor[1])
}

func (e *Editor) isOnFirstVisualLine() bool {
	visual := e.buildVisualLineMap(e.renderWidth)
	return e.findCurrentVisualLine(visual) == 0
}

func (e *Editor) isOnLastVisualLine() bool {
	visual := e.buildVisualLineMap(e.renderWidth)
	return e.findCurrentVisualLine(visual) == len(visual)-1
}

// findCursorVisualLine returns the index in visual[] that corresponds to
// the cursor's logical position. Returns 0 when no chunk is decorated
// with the cursor (matches upstream `findIndex(hasCursor)` returning 0
// on -1).
func (e *Editor) findCursorVisualLine(visual []string) int {
	mapping := e.buildVisualLineMap(e.renderWidth)
	idx := e.findCurrentVisualLine(mapping)
	if idx >= len(visual) {
		return max(0, len(visual)-1)
	}
	return idx
}

// borderSGR returns the application-derived default border foreground.
func (e *Editor) borderSGR() string {
	if e.IsBashMode() {
		return bashHeaderColor()
	}
	if sgr := thinkingBorderSGR(e.ThinkingLevel); sgr != "" {
		return sgr
	}
	return ActiveTheme().BorderMuted
}

func createScrollBorder(arrow string, n, width int) string {
	width = max(0, width)
	label := fmt.Sprintf(" %s %d more ", arrow, n)
	labelWidth := widthx.VisibleWidth(label)
	if labelWidth+2 <= width {
		left := (width - labelWidth) / 2
		return strings.Repeat("─", left) + label + strings.Repeat("─", width-left-labelWidth)
	}
	indicator := fmt.Sprintf("─── %s %d more ", arrow, n)
	remaining := width - widthx.VisibleWidth(indicator)
	if remaining >= 0 {
		return indicator + strings.Repeat("─", remaining)
	}
	ellipsis := "..."[:min(3, width)]
	return widthx.SliceByColumn(indicator, 0, width-len(ellipsis), true) + ellipsis
}

func (e *Editor) colorEditorBorder(text string) string {
	if e.BorderColor != nil {
		return e.BorderColor(text)
	}
	reset := SGRFgReset
	if e.IsBashMode() || thinkingBorderSGR(e.ThinkingLevel) != "" {
		reset = "\x1b[0m"
	}
	return e.borderSGR() + text + reset
}

func (e *Editor) renderTopBorder(width, hidden int) string {
	if hidden > 0 {
		return e.colorEditorBorder(createScrollBorder("↑", hidden, width))
	}
	return e.colorEditorBorder(strings.Repeat("─", width))
}

func (e *Editor) renderBottomBorder(width, hidden int) string {
	if hidden > 0 {
		return e.colorEditorBorder(createScrollBorder("↓", hidden, width))
	}
	return e.colorEditorBorder(strings.Repeat("─", width))
}

func (e *Editor) Render(width int) []string {
	if width < 1 {
		width = 1
	}
	if e.remote != nil {
		return e.renderRemote(width)
	}
	// Build the full visual layout first (all chunks of all logical
	// lines, with cursor decoration applied), then scroll/window it
	// per upstream editor.ts scrollOffset + maxVisibleLines logic.
	paddingX := min(e.paddingX, max(0, (width-1)/2))
	contentWidth := max(1, width-paddingX*2)
	layoutWidth := contentWidth
	if paddingX == 0 {
		// Match upstream editor.ts: reserve the rightmost terminal column for
		// the cursor when the editor has no horizontal padding.
		layoutWidth = max(1, contentWidth-1)
	}
	e.renderWidth = layoutWidth
	visual := e.buildVisualLines(layoutWidth, contentWidth+min(paddingX, 1))
	cursorVisualIdx := e.findCursorVisualLine(visual)

	maxVis := e.maxVisibleLines
	if maxVis < 1 {
		maxVis = 5
	}
	if cursorVisualIdx >= 0 {
		if cursorVisualIdx < e.scrollOffset {
			e.scrollOffset = cursorVisualIdx
		} else if cursorVisualIdx >= e.scrollOffset+maxVis {
			e.scrollOffset = cursorVisualIdx - maxVis + 1
		}
	}
	maxOffset := max(0, len(visual)-maxVis)
	if e.scrollOffset > maxOffset {
		e.scrollOffset = maxOffset
	}
	if e.scrollOffset < 0 {
		e.scrollOffset = 0
	}

	end := min(e.scrollOffset+maxVis, len(visual))
	visible := visual[e.scrollOffset:end]
	e.renderedVisibleLineCount = len(visible)
	// Mirrors upstream editor.ts render: every content row is padded to the
	// content width, padding or not. The trailing cells matter beyond looks:
	// compositeTuiLine keeps SGR codes only when a later cell follows them, so
	// an unpadded row ending in the cursor's "\x1b[7m \x1b[0m" loses its reset
	// under an overlay and paints the gap before the overlay inverse. A cursor
	// appended past the content width sits in the right padding, which then
	// gives up one cell.
	padding := strings.Repeat(" ", paddingX)
	for i, line := range visible {
		lineWidth := widthx.VisibleWidth(line)
		right := padding
		if paddingX > 0 && lineWidth > contentWidth {
			right = padding[1:]
		}
		visible[i] = padding + line + strings.Repeat(" ", max(0, contentWidth-lineWidth)) + right
	}

	// Render top border (with "↑ N more" indicator if scrolled down).
	// Mirrors editor.ts:451-465.
	top := e.renderTopBorder(width, e.scrollOffset)
	top = e.renderStatusBorder(width, e.scrollOffset, top)
	out := []string{top}
	out = append(out, visible...)

	// Bottom border (with "↓ N more" indicator if more content below).
	linesBelow := len(visual) - end
	out = append(out, e.renderBottomBorder(width, linesBelow))

	// Render slash-autocomplete popup as additional rows
	// below the editor frame. Mirrors upstream editor.ts:521-528 which
	// appends `autocompleteList.render(contentWidth)` lines to its
	// own output.
	e.renderedAutocompleteHeight = 0
	if len(e.autocompleteItems) > 0 {
		autocomplete := e.renderAutocomplete(contentWidth)
		e.renderedAutocompleteHeight = len(autocomplete)
		for i, line := range autocomplete {
			autocomplete[i] = padding + line + strings.Repeat(" ", max(0, contentWidth-widthx.VisibleWidth(line))) + padding
		}
		out = append(out, autocomplete...)
	}
	return out
}

// HandleMouse activates autocomplete rows and positions the editor cursor on a
// synthesized left click. Press, drag, and release remain unhandled so the
// alternate-screen renderer can own text selection. Mirrors upstream
// Editor.handleMouse. Autocomplete clicks retain the pressed item across scrolling.
func (e *Editor) HandleMouse(event TuiMouseEvent) *TuiMouseDispatchResult {
	if e.remote != nil {
		return e.remoteMouse(event)
	}
	autocompleteStartRow := e.renderedVisibleLineCount + 2
	if len(e.autocompleteItems) > 0 && event.Y >= autocompleteStartRow && event.Y < autocompleteStartRow+e.renderedAutocompleteHeight {
		if event.Type == MouseWheel {
			if event.WheelDelta == 0 {
				return nil
			}
			previous := e.autocompleteCursor
			delta := 1
			if event.WheelDelta < 0 {
				delta = -1
			}
			target := max(0, min(e.autocompleteCursor+delta, len(e.autocompleteItems)-1))
			e.AutocompleteMove(target - e.autocompleteCursor)
			changed := e.autocompleteCursor != previous
			return &TuiMouseDispatchResult{TuiMouseEventResult: TuiMouseEventResult{Handled: true, Focus: true, Render: new(changed)}}
		}
		start, end := e.autocompleteVisibleRange()
		itemIndex := start + event.Y - autocompleteStartRow
		if itemIndex < start || itemIndex >= end {
			return nil
		}
		switch event.Type {
		case MousePress:
			if event.Button != MouseButtonLeft {
				return nil
			}
			e.autocompleteMousePressedIndex = new(itemIndex)
			e.autocompleteCursor = itemIndex
			e.Invalidate()
			return &TuiMouseDispatchResult{TuiMouseEventResult: TuiMouseEventResult{Handled: true, Focus: true}}
		case MouseClick:
			if event.Button != MouseButtonLeft {
				return nil
			}
			if e.autocompleteMousePressedIndex != nil {
				itemIndex = *e.autocompleteMousePressedIndex
			}
			e.autocompleteMousePressedIndex = nil
			e.autocompleteCursor = itemIndex
			e.AutocompleteAccept()
			e.Invalidate()
			return &TuiMouseDispatchResult{TuiMouseEventResult: TuiMouseEventResult{Handled: true, Focus: true}}
		default:
			return nil
		}
	}

	if event.Type != MouseClick || event.Button != MouseButtonLeft {
		return nil
	}
	contentStartRow := 1 // The top border precedes content.
	if event.Y < contentStartRow || event.Y >= contentStartRow+e.renderedVisibleLineCount {
		return &TuiMouseDispatchResult{TuiMouseEventResult: TuiMouseEventResult{Handled: true, Focus: true}}
	}

	visualLines := e.buildVisualLineMap(e.renderWidth)
	visualLineIndex := e.scrollOffset + event.Y - contentStartRow
	if visualLineIndex < 0 || visualLineIndex >= len(visualLines) {
		return &TuiMouseDispatchResult{TuiMouseEventResult: TuiMouseEventResult{Handled: true, Focus: true}}
	}
	visualLine := visualLines[visualLineIndex]
	logicalLine := e.lines[visualLine.logicalLine]
	chunkEnd := min(jsstring.Length(logicalLine), visualLine.startCol+visualLine.length)
	chunk := jsstring.Slice(logicalLine, visualLine.startCol, chunkEnd)
	paddingX := min(e.paddingX, max(0, (event.Width-1)/2))
	targetColumn := max(0, event.X-paddingX)
	visibleColumn := 0
	targetIndex := jsstring.Length(chunk)
	lastGraphemeIndex := 0
	for _, grapheme := range e.segmentLine(chunk) {
		nextColumn := visibleColumn + grapheme.Width
		lastGraphemeIndex = grapheme.Start
		if targetColumn < nextColumn {
			targetIndex = grapheme.Start
			break
		}
		visibleColumn = nextColumn
	}
	isLastSegment := visualLineIndex == len(visualLines)-1 || visualLines[visualLineIndex+1].logicalLine != visualLine.logicalLine
	if !isLastSegment && targetIndex == jsstring.Length(chunk) && len(chunk) > 0 {
		targetIndex = lastGraphemeIndex
	}

	e.cursor[0] = visualLine.logicalLine
	e.setCursorCol(visualLine.startCol + targetIndex)
	e.lastAction = ""
	e.inputHistIdx = -1
	e.inputHistSaved = nil
	if len(e.autocompleteItems) > 0 {
		e.refreshAutocomplete()
	}
	e.Invalidate()
	return &TuiMouseDispatchResult{TuiMouseEventResult: TuiMouseEventResult{Handled: true, Focus: true}}
}

func (e *Editor) autocompleteVisibleRange() (start, end int) {
	n := len(e.autocompleteItems)
	maxVisible := e.autocompleteMax
	if maxVisible <= 0 {
		maxVisible = 5
	}
	start = max(0, min(e.autocompleteCursor-maxVisible/2, n-maxVisible))
	end = min(start+maxVisible, n)
	return start, end
}

// IsBashMode reports whether the buffer is in bash-prefix mode
// (first non-whitespace char is `!`). Used to switch the
// editor border color and to suppress slash-autocomplete in this state.
func (e *Editor) IsBashMode() bool {
	if len(e.lines) == 0 {
		return false
	}
	trimmed := strings.TrimLeft(e.lines[0], " \t")
	return strings.HasPrefix(trimmed, "!")
}

// renderAutocomplete uses the shared SelectList layout, including its centered window and optional per-item descriptions.
func (e *Editor) renderAutocomplete(width int) []string {
	if len(e.autocompleteItems) == 0 {
		return nil
	}
	list := &FilterableList{
		Labels:       make([]string, len(e.autocompleteItems)),
		Descriptions: make([]string, len(e.autocompleteItems)),
		filtered:     make([]int, len(e.autocompleteItems)),
		cursor:       e.autocompleteCursor,
		MaxVisible:   e.AutocompleteMaxVisible(),
	}
	for i, item := range e.autocompleteItems {
		list.Labels[i], list.Descriptions[i], list.filtered[i] = item.Label, item.Description, i
	}
	if strings.HasPrefix(e.autocompletePrefix, "/") {
		list.MinPrimaryColumnWidth, list.MaxPrimaryColumnWidth = 12, 32
	}
	return list.Render(width)
}

// AsyncFileSearcher is an optional capability of the local autocomplete
// provider: it returns a deferred fd-backed @-file search that the editor
// runs off the input thread with cancellation. Mirrors upstream's async
// getFuzzyFileSuggestions (autocomplete.ts:717) so a deep directory walk
// cannot block keystrokes. When ok is false the editor falls back to the
// synchronous GetSuggestions result.
type AsyncFileSearcher interface {
	FileSearchTask(lines []string, cursorLine, cursorCol int) (prefix string, run func(context.Context) []AutocompleteItem, ok bool)
}

// SetAsyncApply installs a scheduler that runs the given closure on the
// host's main loop (single-threaded with keystroke handling). The editor
// uses it to apply async suggestion results without mutating its
// lock-free state from a worker goroutine. When unset, results are
// applied inline on the worker (single-threaded callers / tests only).
func (e *Editor) SetAsyncApply(schedule func(func())) { e.scheduleAsyncApply = schedule }

// SetAutocomplete replaces the provider and cancels pending completion without querying. The host rebuilds extension wrappers through its change callback.
func (e *Editor) SetAutocomplete(p AutocompleteProvider) {
	e.AutocompleteCancel()
	e.autocomplete = p
	if e.autocompleteChanged != nil {
		e.AutocompleteCancel()
		e.autocompleteChanged(p)
		return
	}
	e.autocompleteTriggerCharacters = []rune{'@', '#'}
	if provider, ok := p.(interface{ TriggerCharacters() []string }); ok {
		for _, character := range provider.TriggerCharacters() {
			r, size := jsstring.DecodeRuneInString(character)
			if size == 0 || len(character) != size || jsstring.Length(character) != 1 || r == '/' || widthx.IsJSSpace(r) || slices.Contains(e.autocompleteTriggerCharacters, r) {
				continue
			}
			e.autocompleteTriggerCharacters = append(e.autocompleteTriggerCharacters, r)
		}
	}
	if e.autocompleteMax == 0 {
		e.autocompleteMax = 5
	}
}

// RefreshAutocomplete queries the provider again for the current buffer,
// for a provider whose answer arrived after the keystroke that asked for it.
func (e *Editor) RefreshAutocomplete() {
	if e.autocomplete == nil {
		return
	}
	e.refreshAutocomplete()
}

// AutocompleteOpen reports whether the popup is currently visible.
// Used by the host (interactive.go) to gate Esc/Enter handling.
func (e *Editor) AutocompleteOpen() bool { return e.remote == nil && len(e.autocompleteItems) > 0 }

// AutocompleteCancel dismisses the popup without applying.
func (e *Editor) AutocompleteCancel() {
	e.autocompleteForced = false
	e.autocompleteFromAwaited = false
	e.autocompleteMousePressedIndex = nil
	e.autocompleteRequestID++
	e.asyncSeq++
	if e.asyncCancel != nil {
		e.asyncCancel()
		e.asyncCancel = nil
	}
	if len(e.autocompleteItems) == 0 {
		return
	}
	e.autocompleteItems = nil
	e.autocompleteCursor = 0
	e.autocompletePrefix = ""
	e.Invalidate()
}

// AutocompleteAccept applies the currently-selected suggestion.
// Returns submit=true iff the host should now submit the editor
// (mirrors upstream editor.ts:644: Enter on a slash-name prefix
// inserts and falls through to submit).
func (e *Editor) AutocompleteAccept() (submit bool) {
	if e.asyncAutocomplete != nil {
		e.acceptAsyncAutocomplete(nil)
		return false
	}
	e.autocompleteMousePressedIndex = nil
	if len(e.autocompleteItems) == 0 || e.autocomplete == nil {
		return false
	}
	// Accept the published menu without re-querying; only a moved cursor needs a synchronous refresh or rejection of an awaited result.
	if !e.autocompleteFromAwaited && e.cursor != e.autocompleteQueryCursor {
		res := e.getAutocompleteSuggestions(e.autocompleteForced)
		if res == nil || len(res.Items) == 0 {
			e.autocompleteItems = nil
			e.autocompleteCursor = 0
			e.autocompletePrefix = ""
			e.Invalidate()
			return false
		}
		e.autocompleteItems = res.Items
		e.autocompletePrefix = res.Prefix
		e.autocompleteQueryCursor = e.cursor
		e.autocompleteCursor = bestAutocompleteMatchIndex(res.Items, res.Prefix)
	} else if e.cursor != e.autocompleteQueryCursor {
		// An awaited command result cannot be recomputed on the input loop. Reject a popup whose cursor belongs to older text.
		e.autocompleteItems = nil
		e.autocompleteCursor = 0
		e.autocompletePrefix = ""
		e.Invalidate()
		return false
	}
	item := e.autocompleteItems[e.autocompleteCursor]
	prefix := e.autocompletePrefix
	e.saveHistory()
	e.lastAction = ""
	lines, row, byteCol := e.autocompleteView()
	newLines, nl, nc := e.autocomplete.ApplyCompletion(lines, row, byteCol, item, prefix)
	e.applyAutocompleteState(newLines, nl, nc)
	// A prefix that starts with "/" falls through to submit; anything else
	// (an argument completion) does not (upstream editor.ts
	// tui.select.confirm).
	isSlashName := strings.HasPrefix(prefix, "/")
	e.AutocompleteCancel()
	if !isSlashName && e.OnChange != nil {
		e.OnChange(e.Text())
	}
	return isSlashName
}

// AutocompleteMove moves the selection with keyboard-style wraparound. Mouse callers clamp their target before invoking it.
func (e *Editor) AutocompleteMove(delta int) {
	n := len(e.autocompleteItems)
	if n == 0 {
		return
	}
	c := (e.autocompleteCursor + delta) % n
	if c < 0 {
		c += n
	}
	if c != e.autocompleteCursor {
		e.autocompleteCursor = c
		e.Invalidate()
	}
}

// forceFileAutocomplete triggers a forced file-completion query on Tab
// when the popup is closed. Returns true when suggestions were produced
// and the popup is now open. Mirrors upstream editor.ts
// forceFileAutocomplete → requestAutocomplete({force: true})
// (.upstream/current/packages/tui/src/components/editor.ts:2102).
func (e *Editor) forceFileAutocomplete() bool {
	if e.asyncAutocomplete != nil {
		before := jsstring.Slice(e.lines[e.cursor[0]], 0, e.cursor[1])
		force := !strings.HasPrefix(strings.TrimLeft(before, " \t"), "/") || strings.Contains(strings.TrimLeft(before, " \t"), " ")
		e.requestAsyncAutocomplete(force, true)
		return true
	}
	if e.autocomplete == nil || e.IsBashMode() {
		return false
	}
	lines, row, byteCol := e.autocompleteView()
	if provider, ok := e.autocomplete.(interface{ ShouldTriggerFileCompletion([]string, int, int) bool }); ok && !provider.ShouldTriggerFileCompletion(lines, row, byteCol) {
		return false
	}
	if e.startAutocompleteTask != nil {
		e.autocompleteRequestID++
		e.requestNativeAutocomplete(true, true)
		return true
	}
	res := e.getAutocompleteSuggestions(true)
	if res == nil || len(res.Items) == 0 {
		return false
	}
	if len(res.Items) == 1 {
		e.applySingleForcedCompletion(res.Items[0], res.Prefix)
		return true
	}
	e.autocompleteForced = true
	e.autocompleteItems = res.Items
	e.autocompletePrefix = res.Prefix
	e.autocompleteQueryCursor = e.cursor
	e.autocompleteCursor = bestAutocompleteMatchIndex(res.Items, res.Prefix)
	e.Invalidate()
	return true
}

func (e *Editor) getAutocompleteSuggestions(force bool) *AutocompleteSuggestions {
	lines, row, byteCol := e.autocompleteView()
	if force {
		if provider, ok := e.autocomplete.(ForcefulAutocompleteProvider); ok {
			return provider.GetSuggestionsForce(lines, row, byteCol)
		}
	}
	return e.autocomplete.GetSuggestions(lines, row, byteCol)
}

func (e *Editor) applySingleForcedCompletion(item AutocompleteItem, prefix string) {
	e.saveHistory()
	e.lastAction = ""
	lines, row, byteCol := e.autocompleteView()
	newLines, row, col := e.autocomplete.ApplyCompletion(lines, row, byteCol, item, prefix)
	e.applyAutocompleteState(newLines, row, col)
	e.autocompleteForced = false
	e.autocompleteItems = nil
	e.autocompleteCursor = 0
	e.autocompletePrefix = ""
	e.Invalidate()
	if e.OnChange != nil {
		e.OnChange(e.Text())
	}
}

// naturalAutocompleteContext follows editor.ts buildTriggerPattern and isInSlashCommandContext. Provider path matching is broader than the contexts that automatically open the editor menu.
func (e *Editor) naturalAutocompleteContext() bool {
	if e.cursor[0] < 0 || e.cursor[0] >= len(e.lines) {
		return false
	}
	line := e.lines[e.cursor[0]]
	before := jsstring.Slice(line, 0, e.cursor[1])
	if e.cursor[0] == 0 && strings.HasPrefix(widthx.JSTrim(before), "/") {
		return true
	}
	for index := 0; index < len(before); {
		start := index
		r, size := jsstring.DecodeRuneInString(before[index:])
		index += size
		if !slices.Contains(e.autocompleteTriggerCharacters, r) {
			continue
		}
		if start > 0 {
			previous, _ := utf8.DecodeLastRuneInString(before[:start])
			if !autocompleteSeparator(previous) {
				continue
			}
		}
		rest := before[index:]
		if r == '@' && strings.HasPrefix(rest, `"`) && !strings.Contains(rest[1:], `"`) {
			return true
		}
		if !strings.ContainsFunc(rest, autocompleteSeparator) {
			return true
		}
	}
	return false
}

// refreshAutocomplete re-queries an open popup or an eligible natural trigger context after a mutation.
func (e *Editor) refreshAutocomplete() {
	e.autocompleteMousePressedIndex = nil
	e.autocompleteRequestID++
	if e.asyncAutocomplete != nil {
		if natural, _ := e.naturalAsyncAutocomplete(); !natural {
			e.AutocompleteCancel()
			return
		}
		e.requestAsyncAutocomplete(e.autocompleteForced, false)
		return
	}
	// Always cancel any in-flight async query so its late delivery
	// can't overwrite items belonging to a newer buffer state.
	if e.asyncCancel != nil {
		e.asyncCancel()
		e.asyncCancel = nil
	}
	if !e.AutocompleteOpen() && !e.naturalAutocompleteContext() {
		return
	}
	if e.autocomplete == nil {
		return
	}
	force := e.AutocompleteOpen() && e.autocompleteForced
	if e.startAutocompleteTask != nil {
		e.requestNativeAutocomplete(force, false)
		return
	}
	// Resolve any deferred fd-backed @-file search up front so a deep tree
	// walk runs off the input thread instead of blocking keystrokes.
	var fileTask func(context.Context) []AutocompleteItem
	var filePrefix string
	if afs, ok := e.autocomplete.(AsyncFileSearcher); ok && !e.IsBashMode() {
		lines, row, byteCol := e.autocompleteView()
		if pfx, run, ok := afs.FileSearchTask(lines, row, byteCol); ok {
			fileTask, filePrefix = run, pfx
		}
	}
	if e.autocomplete == nil {
		return
	}
	// Never show the slash-autocomplete popup while the
	// editor is in bash mode (`!` prefix). Mirrors upstream behavior
	// where the autocompleteProvider is short-circuited by
	// `interactive-mode.ts::isBashMode` checks.
	if e.IsBashMode() {
		if len(e.autocompleteItems) > 0 {
			e.autocompleteItems = nil
			e.autocompleteCursor = 0
			e.autocompletePrefix = ""
			e.Invalidate()
		}
		return
	}
	res := e.getAutocompleteSuggestions(force)
	if res == nil || len(res.Items) == 0 {
		// Only clear synchronously when no async fd search is pending;
		// otherwise keep the prior popup until fd returns to avoid flicker.
		if fileTask == nil && len(e.autocompleteItems) > 0 {
			e.autocompleteItems = nil
			e.autocompleteCursor = 0
			e.autocompletePrefix = ""
			e.Invalidate()
		}
		// Complete a deferred filesystem answer if the local provider has one.
		e.kickFileAutocomplete(nil, fileTask, filePrefix)
		return
	}
	requestID, text, cursor := e.autocompleteRequestID, e.Text(), e.cursor
	apply := func() {
		if requestID != e.autocompleteRequestID || text != e.Text() || cursor != e.cursor {
			return
		}
		e.autocompleteMousePressedIndex = nil
		e.autocompleteItems = res.Items
		e.autocompletePrefix = res.Prefix
		e.autocompleteQueryCursor = cursor
		e.autocompleteCursor = bestAutocompleteMatchIndex(res.Items, res.Prefix)
		e.autocompleteForced = force
		e.Invalidate()
	}
	// Upstream awaits even local suggestions. Paint the input before a popup
	// can grow the viewport, and reject results superseded by submit/cancel.
	if e.scheduleAsyncApply != nil {
		e.scheduleAsyncApply(apply)
	} else {
		apply()
	}
	e.kickFileAutocomplete(res, fileTask, filePrefix)
}

// kickFileAutocomplete completes a deferred local filesystem query without running the search on the input loop.
func (e *Editor) kickFileAutocomplete(baseRes *AutocompleteSuggestions, fileTask func(context.Context) []AutocompleteItem, filePrefix string) {
	if fileTask == nil {
		return
	}
	e.asyncSeq++
	sequence := e.asyncSeq
	lifetime := e.autocompleteLifetime
	if lifetime == nil {
		lifetime = context.Background()
	}
	ctx, cancel := context.WithCancel(lifetime)
	e.asyncCancel = cancel
	lines, cursor, request := slices.Clone(e.lines), e.cursor, e.autocompleteRequestID
	work := func() {
		defer cancel()
		if strings.HasPrefix(filePrefix, "@") {
			timer := time.NewTimer(attachmentAutocompleteDebounce)
			defer timer.Stop()
			select {
			case <-timer.C:
			case <-ctx.Done():
				return
			}
		}
		items := fileTask(ctx)
		if ctx.Err() != nil {
			return
		}
		var final []AutocompleteItem
		prefix := filePrefix
		if baseRes != nil {
			final = append(final, baseRes.Items...)
			prefix = baseRes.Prefix
		}
		final = append(final, items...)
		apply := func() {
			if lifetime.Err() != nil || request != e.autocompleteRequestID || sequence != e.asyncSeq || e.cursor != cursor || !slices.Equal(e.lines, lines) {
				return
			}
			e.autocompleteMousePressedIndex = nil
			e.autocompleteItems = final
			e.autocompletePrefix = prefix
			e.autocompleteQueryCursor = cursor
			if e.autocompleteCursor >= len(final) {
				e.autocompleteCursor = 0
			}
			e.Invalidate()
		}
		if e.scheduleAsyncApply != nil {
			e.scheduleAsyncApply(apply)
		} else {
			apply()
		}
	}
	if e.startAutocompleteTask != nil {
		e.startAutocompleteTask(work)
	} else {
		go work()
	}
}

// renderCursorAt highlights the marker-aware grapheme at a UTF-16 offset. At end of line it appends a highlighted space.
func (e *Editor) renderCursorAt(line string, col int, emitMarker bool) string {
	marker := ""
	if emitMarker {
		marker = widthx.CursorMarker
	}
	if col >= jsstring.Length(line) {
		return line + marker + "\033[7m \033[0m"
	}
	end := e.nextSegmentEnd(line, col)
	return jsstring.Slice(line, 0, col) + marker + "\033[7m" + jsstring.Slice(line, col, end) + "\033[0m" + jsstring.Slice(line, end)
}

// pasteMarkerRegexp returns the regexp matching the marker
// "[paste #<id>]" plus the optional " +K lines" / " M chars" suffix
// produced by handlePasteFlush. Mirrors upstream's literal regex in
// expandPasteMarkers.
func pasteMarkerRegexp(id int) *regexp.Regexp {
	return regexp.MustCompile(fmt.Sprintf(`\[paste #%d( (\+\d+ lines|\d+ chars))?\]`, id))
}

// handlePasteFlush inserts a buffered bracketed paste. Large pastes
// (>10 lines or >1000 chars) are replaced with a compact marker and
// stashed in e.pastes for later expansion via GetExpandedText.
// Mirrors upstream editor.ts::handlePaste (line 1084).
func (e *Editor) handlePasteFlush(buf string) {
	if buf == "" {
		return
	}
	// Upstream handlePaste cancels autocomplete first and inserts through a
	// path that never triggers it, so a paste never leaves a popup open that
	// would swallow the following Enter.
	e.AutocompleteCancel()
	e.inputHistIdx = -1
	e.inputHistSaved = nil
	e.lastAction = ""
	e.saveHistory()
	// Some terminals re-encode control bytes inside bracketed paste as
	// CSI-u Ctrl+<letter> sequences (ESC [ <codepoint> ; 5 u). Decode
	// those back to their literal byte before normalization so Ctrl+J
	// becomes a real newline rather than leaking "[106;5u" text.
	decoded := pasteCtrlCSIU.ReplaceAllStringFunc(buf, func(seq string) string {
		match := pasteCtrlCSIU.FindStringSubmatch(seq)
		if len(match) != 2 {
			return seq
		}
		cp, err := strconv.Atoi(match[1])
		if err != nil {
			return seq
		}
		switch {
		case cp >= 97 && cp <= 122:
			return string(rune(cp - 96))
		case cp >= 65 && cp <= 90:
			return string(rune(cp - 64))
		default:
			return seq
		}
	})
	// Normalize line endings and tabs the same way upstream
	// normalizeText does (\r\n / \r → \n, tab → 4 spaces).
	clean := normalizeEditorText(decoded)
	// Filter JavaScript code units so lone surrogates survive paste cleaning.
	units := jsstring.ToUTF16(clean)
	filtered := units[:0]
	for _, unit := range units {
		if unit == '\n' || unit >= 32 {
			filtered = append(filtered, unit)
		}
	}
	text := jsstring.FromUTF16(filtered)
	if text == "" {
		return
	}
	if strings.ContainsRune("/~.", rune(text[0])) && e.cursor[0] >= 0 && e.cursor[0] < len(e.lines) {
		line := e.lines[e.cursor[0]]
		cursor := min(max(e.cursor[1], 0), jsstring.Length(line))
		if cursor > 0 {
			before, _ := jsstring.DecodeRuneInString(jsstring.Slice(line, cursor-1, cursor))
			if isJSWordChar(before) {
				text = " " + text
			}
		}
	}
	lineCount := strings.Count(text, "\n") + 1
	charCount := jsStringLength(text)
	if lineCount > 10 || charCount > 1000 {
		if e.pastes == nil {
			e.pastes = make(map[int]string)
		}
		e.pasteCounter++
		id := e.pasteCounter
		e.pastes[id] = text
		var marker string
		if lineCount > 10 {
			marker = fmt.Sprintf("[paste #%d +%d lines]", id, lineCount)
		} else {
			marker = fmt.Sprintf("[paste #%d %d chars]", id, charCount)
		}
		e.insert(marker)
		return
	}
	e.insert(text)
}

func jsStringLength(text string) int { return jsstring.Length(text) }

func isJSWordChar(r rune) bool {
	return r == '_' || r >= '0' && r <= '9' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z'
}

func (e *Editor) HandleInput(data string) {
	if e.remote != nil {
		e.remote.remote.Input(data)
		return
	}
	beforeText := e.Text()
	notifyChange := true
	defer func() {
		if notifyChange && e.OnChange != nil && e.Text() != beforeText {
			e.OnChange(e.Text())
		}
	}()

	if e.jumpMode != "" {
		if kb := GetTUIKeybindings(); kb.Matches(data, KBEditorJumpForward) || kb.Matches(data, KBEditorJumpBackward) {
			e.jumpMode = ""
			return
		}
		if len(data) > 0 && data[0] >= 0x20 {
			direction := e.jumpMode
			e.jumpMode = ""
			e.jumpToChar(data, direction)
			return
		}
		e.jumpMode = ""
	}

	// ─── Bracketed paste handling ────────────────────────────────────────
	// Mirrors upstream editor.ts::handleInput bracketed paste mode.
	// Content between \x1b[200~ and \x1b[201~ is treated as a single insert.
	if e.isInPaste {
		e.pasteBuffer += data
		if endIndex := strings.Index(e.pasteBuffer, "\x1b[201~"); endIndex >= 0 {
			pasteContent := e.pasteBuffer[:endIndex]
			remainder := e.pasteBuffer[endIndex+len("\x1b[201~"):]
			e.isInPaste = false
			e.pasteBuffer = ""
			if pasteContent != "" {
				e.handlePasteFlush(pasteContent)
			}
			if remainder != "" {
				e.HandleInput(remainder)
			}
		}
		return
	}
	if idx := strings.Index(data, "\x1b[200~"); idx >= 0 {
		// Start of paste: process anything before the marker normally.
		if idx > 0 {
			e.HandleInput(data[:idx])
		}
		e.isInPaste = true
		e.pasteBuffer = ""
		remainder := data[idx+6:] // len("\x1b[200~") == 6
		if remainder != "" {
			e.HandleInput(remainder)
		}
		return
	}

	// Completion selection precedes ordinary editing. Async application remains owned by the host's input ticket.
	kb := GetTUIKeybindings()
	if len(e.autocompleteItems) > 0 {
		switch {
		case kb.Matches(data, KBSelectUp):
			e.AutocompleteMove(-1)
			return
		case kb.Matches(data, KBSelectDown):
			e.AutocompleteMove(1)
			return
		case kb.Matches(data, KBSelectCancel):
			e.AutocompleteCancel()
			return
		case kb.Matches(data, KBInputTab):
			notifyChange = e.AutocompleteAccept()
			return
		case kb.Matches(data, KBSelectConfirm):
			if !e.AutocompleteAccept() {
				notifyChange = false
				return
			}
		}
	} else if kb.Matches(data, KBInputTab) {
		line := e.lines[e.cursor[0]]
		before := jsstring.Slice(line, 0, e.cursor[1])
		if e.cursor[0] == 0 && strings.HasPrefix(widthx.JSTrim(before), "/") && !strings.Contains(strings.TrimLeftFunc(before, widthx.IsJSSpace), " ") {
			e.refreshAutocomplete()
			return
		}
		if e.forceFileAutocomplete() {
			return
		}
	}

	// Dedicated history actions always browse entries instead of moving the
	// cursor. Upstream checks them after the copy, undo, tab, deletion, and
	// kill-ring actions and before cursor movement, newline, and submit
	// (editor.ts handleInput), so a key bound to both keeps the earlier action.
	if !e.matchesActionBeforeHistory(kb, data) {
		if kb.Matches(data, KBEditorHistoryPrevious) {
			e.AutocompleteCancel()
			e.navigateHistory(-1)
			return
		}
		if kb.Matches(data, KBEditorHistoryNext) {
			e.AutocompleteCancel()
			e.navigateHistory(1)
			return
		}
	}

	// ─── Editor dispatch ─────────────────────────────────────────────────
	// All editing actions go through the keybinding registry so user
	// overrides take effect. Mirrors upstream editor.ts handleInput chain
	// (editor.ts:585-820). Each branch corresponds to one keybinding ID;
	// the registry knows which raw terminal sequences map to each ID.
	switch {
	case kb.Matches(data, KBEditorCursorUp):
		switch {
		case e.isOnFirstVisualLine() && (e.isEditorEmpty() || e.inputHistIdx >= 0 || e.cursor[1] == 0):
			e.navigateHistory(-1)
		case e.isOnFirstVisualLine():
			e.lastAction = ""
			e.setCursorCol(0)
			e.Invalidate()
		default:
			e.moveCursor(-1, 0)
		}
	case kb.Matches(data, KBEditorCursorDown):
		switch {
		case e.inputHistIdx >= 0 && e.isOnLastVisualLine():
			e.navigateHistory(1)
		case e.isOnLastVisualLine():
			e.lastAction = ""
			e.setCursorCol(jsstring.Length(e.lines[e.cursor[0]]))
			e.Invalidate()
		default:
			e.moveCursor(1, 0)
		}
	case kb.Matches(data, KBEditorCursorWordRight):
		// alt+right / ctrl+right / alt+f: cursor word forward
		// Mirrors upstream `tui.editor.cursorWordRight` (keybindings.ts:128).
		e.cursorWordForward()
	case kb.Matches(data, KBEditorCursorWordLeft):
		// alt+left / ctrl+left / alt+b: cursor word backward
		// Mirrors upstream `tui.editor.cursorWordLeft` (keybindings.ts:124).
		e.cursorWordBackward()
	case kb.Matches(data, KBEditorCursorRight):
		e.moveCursor(0, 1)
	case kb.Matches(data, KBEditorCursorLeft):
		e.moveCursor(0, -1)
	case matchesEditorNewLine(data, kb):
		if e.shouldSubmitOnBackslashEnter(data, kb) {
			e.backspace()
			e.submitValue()
			return
		}
		e.insertNewline()
		e.refreshAutocomplete()
	case kb.Matches(data, KBInputSubmit):
		if e.DisableSubmit {
			return
		}
		line := e.lines[e.cursor[0]]
		if e.cursor[1] > 0 && jsstring.Slice(line, e.cursor[1]-1, e.cursor[1]) == "\\" {
			e.backspace()
			e.insertNewline()
			e.refreshAutocomplete()
			return
		}
		e.submitValue()
	case kb.Matches(data, KBEditorDeleteWordBack):
		// alt+backspace / ctrl+w: delete word backward
		// Mirrors upstream `tui.editor.deleteWordBackward`
		// (.upstream/current/packages/tui/src/keybindings.ts:100).
		e.deleteWordBackward()
		e.refreshAutocomplete()
	case kb.Matches(data, KBEditorDeleteWordForward):
		// alt+d / alt+delete: delete word forward
		// Mirrors upstream `tui.editor.deleteWordForward` (keybindings.ts:104).
		e.deleteWordForward()
		e.refreshAutocomplete()
	case kb.Matches(data, KBEditorDeleteCharBack):
		// backspace: delete char backward
		e.backspace()
		e.refreshAutocomplete()
	case kb.Matches(data, KBEditorDeleteCharForward):
		// Delete key / Ctrl+D: forward delete
		// Mirrors upstream editor.ts::forwardDelete (keybindings.ts:92).
		e.forwardDelete()
		e.refreshAutocomplete()
	case kb.Matches(data, KBEditorCursorLineStart):
		// Home / Ctrl+A: start of line
		e.lastAction = ""
		e.setCursorCol(0)
		e.Invalidate()
	case kb.Matches(data, KBEditorCursorLineEnd):
		// End / Ctrl+E: end of line
		e.lastAction = ""
		e.setCursorCol(jsstring.Length(e.lines[e.cursor[0]]))
		e.Invalidate()
	case kb.Matches(data, KBEditorDeleteToLineStart):
		// Ctrl+U: kill to line start
		// Mirrors upstream editor.ts::deleteToStartOfLine.
		e.deleteToLineStart()
		e.refreshAutocomplete()
	case kb.Matches(data, KBEditorDeleteToLineEnd):
		// Ctrl+K: kill to end of line
		// Mirrors upstream editor.ts::deleteToEndOfLine.
		e.deleteToLineEnd()
		e.refreshAutocomplete()
	case kb.Matches(data, KBEditorYank):
		// Ctrl+Y: yank
		// Mirrors upstream editor.ts::yank.
		if e.killRing.Len() > 0 {
			e.saveHistory()
			text := e.killRing.Peek()
			e.insert(text)
			e.lastAction = "yank"
		}
	case kb.Matches(data, KBEditorYankPop):
		// Alt+Y: yank-pop; cycles kill ring after a yank
		// Mirrors upstream editor.ts::yankPop.
		if e.lastAction == "yank" && e.killRing.Len() > 1 {
			e.saveHistory()
			// Delete the previously yanked text.
			e.deleteYankedText()
			// Rotate: move last entry to front; new Peek is the previous second.
			e.killRing.Rotate()
			// Re-insert the new top.
			text := e.killRing.Peek()
			e.insert(text)
			e.lastAction = "yank"
		}
	case kb.Matches(data, KBEditorUndo):
		// Ctrl+- (default): undo. Upstream binds undo to `ctrl+-` (which
		// emits \x1f on most terminals); user can rebind via
		// ~/.pig/keybindings.json.
		e.lastAction = ""
		e.undo()
	case kb.Matches(data, KBEditorPageUp):
		e.pageScroll(-1)
	case kb.Matches(data, KBEditorPageDown):
		e.pageScroll(1)
	case kb.Matches(data, KBEditorJumpForward):
		e.jumpMode = "forward"
	case kb.Matches(data, KBEditorJumpBackward):
		e.jumpMode = "backward"
	default:
		if len(data) > 0 && data[0] >= 0x20 {
			e.insertCharacter(data)
			e.refreshAutocomplete()
		} else if ch, ok := DecodePrintableKey(data); ok {
			e.insertCharacter(ch)
			e.refreshAutocomplete()
		}
	}
}

// actionsBeforeHistory are the editor actions upstream's handleInput checks
// before tui.editor.historyPrevious/historyNext.
var actionsBeforeHistory = []TUIKeybinding{
	KBInputCopy, KBEditorUndo, KBInputTab,
	KBEditorDeleteToLineEnd, KBEditorDeleteToLineStart,
	KBEditorDeleteWordBack, KBEditorDeleteWordForward,
	KBEditorDeleteCharBack, KBEditorDeleteCharForward,
	KBEditorYank, KBEditorYankPop,
}

func (e *Editor) matchesActionBeforeHistory(kb *TUIKeybindingsManager, data string) bool {
	for _, action := range actionsBeforeHistory {
		if kb.Matches(data, action) {
			return true
		}
	}
	return false
}

func matchesEditorNewLine(data string, kb *TUIKeybindingsManager) bool {
	return kb.Matches(data, KBInputNewLine) ||
		len(data) > 1 && data[0] == '\n' ||
		data == "\x1b\r" ||
		data == "\x1b[13;2~" ||
		len(data) > 1 && strings.Contains(data, "\x1b") && strings.Contains(data, "\r") ||
		data == "\n"
}

func (e *Editor) shouldSubmitOnBackslashEnter(data string, kb *TUIKeybindingsManager) bool {
	if e.DisableSubmit || !matchesKeyID(data, "enter") {
		return false
	}
	submitKeys := kb.GetKeys(KBInputSubmit)
	hasShiftEnter := slices.Contains(submitKeys, "shift+enter") || slices.Contains(submitKeys, "shift+return")
	if !hasShiftEnter {
		return false
	}
	line := e.lines[e.cursor[0]]
	return e.cursor[1] > 0 && jsstring.Slice(line, e.cursor[1]-1, e.cursor[1]) == "\\"
}

func (e *Editor) submitValue() {
	e.AutocompleteCancel()
	result := widthx.JSTrim(e.GetExpandedText())
	e.lines = []string{""}
	e.cursor = [2]int{0, 0}
	e.jumpMode = ""
	e.preferredVisualCol = nil
	e.snappedFromCursorCol = nil
	e.inputHistIdx = -1
	e.scrollOffset = 0
	e.pastes = nil
	e.pasteCounter = 0
	e.lastAction = ""
	e.history = nil
	e.inputHistSaved = nil
	e.Invalidate()
	if e.OnSubmit != nil {
		e.OnSubmit(result)
	}
}

func (e *Editor) insertCharacter(char string) {
	if isWhitespaceChar(char) || e.lastAction != "type-word" {
		e.saveHistory()
	}
	e.lastAction = "type-word"
	e.insert(char)
}

func normalizeEditorText(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	return jsstring.Canonical(strings.ReplaceAll(text, "\t", "    "))
}

func (e *Editor) insert(s string) {
	if s == "" {
		return
	}
	// Handle multi-line inserts (e.g. from paste or programmatic API).
	if strings.Contains(s, "\n") {
		e.insertMultiLine(s)
		return
	}
	l := e.cursor[0]
	c := e.cursor[1]
	line := e.lines[l]
	e.lines[l] = jsstring.Splice(line, c, c, s)
	e.setCursorCol(c + jsstring.Length(s))
	e.inputHistIdx = -1 // typing exits history-browse mode
	e.inputHistSaved = nil
	e.Invalidate()
}

// insertMultiLine inserts text that may contain newlines.
func (e *Editor) insertMultiLine(s string) {
	parts := strings.Split(s, "\n")
	l, c := e.cursor[0], e.cursor[1]
	line := e.lines[l]
	before, after := jsstring.Slice(line, 0, c), jsstring.Slice(line, c)
	lastCol := jsstring.Length(parts[len(parts)-1])
	parts[0] = jsstring.Canonical(before + parts[0])
	parts[len(parts)-1] = jsstring.Canonical(parts[len(parts)-1] + after)
	e.lines = slices.Concat(e.lines[:l], parts, e.lines[l+1:])
	e.cursor[0] = l + len(parts) - 1
	e.setCursorCol(lastCol)
	e.inputHistIdx = -1
	e.inputHistSaved = nil
	e.Invalidate()
}

// InsertTextAtCursor inserts normalized single- or multi-line text as one undoable edit.
func (e *Editor) InsertTextAtCursor(text string) {
	if e.remote != nil {
		e.remote.remote.InsertTextAtCursor(text)
		return
	}
	if text == "" {
		return
	}
	e.saveHistory()
	e.lastAction = ""
	e.insert(normalizeEditorText(text))
	e.refreshAutocomplete()
	if e.OnChange != nil {
		e.OnChange(e.Text())
	}
}

func (e *Editor) insertNewline() {
	e.saveHistory()
	e.lastAction = ""
	e.inputHistIdx = -1
	e.inputHistSaved = nil
	l := e.cursor[0]
	c := e.cursor[1]
	line := e.lines[l]
	before := jsstring.Slice(line, 0, c)
	after := jsstring.Slice(line, c)
	e.lines = append(e.lines[:l+1], append([]string{after}, e.lines[l+1:]...)...)
	e.lines[l] = before
	e.cursor[0]++
	e.setCursorCol(0)
	e.Invalidate()
}

func (e *Editor) backspace() {
	e.lastAction = ""
	e.inputHistIdx = -1
	e.inputHistSaved = nil
	l := e.cursor[0]
	c := e.cursor[1]
	if c > 0 || l > 0 {
		e.saveHistory()
	}
	if c > 0 {
		line := e.lines[l]
		// If the segment immediately before the cursor is a paste marker,
		// delete the whole marker atomically and compact the paste registry,
		// mirroring upstream editor.ts backspace. Otherwise delete one grapheme.
		if segs := e.segmentLine(jsstring.Slice(line, 0, c)); len(segs) > 0 && isPasteMarker(segs[len(segs)-1].Text) {
			marker := segs[len(segs)-1]
			e.lines[l] = jsstring.Splice(line, marker.Start, c, "")
			e.cursor[1] = marker.Start
			if m := pasteMarkerSingle.FindStringSubmatch(marker.Text); m != nil {
				if targetID, err := strconv.Atoi(m[1]); err == nil {
					e.removePasteFromRegistry(targetID)
				}
			}
		} else {
			start := e.previousSegmentStart(line, c)
			e.lines[l] = jsstring.Splice(line, start, c, "")
			e.cursor[1] = start
		}
	} else if l > 0 {
		prev := e.lines[l-1]
		e.cursor[1] = jsstring.Length(prev)
		e.lines[l-1] = jsstring.Canonical(prev + e.lines[l])
		e.lines = append(e.lines[:l], e.lines[l+1:]...)
		e.cursor[0]--
	}
	if c > 0 || l > 0 {
		e.setCursorCol(e.cursor[1])
	}
	e.Invalidate()
}

// removePasteFromRegistry deletes targetID from the paste registry, shifts
// every higher id down by one, and renumbers the surviving [paste #N] markers
// in the buffer text so ids stay contiguous. Mirrors upstream editor.ts
// backspace registry compaction (deleting [paste #1] renumbers [paste #2] to
// [paste #1], and so on, independent of marker order in the text).
func (e *Editor) removePasteFromRegistry(targetID int) {
	if e.pastes != nil {
		delete(e.pastes, targetID)
		higher := make([]int, 0, len(e.pastes))
		for id := range e.pastes {
			if id > targetID {
				higher = append(higher, id)
			}
		}
		slices.Sort(higher)
		for _, id := range higher {
			e.pastes[id-1] = e.pastes[id]
			delete(e.pastes, id)
		}
	}
	e.pasteCounter--
	for i, ln := range e.lines {
		e.lines[i] = pasteMarkerRegex.ReplaceAllStringFunc(ln, func(marker string) string {
			m := pasteMarkerRegex.FindStringSubmatch(marker)
			if m == nil {
				return marker
			}
			id, err := strconv.Atoi(m[1])
			if err != nil || id <= targetID {
				return marker
			}
			return "[paste #" + strconv.Itoa(id-1) + m[2] + "]"
		})
	}
}

// forwardDelete deletes the character at the cursor (Delete key / Ctrl+D).
// Mirrors upstream editor.ts::forwardDelete.
func (e *Editor) forwardDelete() {
	e.lastAction = ""
	e.inputHistIdx = -1
	e.inputHistSaved = nil
	l := e.cursor[0]
	c := e.cursor[1]
	line := e.lines[l]
	if c < jsstring.Length(line) || l < len(e.lines)-1 {
		e.saveHistory()
	}
	if c < jsstring.Length(line) {
		end := e.nextSegmentEnd(line, c)
		e.lines[l] = jsstring.Splice(line, c, end, "")
	} else if l < len(e.lines)-1 {
		// Join with next line.
		e.lines[l] = jsstring.Canonical(line + e.lines[l+1])
		e.lines = append(e.lines[:l+1], e.lines[l+2:]...)
	}
	e.Invalidate()
}

func (e *Editor) moveToVisualLine(visual []editorVisualLine, currentVisualLine, targetVisualLine int) {
	if currentVisualLine < 0 || currentVisualLine >= len(visual) || targetVisualLine < 0 || targetVisualLine >= len(visual) {
		return
	}
	currentVL := visual[currentVisualLine]
	targetVL := visual[targetVisualLine]
	currentVisualCol := e.cursor[1] - currentVL.startCol
	if e.snappedFromCursorCol != nil {
		vlIndex := e.findVisualLineAt(visual, currentVL.logicalLine, *e.snappedFromCursorCol)
		currentVisualCol = *e.snappedFromCursorCol - visual[vlIndex].startCol
	}

	isLastSourceSegment := currentVisualLine == len(visual)-1 || visual[currentVisualLine+1].logicalLine != currentVL.logicalLine
	sourceMaxVisualCol := currentVL.length
	if !isLastSourceSegment {
		sourceMaxVisualCol = max(0, currentVL.length-1)
	}

	isLastTargetSegment := targetVisualLine == len(visual)-1 || visual[targetVisualLine+1].logicalLine != targetVL.logicalLine
	targetMaxVisualCol := targetVL.length
	if !isLastTargetSegment {
		targetMaxVisualCol = max(0, targetVL.length-1)
	}

	moveToVisualCol := e.computeVerticalMoveColumn(currentVisualCol, sourceMaxVisualCol, targetMaxVisualCol)
	e.cursor[0] = targetVL.logicalLine
	targetCol := targetVL.startCol + moveToVisualCol
	e.cursor[1] = min(targetCol, jsstring.Length(e.lines[targetVL.logicalLine]))
	for _, segment := range e.segmentLine(e.lines[targetVL.logicalLine]) {
		if segment.Start > e.cursor[1] {
			break
		}
		if segment.End-segment.Start <= 1 {
			continue
		}
		if e.cursor[1] < segment.End {
			if segment.Start < targetVL.startCol && targetVisualLine > currentVisualLine {
				next := targetVisualLine + 1
				for next < len(visual) && visual[next].logicalLine == targetVL.logicalLine && visual[next].startCol < segment.End {
					next++
				}
				if next < len(visual) {
					e.moveToVisualLine(visual, currentVisualLine, next)
					return
				}
			}
			e.snappedFromCursorCol = new(e.cursor[1])
			e.cursor[1] = segment.Start
			e.Invalidate()
			return
		}
	}
	e.snappedFromCursorCol = nil
	e.Invalidate()
}

func (e *Editor) computeVerticalMoveColumn(currentVisualCol, sourceMaxVisualCol, targetMaxVisualCol int) int {
	hasPreferred := e.preferredVisualCol != nil
	cursorInMiddle := currentVisualCol < sourceMaxVisualCol
	targetTooShort := targetMaxVisualCol < currentVisualCol

	if !hasPreferred || cursorInMiddle {
		if targetTooShort {
			e.preferredVisualCol = new(currentVisualCol)
			return targetMaxVisualCol
		}
		e.preferredVisualCol = nil
		return currentVisualCol
	}

	targetCantFitPreferred := targetMaxVisualCol < *e.preferredVisualCol
	if targetTooShort || targetCantFitPreferred {
		return targetMaxVisualCol
	}

	result := *e.preferredVisualCol
	e.preferredVisualCol = nil
	return result
}

func (e *Editor) moveCursor(deltaLine, deltaCol int) {
	defer func() {
		if e.AutocompleteOpen() {
			e.refreshAutocomplete()
		}
	}()
	e.lastAction = ""
	visual := e.buildVisualLineMap(e.renderWidth)
	currentVisualLine := e.findCurrentVisualLine(visual)

	if deltaLine != 0 {
		targetVisualLine := currentVisualLine + deltaLine
		if targetVisualLine >= 0 && targetVisualLine < len(visual) {
			e.moveToVisualLine(visual, currentVisualLine, targetVisualLine)
		}
	}

	if deltaCol == 0 {
		return
	}
	currentLine := e.lines[e.cursor[0]]
	if deltaCol > 0 {
		switch {
		case e.cursor[1] < jsstring.Length(currentLine):
			e.setCursorCol(e.nextSegmentEnd(currentLine, e.cursor[1]))
		case e.cursor[0] < len(e.lines)-1:
			e.cursor[0]++
			e.setCursorCol(0)
		case currentVisualLine >= 0 && currentVisualLine < len(visual):
			currentVL := visual[currentVisualLine]
			e.preferredVisualCol = new(e.cursor[1] - currentVL.startCol)
		}
	} else {
		switch {
		case e.cursor[1] > 0:
			e.setCursorCol(e.previousSegmentStart(currentLine, e.cursor[1]))
		case e.cursor[0] > 0:
			e.cursor[0]--
			e.setCursorCol(jsstring.Length(e.lines[e.cursor[0]]))
		}
	}
	e.Invalidate()
}

func (e *Editor) pageScroll(direction int) {
	e.lastAction = ""
	visual := e.buildVisualLineMap(e.renderWidth)
	currentVisualLine := e.findCurrentVisualLine(visual)
	pageSize := max(5, e.maxVisibleLines)
	targetVisualLine := currentVisualLine + direction*pageSize
	targetVisualLine = max(0, min(len(visual)-1, targetVisualLine))
	e.moveToVisualLine(visual, currentVisualLine, targetVisualLine)
}

func (e *Editor) jumpToChar(char, direction string) {
	e.lastAction = ""
	forward := direction == "forward"
	end, step := -1, -1
	if forward {
		end, step = len(e.lines), 1
	}
	for row := e.cursor[0]; row != end; row += step {
		line := e.lines[row]
		from := jsstring.Length(line)
		if forward {
			from = 0
		}
		if row == e.cursor[0] {
			from = e.cursor[1] - 1
			if forward {
				from = e.cursor[1] + 1
			}
		}
		var index int
		if forward {
			index = jsstring.IndexOf(line, char, from)
		} else {
			index = jsstring.LastIndexOf(line, char, from)
		}
		if index >= 0 {
			e.cursor[0] = row
			e.setCursorCol(index)
			e.Invalidate()
			return
		}
	}
}

func (e *Editor) undo() {
	// Undo leaves history browsing, as upstream undo calls exitHistoryBrowsing.
	e.inputHistIdx = -1
	e.inputHistSaved = nil
	if len(e.history) == 0 {
		return
	}
	last := len(e.history) - 1
	state := e.history[last]
	e.history[last] = editorState{}
	e.history = e.history[:last]
	e.lines = state.lines
	e.cursor = state.cursor
	e.pastes = state.pastes
	e.pasteCounter = state.pasteCounter
	e.lastAction = ""
	e.preferredVisualCol = nil
	e.Invalidate()
}

func (e *Editor) cursorWordBackward() {
	e.lastAction = ""
	l, c := e.cursor[0], e.cursor[1]
	if c == 0 {
		if l > 0 {
			e.cursor[0]--
			e.setCursorCol(jsstring.Length(e.lines[e.cursor[0]]))
			e.Invalidate()
		}
		return
	}
	e.setCursorCol(e.prevWordStart(e.lines[l], c))
	e.Invalidate()
}

func (e *Editor) cursorWordForward() {
	e.lastAction = ""
	l, c := e.cursor[0], e.cursor[1]
	if c >= jsstring.Length(e.lines[l]) {
		if l < len(e.lines)-1 {
			e.cursor[0]++
			e.setCursorCol(0)
			e.Invalidate()
		}
		return
	}
	e.setCursorCol(e.nextWordEnd(e.lines[l], c))
	e.Invalidate()
}

func (e *Editor) deleteWordBackward() {
	e.inputHistIdx = -1
	e.inputHistSaved = nil
	l := e.cursor[0]
	c := e.cursor[1]
	if c == 0 {
		if l == 0 {
			return
		}
		e.saveHistory()
		wasKill := e.lastAction == "kill"
		e.killRing.Push("\n", true, wasKill)
		prev := e.lines[l-1]
		e.cursor[0] = l - 1
		e.setCursorCol(jsstring.Length(prev))
		e.lines[l-1] = jsstring.Canonical(prev + e.lines[l])
		e.lines = append(e.lines[:l], e.lines[l+1:]...)
		e.lastAction = "kill"
		e.Invalidate()
		return
	}
	e.saveHistory()
	line := e.lines[l]
	start := e.prevWordStart(line, c)
	killed := jsstring.Slice(line, start, c)
	wasKill := e.lastAction == "kill"
	e.killRing.Push(killed, true, wasKill)
	e.lastAction = "kill"
	e.lines[l] = jsstring.Splice(line, start, c, "")
	e.setCursorCol(start)
	e.Invalidate()
}

func (e *Editor) deleteWordForward() {
	e.inputHistIdx = -1
	e.inputHistSaved = nil
	l := e.cursor[0]
	c := e.cursor[1]
	line := e.lines[l]
	if c >= jsstring.Length(line) {
		if l >= len(e.lines)-1 {
			return
		}
		e.saveHistory()
		wasKill := e.lastAction == "kill"
		e.killRing.Push("\n", false, wasKill)
		e.lines[l] = jsstring.Canonical(line + e.lines[l+1])
		e.lines = append(e.lines[:l+1], e.lines[l+2:]...)
		e.lastAction = "kill"
		e.Invalidate()
		return
	}
	e.saveHistory()
	end := e.nextWordEnd(line, c)
	e.setCursorCol(c)
	killed := jsstring.Slice(line, c, end)
	wasKill := e.lastAction == "kill"
	e.killRing.Push(killed, false, wasKill)
	e.lastAction = "kill"
	e.lines[l] = jsstring.Splice(line, c, end, "")
	e.Invalidate()
}

// deleteToLineEnd kills from cursor to end of the current line.
// If cursor is already at end-of-line and this is not the last line,
// kills the newline (joining with next line).
// Mirrors upstream editor.ts::deleteToEndOfLine.
func (e *Editor) deleteToLineEnd() {
	e.inputHistIdx = -1
	e.inputHistSaved = nil
	l := e.cursor[0]
	c := e.cursor[1]
	wasKill := e.lastAction == "kill"
	if c < jsstring.Length(e.lines[l]) {
		e.saveHistory()
		killed := jsstring.Slice(e.lines[l], c)
		e.killRing.Push(killed, false, wasKill)
		e.lines[l] = jsstring.Slice(e.lines[l], 0, c)
		e.lastAction = "kill"
		e.Invalidate()
	} else if l < len(e.lines)-1 {
		e.saveHistory()
		e.killRing.Push("\n", false, wasKill)
		e.lines[l] = jsstring.Canonical(e.lines[l] + e.lines[l+1])
		e.lines = append(e.lines[:l+1], e.lines[l+2:]...)
		e.lastAction = "kill"
		e.Invalidate()
	}
}

// deleteToLineStart kills from line start to cursor.
// If cursor is at col 0 and not on first line, kills the newline.
// Mirrors upstream editor.ts::deleteToStartOfLine.
func (e *Editor) deleteToLineStart() {
	e.inputHistIdx = -1
	e.inputHistSaved = nil
	l := e.cursor[0]
	c := e.cursor[1]
	wasKill := e.lastAction == "kill"
	if c > 0 {
		e.saveHistory()
		killed := jsstring.Slice(e.lines[l], 0, c)
		e.killRing.Push(killed, true, wasKill)
		e.lines[l] = jsstring.Slice(e.lines[l], c)
		e.setCursorCol(0)
		e.lastAction = "kill"
		e.Invalidate()
	} else if l > 0 {
		e.saveHistory()
		e.killRing.Push("\n", true, wasKill)
		prev := e.lines[l-1]
		e.setCursorCol(jsstring.Length(prev))
		e.lines[l-1] = jsstring.Canonical(prev + e.lines[l])
		e.lines = append(e.lines[:l], e.lines[l+1:]...)
		e.cursor[0]--
		e.lastAction = "kill"
		e.Invalidate()
	}
}

// deleteYankedText removes the most recent ring entry immediately before the cursor, including its line breaks.
func (e *Editor) deleteYankedText() {
	text := e.killRing.Peek()
	if text == "" {
		return
	}
	parts := strings.Split(text, "\n")
	l, c := e.cursor[0], e.cursor[1]
	startLine := l - len(parts) + 1
	startCol := c - jsstring.Length(text)
	if len(parts) > 1 {
		startCol = jsstring.Length(e.lines[startLine]) - jsstring.Length(parts[0])
	}
	before := jsstring.Slice(e.lines[startLine], 0, startCol)
	after := jsstring.Slice(e.lines[l], c)
	e.lines = slices.Concat(e.lines[:startLine], []string{jsstring.Canonical(before + after)}, e.lines[l+1:])
	e.cursor[0] = startLine
	e.setCursorCol(startCol)
	e.Invalidate()
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

// wrapText wraps s to lines of at most width runes.
// wrapText wraps s to lines of at most `width` visible columns, preserving
// active ANSI/OSC 8 hyperlink state across line breaks.
//
// Delegates to widthx.WrapTextWithAnsi which mirrors upstream
// `wrapTextWithAnsi` (utils.ts:596). The previous in-place implementation
// used `len(stripANSI(line))` for width measurement which was wrong for
// CJK/emoji and could not break overlong tokens.
func wrapText(s string, width int) []string {
	return widthx.WrapTextWithAnsi(s, width)
}

// WrapText wraps text at width, preserving ANSI escape codes across
// line breaks. Exported for use by tool renderers.
func WrapText(s string, width int) []string {
	return widthx.WrapTextWithAnsi(s, width)
}
