// Ports upstream pi-tui width-bounded truncation with ellipsis + ANSI/tab
// awareness.
//
// Mirrors:
//   - truncateFragmentToWidth  utils.ts:50
//   - finalizeTruncatedResult  utils.ts:130
//   - truncateToWidth          utils.ts:812

package widthx

import (
	"strings"
)

// TruncateToWidth truncates text to fit within `maxWidth` visible columns,
// appending `ellipsis` (default "...") when truncation occurs. If `pad` is
// true, the result is padded with trailing spaces to exactly `maxWidth`
// columns. Mirrors upstream `truncateToWidth`.
//
// ANSI escape codes do not count toward width. Tabs expand to 3 columns to
// match upstream.
func TruncateToWidth(text string, maxWidth int, ellipsis string, pad bool) string {
	if maxWidth <= 0 {
		return ""
	}
	if text == "" {
		if pad {
			return strings.Repeat(" ", maxWidth)
		}
		return ""
	}

	ellipsisWidth := VisibleWidth(ellipsis)
	if ellipsisWidth >= maxWidth {
		textWidth := VisibleWidth(text)
		if textWidth <= maxWidth {
			if pad {
				return text + strings.Repeat(" ", maxWidth-textWidth)
			}
			return text
		}
		clipped := truncateFragmentToWidth(ellipsis, maxWidth)
		if clipped.width == 0 {
			if pad {
				return strings.Repeat(" ", maxWidth)
			}
			return ""
		}
		return finalizeTruncatedResult("", 0, clipped.text, clipped.width, maxWidth, pad)
	}

	// Fast ASCII path.
	if isPrintableASCII(text) {
		if len(text) <= maxWidth {
			if pad {
				return text + strings.Repeat(" ", maxWidth-len(text))
			}
			return text
		}
		targetWidth := maxWidth - ellipsisWidth
		return finalizeTruncatedResult(text[:targetWidth], targetWidth, ellipsis, ellipsisWidth, maxWidth, pad)
	}

	targetWidth := maxWidth - ellipsisWidth
	var result strings.Builder
	pendingAnsi := ""
	visibleSoFar := 0
	keptWidth := 0
	keepContiguous := true
	overflowed := false
	exhausted := true

	hasAnsi := strings.ContainsRune(text, 0x1B)
	hasTabs := strings.ContainsRune(text, '\t')

	if !hasAnsi && !hasTabs {
		gs := newGraphemeIter(text)
		for gs.Next() {
			seg := gs.Str()
			w := graphemeWidthFast(seg)
			if keepContiguous && keptWidth+w <= targetWidth {
				result.WriteString(seg)
				keptWidth += w
			} else {
				keepContiguous = false
			}
			visibleSoFar += w
			if visibleSoFar > maxWidth {
				overflowed = true
				exhausted = false
				break
			}
		}
	} else {
		i := 0
		for i < len(text) {
			if n := ExtractAnsi(text, i); n > 0 {
				pendingAnsi += text[i : i+n]
				i += n
				continue
			}
			if text[i] == '\t' {
				if keepContiguous && keptWidth+3 <= targetWidth {
					if pendingAnsi != "" {
						result.WriteString(pendingAnsi)
						pendingAnsi = ""
					}
					result.WriteByte('\t')
					keptWidth += 3
				} else {
					keepContiguous = false
					pendingAnsi = ""
				}
				visibleSoFar += 3
				if visibleSoFar > maxWidth {
					overflowed = true
					exhausted = false
					break
				}
				i++
				continue
			}
			end := i
			for end < len(text) && text[end] != '\t' {
				if ExtractAnsi(text, end) > 0 {
					break
				}
				end++
			}
			gs := newGraphemeIter(text[i:end])
			for gs.Next() {
				seg := gs.Str()
				w := graphemeWidthFast(seg)
				if keepContiguous && keptWidth+w <= targetWidth {
					if pendingAnsi != "" {
						result.WriteString(pendingAnsi)
						pendingAnsi = ""
					}
					result.WriteString(seg)
					keptWidth += w
				} else {
					keepContiguous = false
					pendingAnsi = ""
				}
				visibleSoFar += w
				if visibleSoFar > maxWidth {
					overflowed = true
					exhausted = false
					break
				}
			}
			if overflowed {
				break
			}
			i = end
		}
	}

	if !overflowed && exhausted {
		if pad {
			extra := max(maxWidth-visibleSoFar, 0)
			return text + strings.Repeat(" ", extra)
		}
		return text
	}
	return finalizeTruncatedResult(result.String(), keptWidth, ellipsis, ellipsisWidth, maxWidth, pad)
}

