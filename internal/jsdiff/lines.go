package jsdiff

import "strings"

// Line diffs over the changed window (Pi-Bolt bf325340, "edit previews and patches diff only the changed lines").
//
// diffLines tokenizes both texts in full, although an edit changes a few lines of a large file. DiffLineRuns runs the same Myers search as
// myers (jsdiff 8's Diff.diffWithOptionsObj with default options), step for step, so it returns the same parts, but:
//   - the common prefix is found by comparing bytes; jsdiff's first step consumes exactly those lines, and no later step reads them;
//   - lines are located on demand, so lines the search never reaches are never located;
//   - on the diagonal that ends at the end of both texts, reaching the common suffix ends the walk; jsdiff compares those lines one by one
//     and finds them all equal.
//
// Diffing a cut-out window instead is not equivalent: lines after the change can make jsdiff align the change differently ("b c" against
// "c b" diffs as "-b c +b", but with a common "b" appended jsdiff returns "-b c b +b", not "-b c +b b"). lines_test.go compares the
// result with the full search on randomized inputs.

// LineRun is one part of a line diff: Count lines kept, added or removed. Start is the index of its first line in the text its lines come
// from: the old text when Removed, the new text otherwise.
type LineRun struct {
	Start   int
	Count   int
	Added   bool
	Removed bool
}

// LineDiff is jsdiff's diffLines(oldStr, newStr) as runs of lines read from the texts on demand.
type LineDiff struct {
	Runs []LineRun
	old  lazyLines
	new  lazyLines
}

// Line returns line i of run as jsdiff tokenizes it: with its terminating "\n", which only the last line of a text can lack.
func (d *LineDiff) Line(run LineRun, i int) string {
	return d.side(run).line(run.Start + i)
}

// Value is the run's jsdiff change value: its lines joined, a substring of the text they come from.
func (d *LineDiff) Value(run LineRun) string {
	lines := d.side(run)
	return lines.text[lines.offset(run.Start):lines.offset(run.Start+run.Count)]
}

func (d *LineDiff) side(run LineRun) *lazyLines {
	if run.Removed {
		return &d.old
	}
	return &d.new
}

// lazyLines locates the line tokens of text on demand, going forward or backward from a known line start.
type lazyLines struct {
	text  string
	count int // number of line tokens
	base  int // index of the line that starts at forward[0]
	// forward[k] is the start of line base+k; backward[k] the start of line base-1-k.
	forward  []int
	backward []int
}

func newLazyLines(text string, count, base, baseOffset int) lazyLines {
	return lazyLines{text: text, count: count, base: base, forward: []int{baseOffset}}
}

// offset is the byte offset at which line i starts, len(text) for i == count.
func (l *lazyLines) offset(i int) int {
	if i <= 0 {
		return 0
	}
	if i >= l.count {
		return len(l.text)
	}
	if i >= l.base {
		for k := i - l.base; len(l.forward) <= k; {
			start := l.forward[len(l.forward)-1]
			next := len(l.text)
			if nl := strings.IndexByte(l.text[start:], '\n'); nl >= 0 {
				next = start + nl + 1
			}
			l.forward = append(l.forward, next)
		}
		return l.forward[i-l.base]
	}
	for k := l.base - 1 - i; len(l.backward) <= k; {
		end := l.forward[0]
		if len(l.backward) > 0 {
			end = l.backward[len(l.backward)-1]
		}
		// end is a line start after at least one line, so text[end-1] is that line's "\n".
		l.backward = append(l.backward, strings.LastIndexByte(l.text[:end-1], '\n')+1)
	}
	return l.backward[l.base-1-i]
}

func (l *lazyLines) line(i int) string {
	return l.text[l.offset(i):l.offset(i+1)]
}

// countLines is the number of jsdiff line tokens in text[from:to], where from is a line start.
func countLines(text string, from, to int) int {
	n := strings.Count(text[from:to], "\n")
	if to > from && text[to-1] != '\n' {
		n++
	}
	return n
}

const commonRunInitialChunk = 256

// commonPrefixLength is the length of the longest common prefix of a and b. Chunks double in size and compare as whole strings.
func commonPrefixLength(a, b string) int {
	limit := min(len(a), len(b))
	low, step := 0, commonRunInitialChunk
	for low < limit {
		high := min(low+step, limit)
		if a[low:high] != b[low:high] {
			for a[low] == b[low] {
				low++
			}
			return low
		}
		low = high
		step *= 2
	}
	return limit
}

// commonSuffixLength is the length of the longest common suffix of a and b, at most limit.
func commonSuffixLength(a, b string, limit int) int {
	low, step := 0, commonRunInitialChunk
	for low < limit {
		high := min(low+step, limit)
		if a[len(a)-high:len(a)-low] != b[len(b)-high:len(b)-low] {
			for a[len(a)-low-1] == b[len(b)-low-1] {
				low++
			}
			return low
		}
		low = high
		step *= 2
	}
	return limit
}

