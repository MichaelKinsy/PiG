package tui

// Ports packages/tui/src/components/markdown.ts (nested list block rendering).

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/internal/lazyregexp"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

type markdownList struct {
	ordered, loose bool
	start          int
	items          []markdownListEntry
}
type markdownListEntry struct {
	marker string
	blocks []markdownListBlock
	// spacedEmpty reports an empty item whose line holds spaces after the marker.
	spacedEmpty bool
}
type markdownListBlock struct {
	text string
	list *markdownList
	// merged are the indexes of the text's lines that marked's code tokenizer merged into the paragraph before them, four leading spaces removed: they are text whatever they now look like.
	merged []int
}

// markdownLatexBlockAt reports whether a block LaTeX token starts at lines[index]; pi-tui's latexBlock extension start() clips a paragraph there.
func markdownLatexBlockAt(lines []string, index int) bool {
	if !blockLatexStart(lines[index]) {
		return false
	}
	_, ok := tokenizeBlockLatex(strings.Join(lines[index:], "\n"))
	return ok
}

// markdownBlankRunContinues reports that the blank lines from index j are followed by a line indented to at least contentIndent.
func markdownBlankRunContinues(lines []string, j, contentIndent int) bool {
	for j < len(lines) && widthx.JSTrim(lines[j]) == "" {
		j++
	}
	return j < len(lines) && markdownIndent(lines[j]) >= contentIndent
}

func markdownIndent(line string) int     { return len(line) - len(strings.TrimLeft(line, " ")) }
func markdownOrdered(marker string) bool { return len(marker) > 1 }

