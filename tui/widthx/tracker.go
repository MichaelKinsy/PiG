// Ports upstream `AnsiCodeTracker` (utils.ts:302).
//
// Tracks active SGR attributes and OSC 8 hyperlink state so styling can be
// preserved across line breaks (wrapTextWithAnsi) and across overlay splices
// (extractSegments). Pure state machine: no I/O.

package widthx

import (
	"strconv"
	"strings"
)

// AnsiCodeTracker tracks SGR state and active OSC 8 hyperlink across a
// stream of ANSI codes. Mirrors upstream `AnsiCodeTracker`.
type AnsiCodeTracker struct {
	bold, dim, italic, underline   bool
	blink, inverse, hidden, strike bool
	fgColor, bgColor               string // "" = default
	// activeHyperlink stores the full re-open sequence (params + url +
	// original terminator) so line-wrap re-opens preserve the original
	// BEL vs ST terminator: only BEL-terminated links are clickable in
	// some terminals (e.g. Apple Terminal). "" = no active hyperlink.
	activeHyperlinkOpen  string // e.g. "\x1b]8;;https://e.com\x07"
	activeHyperlinkClose string // e.g. "\x1b]8;;\x07"
}

// Process applies a single ANSI escape code to the tracker. The code must
// be a complete escape sequence as returned by ExtractAnsi.
func (t *AnsiCodeTracker) Process(code string) {
	if strings.HasPrefix(code, "\x1b]8;") {
		// OSC 8 hyperlink: \x1b]8;<params>;<url>...
		// Preserve the original terminator (BEL \x07 or ST \x1b\\) because
		// some terminals (e.g. Apple Terminal) only make BEL-terminated links
		// clickable. Re-opening wrapped lines must use the same terminator.
		body := code[len("\x1b]8;"):]
		var terminator, closeTerminator string
		switch {
		case strings.HasSuffix(code, "\x07"):
			terminator = "\x07"
			closeTerminator = "\x07"
			body = strings.TrimSuffix(body, "\x07")
		default:
			terminator = "\x1b\\"
			closeTerminator = "\x1b\\"
			body = strings.TrimSuffix(body, "\x1b\\")
		}
		if params, after, ok := strings.Cut(body, ";"); ok {
			url := after
			if url == "" {
				t.activeHyperlinkOpen = ""
				t.activeHyperlinkClose = ""
			} else {
				t.activeHyperlinkOpen = "\x1b]8;" + params + ";" + url + terminator
				t.activeHyperlinkClose = "\x1b]8;;" + closeTerminator
			}
		}
		return
	}

	if !strings.HasSuffix(code, "m") {
		return
	}

	// Extract params like upstream's unanchored /\x1b\[([\d;]*)m/: the first
	// ESC [ digits-or-semicolons m anywhere in the code. A CSI extracted by
	// ExtractAnsi can swallow other bytes before its terminating m.
	params, ok := firstSGRParams(code)
	if !ok {
		return
	}
	if params == "" || params == "0" {
		t.resetSGR()
		return
	}

	parts := strings.Split(params, ";")
	for i := 0; i < len(parts); {
		c, err := strconv.Atoi(parts[i])
		if err != nil {
			i++
			continue
		}

		// 256-color / RGB consume multiple params.
		if (c == 38 || c == 48) && i+1 < len(parts) {
			if parts[i+1] == "5" && i+2 < len(parts) {
				colorCode := parts[i] + ";" + parts[i+1] + ";" + parts[i+2]
				if c == 38 {
					t.fgColor = colorCode
				} else {
					t.bgColor = colorCode
				}
				i += 3
				continue
			}
			if parts[i+1] == "2" && i+4 < len(parts) {
				colorCode := parts[i] + ";" + parts[i+1] + ";" + parts[i+2] + ";" + parts[i+3] + ";" + parts[i+4]
				if c == 38 {
					t.fgColor = colorCode
				} else {
					t.bgColor = colorCode
				}
				i += 5
				continue
			}
		}

		switch {
		case c == 0:
			t.resetSGR()
		case c == 1:
			t.bold = true
		case c == 2:
			t.dim = true
		case c == 3:
			t.italic = true
		case c == 4:
			t.underline = true
		case c == 5:
			t.blink = true
		case c == 7:
			t.inverse = true
		case c == 8:
			t.hidden = true
		case c == 9:
			t.strike = true
		case c == 21:
			t.bold = false
		case c == 22:
			t.bold = false
			t.dim = false
		case c == 23:
			t.italic = false
		case c == 24:
			t.underline = false
		case c == 25:
			t.blink = false
		case c == 27:
			t.inverse = false
		case c == 28:
			t.hidden = false
		case c == 29:
			t.strike = false
		case c == 39:
			t.fgColor = ""
		case c == 49:
			t.bgColor = ""
		case (c >= 30 && c <= 37) || (c >= 90 && c <= 97):
			t.fgColor = strconv.Itoa(c)
		case (c >= 40 && c <= 47) || (c >= 100 && c <= 107):
			t.bgColor = strconv.Itoa(c)
		}
		i++
	}
}