// DiffLineRuns is jsdiff's diffLines(oldStr, newStr) with default options, without the change values.
func DiffLineRuns(oldStr, newStr string) *LineDiff {
	if oldStr == newStr {
		count := countLines(oldStr, 0, len(oldStr))
		lines := newLazyLines(oldStr, count, 0, 0)
		d := &LineDiff{old: lines, new: lines}
		if count > 0 {
			d.Runs = []LineRun{{Count: count}}
		}
		return d
	}
	// Common prefix lines end at the last "\n" of the common prefix. The texts differ, so a final line without "\n" is not common to both.
	prefixEnd := strings.LastIndexByte(oldStr[:commonPrefixLength(oldStr, newStr)], '\n') + 1
	// Common suffix lines start at a line start in both texts, after the prefix lines.
	suffixChars := commonSuffixLength(oldStr, newStr, min(len(oldStr), len(newStr))-prefixEnd)
	oldTail, newTail := len(oldStr)-suffixChars, len(newStr)-suffixChars
	suffixLength := suffixChars
	if (oldTail != 0 && oldStr[oldTail-1] != '\n') || (newTail != 0 && newStr[newTail-1] != '\n') {
		// The suffix starts mid-line in one text: its lines start after the next "\n", at the same distance from the end in both texts.
		suffixLength = 0
		if nl := strings.IndexByte(oldStr[oldTail:], '\n'); nl >= 0 {
			suffixLength = len(oldStr) - (oldTail + nl + 1)
		}
	}
	prefixLines := strings.Count(oldStr[:prefixEnd], "\n")
	suffixLines := countLines(oldStr, len(oldStr)-suffixLength, len(oldStr))
	d := &LineDiff{
		old: newLazyLines(oldStr, countLines(oldStr, 0, len(oldStr)), prefixLines, prefixEnd),
		new: newLazyLines(newStr, countLines(newStr, 0, len(newStr)), prefixLines, prefixEnd),
	}
	d.Runs = d.search(prefixLines, suffixLines)
	return d
}

// search is myers over the lazily located lines, starting after the common prefix (which jsdiff's first step consumes on diagonal 0).
func (d *LineDiff) search(prefixLines, suffixLines int) []LineRun {
	oldLen, newLen := d.old.count, d.new.count
	endDiagonal := oldLen - newLen
	oldSuffixStart := oldLen - suffixLines
	extractCommon := func(base *path, diagonal int) int {
		oldPos := base.oldPos
		newPos := oldPos - diagonal
		common := 0
		for newPos+1 < newLen && oldPos+1 < oldLen {
			if diagonal == endDiagonal && oldPos+1 >= oldSuffixStart {
				// Both sides are in the common suffix at the same distance from the end: every remaining line matches.
				remaining := oldLen - 1 - oldPos
				oldPos += remaining
				newPos += remaining
				common += remaining
				break
			}
			if d.old.line(oldPos+1) != d.new.line(newPos+1) {
				break
			}
			newPos++
			oldPos++
			common++
		}
		if common > 0 {
			base.last = &component{count: common, previous: base.last}
		}
		base.oldPos = oldPos
		return newPos
	}

	first := &path{oldPos: prefixLines - 1}
	if prefixLines > 0 {
		first.last = &component{count: prefixLines}
	}
	newPos := prefixLines - 1
	if first.oldPos+1 >= oldLen && newPos+1 >= newLen {
		return lineRuns(first.last)
	}
	bestPath := map[int]*path{0: first}
	editLength := 1
	maxEditLength := newLen + oldLen
	const unbounded = int(^uint(0) >> 1)
	minDiagonal, maxDiagonal := -unbounded, unbounded
	for editLength <= maxEditLength {
		for diagonal := max(minDiagonal, -editLength); diagonal <= min(maxDiagonal, editLength); diagonal += 2 {
			removePath, addPath := bestPath[diagonal-1], bestPath[diagonal+1]
			if removePath != nil {
				bestPath[diagonal-1] = nil
			}
			canAdd := false
			if addPath != nil {
				addPathNewPos := addPath.oldPos - diagonal
				canAdd = 0 <= addPathNewPos && addPathNewPos < newLen
			}
			canRemove := removePath != nil && removePath.oldPos+1 < oldLen
			if !canAdd && !canRemove {
				bestPath[diagonal] = nil
				continue
			}
			var base *path
			if !canRemove || (canAdd && removePath.oldPos < addPath.oldPos) {
				base = addToPath(addPath, true, false, 0)
			} else {
				base = addToPath(removePath, false, true, 1)
			}
			newPos = extractCommon(base, diagonal)
			if base.oldPos+1 >= oldLen && newPos+1 >= newLen {
				return lineRuns(base.last)
			}
			bestPath[diagonal] = base
			if base.oldPos+1 >= oldLen {
				maxDiagonal = min(maxDiagonal, diagonal-1)
			}
			if newPos+1 >= newLen {
				minDiagonal = max(minDiagonal, diagonal+1)
			}
		}
		editLength++
	}
	return nil
}

// lineRuns is buildValues without the values: the components in order, each with the index of its first line.
func lineRuns(last *component) []LineRun {
	n := 0
	for c := last; c != nil; c = c.previous {
		n++
	}
	runs := make([]LineRun, n)
	for c := last; c != nil; c = c.previous {
		n--
		runs[n] = LineRun{Count: c.count, Added: c.added, Removed: c.removed}
	}
	newPos, oldPos := 0, 0
	for i := range runs {
		run := &runs[i]
		if run.Removed {
			run.Start = oldPos
			oldPos += run.Count
			continue
		}
		run.Start = newPos
		newPos += run.Count
		if !run.Added {
			oldPos += run.Count
		}
	}
	return runs
}

// DiffLines is jsdiff's diffLines(oldStr, newStr) with default options: Myers over line tokens compared exactly.
func DiffLines(oldStr, newStr string) []Change {
	d := DiffLineRuns(oldStr, newStr)
	changes := make([]Change, len(d.Runs))
	for i, run := range d.Runs {
		changes[i] = Change{Value: d.Value(run), Count: run.Count, Added: run.Added, Removed: run.Removed}
	}
	return changes
}
