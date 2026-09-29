package tui

// Ports packages/tui/src/components/markdown.ts (nested list block rendering).

import (
	"strconv"
	"strings"

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
}
type markdownListBlock struct {
	text string
	list *markdownList
}

func isMarkdownParagraphContinuation(lines []string, index int) bool {
	line := lines[index]
	if _, _, _, ok := parseMarkdownFenceOpen(line); ok {
		return false
	}
	if blockLatexStart(line) {
		if _, ok := tokenizeBlockLatex(strings.Join(lines[index:], "\n")); ok {
			return false
		}
	}
	return isLazyBlockquoteContinuation(line)
}

func markdownIndent(line string) int     { return len(line) - len(strings.TrimLeft(line, " ")) }
func markdownOrdered(marker string) bool { return len(marker) > 1 }

// parseMarkdownList preserves item boundaries and block content before terminal layout. Nested lists retain depth rather than source indentation width.
func parseMarkdownList(lines []string, start int) (*markdownList, int) {
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
		if !matched || len(sourceIndent) != base || markdownOrdered(rawMarker) != list.ordered || rawMarker[len(rawMarker)-1] != delimiter {
			break
		}
		markerEnd := base + len(rawMarker)
		spacing := markdownIndent(lines[i][markerEnd:])
		if spacing == 0 {
			spacing = 1
		}
		contentIndent := markerEnd + spacing
		entry := markdownListEntry{marker: rawMarker}
		pending := []string{body}
		fence, fenceLength, _, inFence := parseMarkdownFenceOpen(body)
		flush := func() {
			if len(pending) > 0 {
				entry.blocks = append(entry.blocks, markdownListBlock{text: strings.Join(pending, "\n")})
				pending = nil
			}
		}
		j := i + 1
		afterBlank := false
		for j < len(lines) {
			line := lines[j]
			if inFence {
				if widthx.JSTrim(line) == "" {
					pending = append(pending, "")
					j++
					continue
				}
				if markdownIndent(line) < contentIndent {
					break
				}
				value := line[contentIndent:]
				pending = append(pending, value)
				j++
				if isMarkdownFenceClose(value, fence, fenceLength) {
					inFence = false
				}
				continue
			}
			if widthx.JSTrim(line) == "" {
				k := j
				for k < len(lines) && widthx.JSTrim(lines[k]) == "" {
					k++
				}
				if k == len(lines) {
					j = k
					break
				}
				ni, nm, _, nested := parseMarkdownListItem(lines[k])
				if nested && len(ni) == base && markdownOrdered(nm) == list.ordered && nm[len(nm)-1] == delimiter {
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
			if nested && len(ni) >= contentIndent {
				flush()
				child, next := parseMarkdownList(lines, j)
				entry.blocks = append(entry.blocks, markdownListBlock{list: child})
				j = next
				afterBlank = false
				continue
			}
			if nested && len(ni) <= base {
				break
			}
			leading := markdownIndent(line)
			if leading >= contentIndent {
				value := line[contentIndent:]
				pending = append(pending, value)
				fence, fenceLength, _, inFence = parseMarkdownFenceOpen(value)
				j++
				afterBlank = false
				continue
			}
			if !afterBlank && !nested && isLazyBlockquoteContinuation(line) {
				pending = append(pending, strings.TrimLeft(line, " "))
				j++
				continue
			}
			break
		}
		flush()
		list.items = append(list.items, entry)
		i = j
	}
	return list, i
}

func (m *Markdown) renderList(list *markdownList, depth, width int) []string {
	theme := m.markdownTheme()
	indent := strings.Repeat("    ", depth)
	var out []string
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
		if len(blocks) > 0 && blocks[0].list == nil {
			task, blocks[0].text = splitListTaskMarker(blocks[0].text)
		}
		marker := bullet + task
		first := indent + theme.ListBullet(marker)
		continuation := indent + strings.Repeat(" ", widthx.VisibleWidth(marker))
		itemWidth := max(1, width-widthx.VisibleWidth(first))
		rendered := false
		for _, block := range blocks {
			if block.list != nil {
				out = append(out, m.renderList(block.list, depth+1, width)...)
				rendered = true
				continue
			}
			child := m.childMarkdown(block.text)
			child.suppressBlockSpacing = true
			for _, line := range child.renderContent(block.text, itemWidth) {
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
