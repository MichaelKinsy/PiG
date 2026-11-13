// Code generated from tui/widthx/wrap_cache.go by tui/sdk_tui_copy_test.go; DO NOT EDIT.

package widthx

// maxCachedRows bounds the finished rows a LineWrapCache keeps: the tool cards that stream hold far fewer, and a text of many
// thousand lines would otherwise retain a memo as large as its own rows.
//
// pig additive (D112): Pi wraps every line of a text on every render and keeps no rows; the rows drawn are the same whatever the
// bound is, which only sets how many of them a Text holds between two renders.
const maxCachedRows = 4096

// LineWrapCache wraps text as WrapTextWithAnsi does and keeps the rows of each logical line of the text it wrapped last. A text
// that grows or changes at its end, as a streaming tool call's does, wraps and finishes again only the lines that differ from
// the last text: a line is served from the cache when its text, the ANSI state the lines before it left and the width are
// those of the line at its index last time, which is every input of its rows. The cache holds the last text only, and of that
// text the first lines whose rows add up to maxCachedRows: what it retains is bounded however long the text, and the lines
// after them are wrapped again by every call.
//
// The zero value is an empty cache; it is not safe for concurrent use.
type LineWrapCache struct {
	width int
	lines []wrapCacheLine
}

// wrapCacheLine is one logical line with the ANSI state after it (the state before it is the one after the line above) and its
// finished rows. out is nil for the state with nothing active, which most lines leave.
type wrapCacheLine struct {
	text string
	out  *AnsiCodeTracker
	rows []string
}

// trackerIs reports whether the state saved in p, nil for the empty state, is tr.
func trackerIs(p *AnsiCodeTracker, tr AnsiCodeTracker) bool {
	if p == nil {
		return tr == AnsiCodeTracker{}
	}
	return *p == tr
}

// Reset forgets the last text, for a caller whose finish function changed.
func (c *LineWrapCache) Reset() {
	c.width = 0
	c.lines = nil
}

// Wrap is WrapTextWithAnsi(text, width) with finish applied to each row. finish must give the same row for the same argument
// until Reset is called; nil finishes nothing. Rows of a line that the cache serves are the strings the last call returned.
func (c *LineWrapCache) Wrap(text string, width int, finish func(row string) string) []string {
	if text == "" {
		if finish == nil {
			return []string{""}
		}
		return []string{finish("")}
	}
	inputLines := wrapInputLines(text)
	previous := c.lines
	if c.width != width {
		previous = nil
	}
	// The lines are rewritten in place: the entry at index i is read before it is written, and only previous[i-1].out, saved
	// in oldOut, is needed after the entry above it is overwritten.
	next := c.lines
	var tr AnsiCodeTracker
	var oldOut *AnsiCodeTracker
	result := make([]string, 0, len(inputLines))
	// The lines kept are a prefix of the text, as many as their rows add up to maxCachedRows: lines are memoized and served only
	// while the prefix is open (i == kept), so the entry above a served line is always the one that was read. The first line
	// that would pass the bound closes it, and every line after it is wrapped again.
	kept, rowsKept := 0, 0
	for i, line := range inputLines {
		open := kept == i
		var old wrapCacheLine
		if open && i < len(previous) {
			old = previous[i]
		}
		var cached wrapCacheLine
		if open && i < len(previous) && old.text == line && trackerIs(oldOut, tr) {
			cached = old
			result = append(result, old.rows...)
			tr = AnsiCodeTracker{}
			if old.out != nil {
				tr = *old.out
			}
		} else {
			prefix := ""
			if i > 0 {
				prefix = tr.ActiveCodes()
			}
			rows := wrapSingleLine(prefix+line, width)
			if finish != nil {
				for j, row := range rows {
					rows[j] = finish(row)
				}
			}
			tr.UpdateFromText(line)
			cached = wrapCacheLine{text: line, rows: rows}
			if tr != (AnsiCodeTracker{}) {
				state := tr
				cached.out = &state
			}
			result = append(result, rows...)
		}
		if !open || rowsKept+len(cached.rows) > maxCachedRows {
			continue
		}
		oldOut = old.out
		rowsKept += len(cached.rows)
		if i < len(next) {
			next[i] = cached
		} else {
			next = append(next, cached)
		}
		kept = i + 1
	}
	clear(next[kept:])
	c.width = width
	c.lines = next[:kept]
	return result
}