// parseMarkdownList preserves item boundaries and block content before terminal layout. Nested lists retain depth rather than source indentation width.
func parseMarkdownList(lines []string, start int, outer []int) (*markdownList, int) {
	indent, marker, _, ok := parseMarkdownListItem(lines[start])
	if !ok {
		return nil, start
	}
	base := len(indent)
	list := &markdownList{ordered: markdownOrdered(marker)}
	if list.ordered {
		list.start, _ = strconv.Atoi(marker[:len(marker)-1])
	}
	delimiter := marker[len(marker)-1]
	i := start
	for i < len(lines) {
		sourceIndent, rawMarker, body, matched := parseMarkdownListItem(lines[i])
		// Tokenizer.list stops at a line the hr rule matches ('- - -' is a rule, not a bullet) before it reads the next item.
		if !matched || len(sourceIndent) != base && !markdownSiblingIndent(len(sourceIndent), outer) || markdownOrdered(rawMarker) != list.ordered || rawMarker[len(rawMarker)-1] != delimiter || isMarkedHr(lines[i]) {
			break
		}
		// Each item of a top-level list has its own marker indent (Tokenizer.list reads `^( {0,3}bull)` per item), so its content starts after that indent.
		markerEnd := len(sourceIndent) + len(rawMarker)
		spacing := markdownIndent(lines[i][markerEnd:])
		if spacing == 0 {
			spacing = 1
		}
		// Tokenizer.ts:308-311: content more than four spaces past the marker is indented code, so the item's content starts one space after the marker.
		if spacing > 4 && body != "" {
			spacing = 1
			body = lines[i][markerEnd+1:]
		}
		contentIndent := markerEnd + spacing
		entry := markdownListEntry{marker: rawMarker, spacedEmpty: body == "" && lines[i][markerEnd:] != ""}
		// Tokenizer.list leaves the item's text empty when the first line is blank by JavaScript's trim, so an item holding only a Unicode space has no text.
		// Its content then starts one column after the marker (`g = t[1].length + 1`) however many spaces follow the marker.
		if widthx.JSTrim(body) == "" {
			body = ""
			contentIndent = markerEnd + 1
		}
		pending := []string{body}
		afterBlank := false
		var merged []int
		// mergedRun is set while the item's last line belongs to an indented run that marked's code tokenizer merged into the paragraph before it.
		mergedRun := false
		// endRun closes that run when more of the item follows: the merged code token keeps its newline, so the text that follows starts a line after an empty one.
		// A nested list is its own block, so the newline stays at the end of the text and renderList adds the empty row before the list.
		endRun := func(textFollows bool) {
			if mergedRun && !afterBlank {
				if textFollows {
					merged = append(merged, len(pending))
				}
				pending = append(pending, "")
			}
			mergedRun = false
		}
		fence, fenceLength, _, inFence := parseMarkdownFenceOpen(body)
		flush := func() {
			if len(pending) > 0 {
				entry.blocks = append(entry.blocks, markdownListBlock{text: strings.Join(pending, "\n"), merged: merged})
				pending, merged = nil, nil
			}
		}
		j := i + 1
		// firstBlank is Tokenizer.ts list's blankLine for the item's first line; afterBlank covers the lines after it.
		firstBlank := widthx.JSTrim(body) == ""
		// An item whose first line is itself a list item holds a nested list (marked lexes the item's text as blocks): parse it as one, in the item's own coordinates.
		// A first line the hr rule matches ('- * * *') holds a rule, not a nested list: Tokenizer.list stops at it.
		//portlint:allow utf16units nestedIndent is parseMarkdownListItem's leading ASCII spaces, so its byte length is its .length
		if nestedIndent, _, _, nested := parseMarkdownListItem(body); nested && len(nestedIndent) <= 3 && !inFence && !isMarkedHr(body) {
			shifted := append([]string{strings.Repeat(" ", contentIndent) + body}, lines[i+1:]...)
			child, next := parseMarkdownList(shifted, 0, append(slices.Clone(outer), contentIndent))
			entry.blocks = append(entry.blocks, markdownListBlock{list: child})
			pending = nil
			j = i + next
		}
		// previous is the item's last line after its content indent (Tokenizer.ts list: h), which decides whether a lazy line may continue it.
		// For the first line that is the text after the marker as written, spacing included: four spaces of it already count as indented code.
		previous := lines[i][markerEnd:]
		for j < len(lines) {
			line := lines[j]
			// A line under the content indent inside a fence is judged by the lazy rules below and, when it continues the item, stays in the fence.
			// Blank lines stay in the fence only while a line at the content indent follows them: a lazy line after a blank line ends the item (Tokenizer.ts list: `if (blankLine) break`).
			if inFence && (widthx.JSTrim(line) == "" && markdownBlankRunContinues(lines, j, contentIndent) || widthx.JSTrim(line) != "" && markdownIndent(line) >= contentIndent) {
				if widthx.JSTrim(line) == "" {
					pending = append(pending, "")
					j++
					continue
				}
				value := line[contentIndent:]
				pending = append(pending, value)
				previous = value
				j++
				if isMarkdownFenceClose(value, fence, fenceLength) {
					inFence = false
				}
				continue
			}
			// Tokenizer.list continues the item over a line that is only Unicode spaces (`!d.trim()` is JavaScript's trim, not the blank line rule) and keeps what is left of it after the content indent.
			// A lazy line cannot follow it.
			if !markedBlankLine(line) && widthx.JSTrim(line) == "" {
				// Two newlines in a row make a space token whose raw holds a line (anyLine), which makes the list loose.
				if len(pending) > 0 && pending[len(pending)-1] == "" || j+1 < len(lines) && widthx.JSTrim(lines[j+1]) != "" && markdownIndent(lines[j+1]) >= contentIndent {
					list.loose = true
				}
				pending = append(pending, jsstring.Slice(strings.ReplaceAll(line, "\t", "    "), contentIndent))
				afterBlank = true
				j++
				continue
			}
			if markedBlankLine(line) {
				k := j
				for k < len(lines) && widthx.JSTrim(lines[k]) == "" {
					k++
				}
				if k == len(lines) {
					j = k
					break
				}
				ni, nm, _, nested := parseMarkdownListItem(lines[k])
				if nested && len(ni) < contentIndent && markdownSiblingIndent(len(ni), outer) && markdownOrdered(nm) == list.ordered && nm[len(nm)-1] == delimiter && !isMarkedHr(lines[k]) {
					// Tokenizer.ts:372-374: the blank lines stay in the item's text; only the last item's text is trimmed, so an indented code block keeps one trailing blank line.
					for range k - j {
						if len(pending) > 0 {
							pending = append(pending, "")
						}
					}
					list.loose = true
					j = k
					break
				}
				if markdownIndent(lines[k]) >= contentIndent {
					pending = append(pending, "")
					list.loose = true
					afterBlank = true
					j++
					continue
				}
				break
			}
			ni, _, _, nested := parseMarkdownListItem(line)
			// A bullet four or more spaces past the content indent is indented code, not a nested list. A line the hr rule matches ('- - -') starts no list (Tokenizer.list stops at it), so it stays item content and lexes as a rule.
			if nested && len(ni) >= contentIndent && len(ni)-contentIndent <= 3 && !isMarkedHr(line[contentIndent:]) {
				endRun(false)
				flush()
				child, next := parseMarkdownList(lines, j, append(slices.Clone(outer), contentIndent))
				entry.blocks = append(entry.blocks, markdownListBlock{list: child})
				// The nested list's last line is the item's last line, which decides whether a lazy line may continue the item.
				previous = jsstring.Slice(lines[next-1], contentIndent)
				j = next
				afterBlank = false
				continue
			}
			if nested && len(ni) <= base {
				break
			}
			leading := markdownIndent(line)
			// This list is lexed from the text of the item that holds it, where a line the enclosing items took lazily is the line as written (Tokenizer.list: `itemContents += '\n' + nextLine`): its indent counts against this item's content indent relative to the holding item, not the absolute one.
			cut := contentIndent
			if innermost := markdownInnermostOuter(outer); leading < innermost && !nested && markdownOuterMayContinue(line, lines[j-1], outer) {
				stripped := markdownStripOuterIndent(line, outer)
				// A line that ends an enclosing item (a fence, heading, quote, rule or bullet within its indent) ends this one too.
				ends := markedListInterrupts(stripped, contentIndent-innermost)
				for _, o := range outer {
					ends = ends || markedListInterrupts(line, o)
				}
				if !ends && markdownIndent(stripped) >= contentIndent-innermost {
					line, cut = stripped, contentIndent-innermost
					leading = contentIndent
				}
			}
			if leading >= contentIndent {
				value := line[cut:]
				raw := value
				// An indented line that continues the item's paragraph is merged by marked's code tokenizer with up to four spaces removed (Tokenizer.code, codeRemoveIndent).
				// The code token takes the following indented lines too, blank lines between them included.
				isMerged := markdownIndent(value) >= 4 && (mergedRun || !afterBlank && widthx.JSTrim(previous) != "" && markdownIndent(previous) < 4 && markdownIsTextLine(previous, beforeLast(pending)))
				if isMerged {
					value = value[min(markdownIndent(value), 4):]
					for q := len(pending) - 1; afterBlank && q >= 0 && pending[q] == ""; q-- {
						merged = append(merged, q)
					}
					merged = append(merged, len(pending))
				}
				if !isMerged {
					endRun(true)
				}
				mergedRun = isMerged
				pending = append(pending, value)
				previous = raw
				inFence = false
				if !isMerged {
					fence, fenceLength, _, inFence = parseMarkdownFenceOpen(value)
				}
				j++
				afterBlank = false
				continue
			}
			if (!firstBlank || j != i+1) && !afterBlank && !nested && widthx.JSTrim(line) != "" && !markedListInterrupts(line, contentIndent) && markdownLazyLineMayContinue(previous, contentIndent) && markdownOuterMayContinue(line, lines[j-1], outer) {
				// Tokenizer.ts list: a lazy line joins the item's text as written, indentation included, after the item that holds this list took the indent of its own content off every line that reaches it.
				endRun(true)
				stripped := markdownStripOuterIndent(line, outer)
				pending = append(pending, stripped)
				// marked slices the line in UTF-16 units, not bytes, at the item's content indent in the text of the item that holds this list: that text is the line as the enclosing item left it, so the indent is relative to the enclosing item's content.
				previous = jsstring.Slice(stripped, contentIndent-markdownInnermostOuter(outer))
				j++
				continue
			}
			break
		}
		flush()
		list.items = append(list.items, entry)
		i = j
	}
	// Tokenizer.list trims the end of the last item's text.
	if n := len(list.items); n > 0 {
		if blocks := list.items[n-1].blocks; len(blocks) > 0 && blocks[len(blocks)-1].list == nil {
			blocks[len(blocks)-1].text = widthx.JSTrimEnd(blocks[len(blocks)-1].text)
		}
	}
	return list, i
}