func (t *AnsiCodeTracker) resetSGR() {
	t.bold, t.dim, t.italic, t.underline = false, false, false, false
	t.blink, t.inverse, t.hidden, t.strike = false, false, false, false
	t.fgColor, t.bgColor = "", ""
	// SGR reset does NOT affect OSC 8 hyperlink state.
}

// Clear resets the entire tracker including hyperlink state. Used to reuse
// a pooled instance.
func (t *AnsiCodeTracker) Clear() {
	t.resetSGR()
	t.activeHyperlinkOpen = ""
	t.activeHyperlinkClose = ""
}

// ActiveCodes returns the ANSI escape string needed to re-establish the
// currently active styling. Returns "" if no styling is active.
func (t *AnsiCodeTracker) ActiveCodes() string {
	var codes []string
	if t.bold {
		codes = append(codes, "1")
	}
	if t.dim {
		codes = append(codes, "2")
	}
	if t.italic {
		codes = append(codes, "3")
	}
	if t.underline {
		codes = append(codes, "4")
	}
	if t.blink {
		codes = append(codes, "5")
	}
	if t.inverse {
		codes = append(codes, "7")
	}
	if t.hidden {
		codes = append(codes, "8")
	}
	if t.strike {
		codes = append(codes, "9")
	}
	if t.fgColor != "" {
		codes = append(codes, t.fgColor)
	}
	if t.bgColor != "" {
		codes = append(codes, t.bgColor)
	}
	var b strings.Builder
	if len(codes) > 0 {
		b.WriteString("\x1b[")
		b.WriteString(strings.Join(codes, ";"))
		b.WriteString("m")
	}
	if t.activeHyperlinkOpen != "" {
		b.WriteString(t.activeHyperlinkOpen)
	}
	return b.String()
}

// HasActiveCodes reports whether any styling or hyperlink is currently
// active.
func (t *AnsiCodeTracker) HasActiveCodes() bool {
	return t.bold || t.dim || t.italic || t.underline ||
		t.blink || t.inverse || t.hidden || t.strike ||
		t.fgColor != "" || t.bgColor != "" || t.activeHyperlinkOpen != ""
}

// LineEndReset returns the escape string needed at end-of-line to prevent
// underline from bleeding into padding and to close any active OSC 8
// hyperlink. The hyperlink is re-opened at the start of the next line via
// ActiveCodes(). Returns "" if no reset is needed.
//
// Mirrors upstream `getLineEndReset`.
func (t *AnsiCodeTracker) LineEndReset() string {
	var b strings.Builder
	if t.underline {
		b.WriteString("\x1b[24m")
	}
	if t.activeHyperlinkOpen != "" {
		b.WriteString(t.activeHyperlinkClose)
	}
	return b.String()
}

// UpdateFromText scans text, extracts every ANSI code, and processes it
// through the tracker. Mirrors upstream `updateTrackerFromText`.
func (t *AnsiCodeTracker) UpdateFromText(text string) {
	for i := 0; i < len(text); {
		n := ExtractAnsi(text, i)
		if n == 0 {
			i++
			continue
		}
		t.Process(text[i : i+n])
		i += n
	}
}

// ActiveBackgroundCode returns only the active background SGR, or "".
// Mirrors upstream AnsiCodeTracker.getActiveBackgroundCode.
func (t *AnsiCodeTracker) ActiveBackgroundCode() string {
	if t.bgColor == "" {
		return ""
	}
	return "\x1b[" + t.bgColor + "m"
}

// GetActiveBackgroundAnsi returns only the background color active at the end
// of an ANSI-styled string. Mirrors upstream getActiveBackgroundAnsi.
func GetActiveBackgroundAnsi(text string) string {
	var tracker AnsiCodeTracker
	tracker.UpdateFromText(text)
	return tracker.ActiveBackgroundCode()
}

// firstSGRParams returns the parameters of the first match of
// /\x1b\[([\d;]*)m/ in code.
func firstSGRParams(code string) (string, bool) {
	for i := 0; i+2 < len(code); i++ {
		if code[i] != 0x1b || code[i+1] != '[' {
			continue
		}
		j := i + 2
		for j < len(code) && (code[j] == ';' || (code[j] >= '0' && code[j] <= '9')) {
			j++
		}
		if j < len(code) && code[j] == 'm' {
			return code[i+2 : j], true
		}
	}
	return "", false
}
