// Ports upstream pi-tui line wrapping with ANSI state preservation.
//
// Mirrors:
//   - splitIntoTokensWithAnsi   utils.ts:545
//   - wrapSingleLine            utils.ts:620
//   - breakLongWord             utils.ts:713
//   - wrapTextWithAnsi          utils.ts:596

package widthx

import (
	"strings"
)

// WrapTextWithAnsi wraps text to `width` visible columns per line, preserving
// active ANSI SGR and OSC 8 hyperlink state across line breaks. Returns at
// least one line.
//
// Mirrors upstream `wrapTextWithAnsi`. ONLY does word wrapping: no padding,
// no background colors.
func WrapTextWithAnsi(text string, width int) []string {
	if text == "" {
		return []string{""}
	}
	// Split on CRLF, lone CR, and LF alike so text pasted or produced with
	// non-LF line endings still wraps per logical line. Mirrors upstream
	// wrapTextWithAnsi splitting on /\r\n|\r|\n/ (#6764).
	src := text
	if strings.ContainsRune(src, '\r') {
		src = strings.ReplaceAll(src, "\r\n", "\n")
		src = strings.ReplaceAll(src, "\r", "\n")
	}
	inputLines := strings.Split(src, "\n")
	var result []string
	tr := &AnsiCodeTracker{}
	for _, line := range inputLines {
		prefix := ""
		if len(result) > 0 {
			prefix = tr.ActiveCodes()
		}
		result = append(result, wrapSingleLine(prefix+line, width)...)
		tr.UpdateFromText(line)
	}
	if len(result) == 0 {
		return []string{""}
	}
	return result
}

func wrapSingleLine(line string, width int) []string {
	if line == "" {
		return []string{""}
	}
	if VisibleWidth(line) <= width {
		return []string{line}
	}

	var wrapped []string
	tr := &AnsiCodeTracker{}
	tokens := splitIntoTokensWithAnsi(line)

	currentLine := ""
	currentVisible := 0

	for _, token := range tokens {
		tokenWidth := VisibleWidth(token)
		isWhitespace := JSTrim(token) == ""

		// Token alone is longer than width: break by graphemes.
		if tokenWidth > width && !isWhitespace {
			if currentLine != "" {
				if r := tr.LineEndReset(); r != "" {
					currentLine += r
				}
				wrapped = append(wrapped, currentLine)
			}
			broken := breakLongWord(token, width, tr)
			if len(broken) > 1 {
				wrapped = append(wrapped, broken[:len(broken)-1]...)
			}
			currentLine = broken[len(broken)-1]
			currentVisible = VisibleWidth(currentLine)
			continue
		}

		if currentVisible+tokenWidth > width && currentVisible > 0 {
			// Wrap. Trim trailing whitespace before wrapping.
			toWrap := JSTrimEnd(currentLine)
			if r := tr.LineEndReset(); r != "" {
				toWrap += r
			}
			wrapped = append(wrapped, toWrap)
			if isWhitespace {
				// Don't start a new line with whitespace.
				currentLine = tr.ActiveCodes()
				currentVisible = 0
			} else {
				currentLine = tr.ActiveCodes() + token
				currentVisible = tokenWidth
			}
		} else {
			currentLine += token
			currentVisible += tokenWidth
		}

		tr.UpdateFromText(token)
	}

	if currentLine != "" {
		wrapped = append(wrapped, currentLine)
	}
	if len(wrapped) == 0 {
		return []string{""}
	}
	// Trim trailing whitespace per upstream.
	for i := range wrapped {
		wrapped[i] = JSTrimEnd(wrapped[i])
	}
	return wrapped
}

// splitIntoTokensWithAnsi splits text into whitespace/non-whitespace tokens
// while keeping ANSI codes attached to the next visible character.
//
// Operates byte-by-byte for ASCII space detection (the only character we
// split on), but accumulates whole bytes via byte slicing: never via
// `string(byte)` which would mangle multi-byte UTF-8 by reinterpreting
// each byte as a code point.
func splitIntoTokensWithAnsi(text string) []string {
	var tokens []string
	var current strings.Builder
	pendingAnsi := ""
	currentKind := byte(0)
	flushCurrent := func() {
		if current.Len() == 0 {
			return
		}
		tokens = append(tokens, current.String())
		current.Reset()
		currentKind = 0
	}

	for i := 0; i < len(text); {
		if n := ExtractAnsi(text, i); n > 0 {
			pendingAnsi += text[i : i+n]
			i += n
			continue
		}

		end := i
		for end < len(text) && ExtractAnsi(text, end) == 0 {
			end++
		}
		graphemes := newGraphemeIter(text[i:end])
		for graphemes.Next() {
			segment := graphemes.Str()
			isSpace := segment == " "
			if !isSpace && isCJKBreak(segment) {
				flushCurrent()
				tokens = append(tokens, pendingAnsi+segment)
				pendingAnsi = ""
				continue
			}
			kind := byte(2)
			if isSpace {
				kind = 1
			}
			if current.Len() > 0 && currentKind != kind {
				flushCurrent()
			}
			if pendingAnsi != "" {
				current.WriteString(pendingAnsi)
				pendingAnsi = ""
			}
			currentKind = kind
			current.WriteString(segment)
		}
		i = end
	}
	if pendingAnsi != "" {
		switch {
		case current.Len() > 0:
			current.WriteString(pendingAnsi)
		case len(tokens) > 0:
			tokens[len(tokens)-1] += pendingAnsi
		default:
			current.WriteString(pendingAnsi)
		}
	}
	flushCurrent()
	return tokens
}

func isCJKBreak(segment string) bool { return IsCJKBreak(segment) }

// breakLongWord breaks a single token that exceeds width into grapheme-level
// chunks, preserving ANSI state across breaks via the provided tracker.
func breakLongWord(word string, width int, tr *AnsiCodeTracker) []string {
	var lines []string
	currentLine := tr.ActiveCodes()
	currentWidth := 0

	type seg struct {
		ansi bool
		val  string
	}
	var segs []seg
	for i := 0; i < len(word); {
		if n := ExtractAnsi(word, i); n > 0 {
			segs = append(segs, seg{ansi: true, val: word[i : i+n]})
			i += n
			continue
		}
		// Non-ANSI run until next ANSI or end.
		end := i
		for end < len(word) {
			if ExtractAnsi(word, end) > 0 {
				break
			}
			end++
		}
		// Grapheme-segment the run.
		run := word[i:end]
		gs := newGraphemeIter(run)
		for gs.Next() {
			segs = append(segs, seg{ansi: false, val: gs.Str()})
		}
		i = end
	}

	for _, s := range segs {
		if s.ansi {
			currentLine += s.val
			tr.Process(s.val)
			continue
		}
		if s.val == "" {
			continue
		}
		gw := VisibleWidth(s.val)
		if currentWidth+gw > width {
			if r := tr.LineEndReset(); r != "" {
				currentLine += r
			}
			lines = append(lines, currentLine)
			currentLine = tr.ActiveCodes()
			currentWidth = 0
		}
		currentLine += s.val
		currentWidth += gw
	}
	if currentLine != "" {
		lines = append(lines, currentLine)
	}
	if len(lines) == 0 {
		return []string{""}
	}
	return lines
}