// beforeLast is the line before the last of lines, or "" when there is none.
func beforeLast(lines []string) string {
	if len(lines) < 2 {
		return ""
	}
	return lines[len(lines)-2]
}

// markdownIsTextLine reports that the line is text a following indented line can join: it starts no block of its own, so it is neither a heading, rule, fence, quote, list item, HTML block or table row nor a setext underline. A setext underline needs a text line before it (before). A definition after text joins that text.
func markdownIsTextLine(line, before string) bool {
	source := &markdownBlockSource{lines: []string{line}}
	if source.paragraphInterrupted(0) || widthx.JSTrim(before) != "" && setextUnderlinePattern.MatchString(line) {
		return false
	}
	if _, _, ok := source.htmlBlock(0); ok {
		return false
	}
	return true
}

// markdownStripOuterIndent removes from a lazy line the content indent of the innermost enclosing item that the line reaches: that item's tokenizer cut those spaces (`nextLine.slice(indent)`) before it lexed its content, which holds the nested list. outer lists the enclosing items' content indents, outermost first.
func markdownStripOuterIndent(line string, outer []int) string {
	indent := markdownIndent(line)
	for _, o := range slices.Backward(outer) {
		if o <= indent {
			return line[o:]
		}
	}
	return line
}

// markdownInnermostOuter is the content indent of the item that holds the list being parsed, which is 0 for a top-level list.
func markdownInnermostOuter(outer []int) int {
	if len(outer) == 0 {
		return 0
	}
	return outer[len(outer)-1]
}