type fragment struct {
	text  string
	width int
}

func truncateFragmentToWidth(text string, maxWidth int) fragment {
	if maxWidth <= 0 || text == "" {
		return fragment{}
	}
	if isPrintableASCII(text) {
		if len(text) > maxWidth {
			return fragment{text: text[:maxWidth], width: maxWidth}
		}
		return fragment{text: text, width: len(text)}
	}
	hasAnsi := strings.ContainsRune(text, 0x1B)
	hasTabs := strings.ContainsRune(text, '\t')
	if !hasAnsi && !hasTabs {
		var b strings.Builder
		width := 0
		gs := newGraphemeIter(text)
		for gs.Next() {
			seg := gs.Str()
			w := graphemeWidthFast(seg)
			if width+w > maxWidth {
				break
			}
			b.WriteString(seg)
			width += w
		}
		return fragment{text: b.String(), width: width}
	}
	var b strings.Builder
	width := 0
	pendingAnsi := ""
	for i := 0; i < len(text); {
		if n := ExtractAnsi(text, i); n > 0 {
			pendingAnsi += text[i : i+n]
			i += n
			continue
		}
		if text[i] == '\t' {
			if width+3 > maxWidth {
				break
			}
			if pendingAnsi != "" {
				b.WriteString(pendingAnsi)
				pendingAnsi = ""
			}
			b.WriteByte('\t')
			width += 3
			i++
			continue
		}
		end := i
		for end < len(text) && text[end] != '\t' {
			if ExtractAnsi(text, end) > 0 {
				break
			}
			end++
		}
		gs := newGraphemeIter(text[i:end])
		broken := false
		for gs.Next() {
			seg := gs.Str()
			w := graphemeWidthFast(seg)
			if width+w > maxWidth {
				broken = true
				break
			}
			if pendingAnsi != "" {
				b.WriteString(pendingAnsi)
				pendingAnsi = ""
			}
			b.WriteString(seg)
			width += w
		}
		if broken {
			break
		}
		i = end
	}
	return fragment{text: b.String(), width: width}
}

func finalizeTruncatedResult(prefix string, prefixWidth int, ellipsis string, ellipsisWidth, maxWidth int, pad bool) string {
	const reset = "\x1b[0m"
	hyperlinkClose := activeOsc8Close(prefix)
	visibleWidth := prefixWidth + ellipsisWidth
	var result string
	if ellipsis != "" {
		result = prefix + hyperlinkClose + reset + ellipsis + reset
	} else {
		result = prefix + hyperlinkClose + reset
	}
	if pad {
		extra := max(maxWidth-visibleWidth, 0)
		return result + strings.Repeat(" ", extra)
	}
	return result
}

// activeOsc8Close mirrors upstream getActiveOsc8Close: when prefix leaves an
// OSC 8 hyperlink open, return the close sequence with the opener's
// terminator.
func activeOsc8Close(prefix string) string {
	if !strings.Contains(prefix, "\x1b]8;") {
		return ""
	}
	close := ""
	for i := 0; i < len(prefix); {
		code, n := ExtractAnsiCode(prefix, i)
		if n == 0 {
			i++
			continue
		}
		if strings.HasPrefix(code, "\x1b]8;") {
			term := "\x1b\\"
			if strings.HasSuffix(code, "\x07") {
				term = "\x07"
			}
			body := code[4 : len(code)-len(term)]
			if _, after, ok := strings.Cut(body, ";"); ok {
				if after == "" {
					close = ""
				} else {
					close = "\x1b]8;;" + term
				}
			}
		}
		i += n
	}
	return close
}