// markdownSiblingIndent reports that a marker indented by indent can be an item of a list whose enclosing item's content starts at outer's last indent (none for a top-level list): the item is lexed from text that item cut, where Tokenizer.list takes up to three spaces before any item's marker.
func markdownSiblingIndent(indent int, outer []int) bool {
	owner := 0
	if len(outer) > 0 {
		owner = outer[len(outer)-1]
	}
	return indent-owner >= 0 && indent-owner <= 3
}

// markdownOuterMayContinue applies markdownLazyLineMayContinue at every enclosing item that the line is lazy for, meaning it is indented less than that item's content: marked's tokenizer decides a lazy line from the outermost item down, with that item's last line cut by its own content indent, so a line that an outer item refuses never reaches a nested list. A line indented to an item's content is no lazy line of it.
func markdownOuterMayContinue(line, last string, outer []int) bool {
	indent := markdownIndent(line)
	for k, o := range outer {
		if indent < o && !markdownLazyLineMayContinue(markdownItemText(last, outer[:k], outer[k]), outer[k]-markdownInnermostOuter(outer[:k])) {
			return false
		}
	}
	return true
}

// markdownItemText is the previous line as the item whose content starts at indent, inside the enclosing items' contents, cuts it for its own break test (Tokenizer.list: `prevLine = nextLine.slice(indent)`): the enclosing items cut the indent they reach from a line, and a line one of them took lazily stays as written, so the cut is relative to the item that holds it.
func markdownItemText(line string, outer []int, indent int) string {
	base := 0
	for _, o := range outer {
		if markdownIndent(line) >= o-base {
			line = line[o-base:]
		}
		base = o
	}
	return jsstring.Slice(line, indent-base)
}

// markdownLazyLineMayContinue is the break test of marked's list tokenizer for a line indented less than the item's content: it does not continue an item whose previous line was empty, indented code, or a fence, heading start or rule that begins within the item's indent (up to three spaces, fewer when the content indent is smaller).
func markdownLazyLineMayContinue(previous string, contentIndent int) bool {
	if markdownIndent(previous) >= 4 {
		return false
	}
	if markdownIndent(previous) > max(0, min(3, contentIndent-1)) {
		return true
	}
	if _, _, _, fenced := parseMarkdownFenceOpen(previous); fenced || strings.HasPrefix(strings.TrimLeft(previous, " "), "#") {
		return false
	}
	return !isMarkedHr(previous)
}

func (m *Markdown) renderList(list *markdownList, depth, width int) []string {
	theme := m.markdownTheme()
	indent := strings.Repeat("    ", depth)
	var out []string
	taskHeading, taskStripped := markdownTaskMarkerStripped(list)
	for index, item := range list.items {
		bullet := "- "
		if list.ordered {
			bullet = strconv.Itoa(list.start+index) + ". "
		}
		if m.options.PreserveOrderedListMarkers {
			bullet = item.marker + " "
		}
		blocks := append([]markdownListBlock(nil), item.blocks...)
		task := ""
		// marked turns the task marker into a checkbox followed by a text token, so the item's first line is text whatever it looks like.
		// A setext heading over that line is no text token: the item gets no checkbox, and its heading text keeps the marker unless another item's fix-up stripped it (markdownTaskMarkerStripped).
		firstLineText := false
		if len(blocks) > 0 && blocks[0].list == nil {
			original := blocks[0].text
			marker, rest := splitListTaskMarker(original)
			switch {
			case marker == "":
			case taskHeading[index]:
				if taskStripped[index] {
					blocks[0].text = rest
				}
			default:
				task = marker
				if taskStripped[index] {
					blocks[0].text = rest
				}
				firstLineText = true
			}
		}
		marker := bullet + task
		first := indent + theme.ListBullet(marker)
		continuation := indent + strings.Repeat(" ", widthx.VisibleWidth(marker))
		itemWidth := max(1, width-widthx.VisibleWidth(first))
		rendered := false
		emptyTail := false
		for blockIndex, block := range blocks {
			notLast := m.keepPartialFence || index < len(list.items)-1 || blockIndex < len(blocks)-1
			// A nested list whose last item is empty ends with a newline in its raw that the item's text then lexes as a space token.
			if emptyTail && block.list == nil && !strings.HasPrefix(block.text, "\n") && block.text != "" {
				out = append(out, "")
			}
			emptyTail = block.list != nil && markdownListEndsEmpty(block.list)
			if block.list != nil {
				// An item whose first line is empty and whose next line starts a nested list has the text "\n- b": the leading newline is a space token, which renders the bullet with an empty row.
				if !rendered && blockIndex == 1 && blocks[0].list == nil && blocks[0].text == "" {
					out = append(out, first)
					rendered = true
				}
				// The blank line before a nested list is lexed with the text's newline as a space token of two characters, which renders an empty row.
				if blockIndex > 0 && blocks[blockIndex-1].list == nil && strings.HasSuffix(blocks[blockIndex-1].text, "\n") && !strings.HasSuffix(blocks[blockIndex-1].text, "\n\n") && rendered {
					out = append(out, "")
				}
				keep := m.keepPartialFence
				m.keepPartialFence = notLast
				out = append(out, m.renderList(block.list, depth+1, width)...)
				m.keepPartialFence = keep
				rendered = true
				continue
			}
			text := block.text
			sentinel := firstLineText && blockIndex == 0
			if sentinel {
				// A control character in front keeps every block rule from matching the first line; only that one is removed from the rendered rows, because a NUL in the source is rendered as written.
				text = "\x00" + text
			}
			child := m.childMarkdown(text)
			child.suppressBlockSpacing = true
			child.lazyIndented, child.lazyUnderlines, child.lazyLines = block.merged, block.merged, block.merged
			child.keepPartialFence = notLast
			for _, line := range child.renderContent(text, itemWidth) {
				if sentinel && strings.Contains(line, "\x00") {
					line = strings.Replace(line, "\x00", "", 1)
					sentinel = false
				}
				for _, wrapped := range widthx.WrapTextWithAnsi(line, itemWidth) {
					prefix := continuation
					if !rendered {
						prefix = first
					}
					out = append(out, prefix+wrapped)
					rendered = true
				}
			}
		}
		if !rendered {
			out = append(out, first)
		}
		if list.loose && index < len(list.items)-1 {
			out = append(out, "")
		}
	}
	return out
}

// markdownTaskMarkerStripped is the task fix-up of marked's list tokenizer (Tokenizer.list, the loop after the items are lexed).
// Every task item whose first token is text or a paragraph removes the checkbox from the last queued inline source that still starts with one, which is not always its own: a task item whose first token is a setext heading (taskHeading) queues such a source but never removes one itself, so an earlier item's fix-up takes it, and the earlier item's own text keeps its marker.
// The M items that remove a marker take the last M queued sources in order.
func markdownTaskMarkerStripped(list *markdownList) (taskHeading, stripped []bool) {
	taskHeading, stripped = make([]bool, len(list.items)), make([]bool, len(list.items))
	var queued []int
	removers := 0
	for index, item := range list.items {
		if len(item.blocks) == 0 || item.blocks[0].list != nil {
			continue
		}
		original := item.blocks[0].text
		if marker, _ := splitListTaskMarker(original); marker == "" {
			continue
		}
		queued = append(queued, index)
		if _, _, heading := setextHeadingEnd(strings.Split(original, "\n"), 0, nil); heading {
			taskHeading[index] = true
			continue
		}
		removers++
	}
	for _, index := range queued[len(queued)-removers:] {
		stripped[index] = true
	}
	return taskHeading, stripped
}

// markdownListEndsEmpty reports that the list's last item has no content but spaces after its marker, which is what leaves a newline for a following space token.
func markdownListEndsEmpty(list *markdownList) bool {
	if len(list.items) == 0 {
		return false
	}
	last := list.items[len(list.items)-1]
	if !last.spacedEmpty {
		return false
	}
	for _, block := range last.blocks {
		if block.list != nil || block.text != "" {
			return false
		}
	}
	return true
}

// markedListInterruptions are the six rules Tokenizer.list tests every following line against (rules.other.nextBulletRegex, hrRegex, fencesBeginRegex, headingBeginRegex, htmlBeginRegex, blockquoteBeginRegex), indexed by the indent they allow, min(3, contentIndent-1) clamped at 0. `.` is any character but a line terminator.
var markedListInterruptions = [4]*lazyregexp.Regexp{}

func init() {
	for n := range markedListInterruptions {
		indent := fmt.Sprintf("^ {0,%d}", n)
		markedListInterruptions[n] = lazyregexp.New(indent + `(?:(?:[*+-]|\d{1,9}[.)])(?:[ \t]|$)|(?:(?:- *){3,}|(?:_ *){3,}|(?:\* *){3,})$|(?:` + "```" + `|~~~)|#|<(?:[a-zA-Z][^\r\n\x{2028}\x{2029}]*>|!--)|>)`)
	}
}

// markedListInterrupts is whether line ends a list item before it, so that it is no lazy continuation of the item's paragraph.
func markedListInterrupts(line string, contentIndent int) bool {
	return markedListInterruptions[max(0, min(3, contentIndent-1))].MatchString(line)
}

// markdownListResidue finds the text Tokenizer.list leaves for the lexer when the list raw (lines start..end-1) ends in whitespace that is not only spaces, tabs and newlines. resume is the line holding the end of
// the last item's text and suffix the part of that line the trim removed.
func markdownListResidue(lines []string, start, end int) (resume int, suffix string, ok bool) {
	resume = end - 1
	for resume > start && widthx.JSTrim(lines[resume]) == "" {
		resume--
	}
	//portlint:allow utf16units the suffix trimEnd removed, cut at the end of the trimmed prefix of the same string; no JS offset leaves the function
	suffix = lines[resume][len(widthx.JSTrimEnd(lines[resume])):]
	ok = strings.Trim(suffix, " \t") != ""
	for k := resume + 1; k < end && !ok; k++ {
		ok = !markedBlankLine(lines[k])
	}
	return resume, suffix, ok
}
