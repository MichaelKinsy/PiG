// SPDX-FileCopyrightText: Copyright (c) 2025 Mario Zechner
// SPDX-FileCopyrightText: Copyright (c) Sindre Sorhus <sindresorhus@gmail.com> (https://sindresorhus.com)
// SPDX-License-Identifier: MIT

// Ports packages/coding-agent/src/core/export-html/ansi-to-html.ts
package export

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/lazyregexp"
)

var ansiColors = []string{
	"#000000",
	"#800000",
	"#008000",
	"#808000",
	"#000080",
	"#800080",
	"#008080",
	"#c0c0c0",
	"#808080",
	"#ff0000",
	"#00ff00",
	"#ffff00",
	"#0000ff",
	"#ff00ff",
	"#00ffff",
	"#ffffff",
}

type textStyle struct {
	fg        string
	bg        string
	bold      bool
	dim       bool
	italic    bool
	underline bool
}

func color256ToHex(index int) string {
	if index < 0 {
		index = 0
	}
	if index < len(ansiColors) {
		return ansiColors[index]
	}
	if index < 232 {
		cubeIndex := index - 16
		r := cubeIndex / 36
		g := (cubeIndex % 36) / 6
		b := cubeIndex % 6
		toComponent := func(n int) int {
			if n == 0 {
				return 0
			}
			return 55 + n*40
		}
		return fmt.Sprintf("#%02x%02x%02x", toComponent(r), toComponent(g), toComponent(b))
	}
	gray := 8 + (index-232)*10
	return fmt.Sprintf("#%02x%02x%02x", gray, gray, gray)
}

func (s textStyle) hasStyle() bool {
	return s.fg != "" || s.bg != "" || s.bold || s.dim || s.italic || s.underline
}

func (s textStyle) inlineCSS() string {
	parts := make([]string, 0, 6)
	if s.fg != "" {
		parts = append(parts, "color:"+s.fg)
	}
	if s.bg != "" {
		parts = append(parts, "background-color:"+s.bg)
	}
	if s.bold {
		parts = append(parts, "font-weight:bold")
	}
	if s.dim {
		parts = append(parts, "opacity:0.6")
	}
	if s.italic {
		parts = append(parts, "font-style:italic")
	}
	if s.underline {
		parts = append(parts, "text-decoration:underline")
	}
	return strings.Join(parts, ";")
}

func applySGRCodes(params []int, style *textStyle) {
	for i := 0; i < len(params); i++ {
		code := params[i]
		switch {
		case code == 0:
			*style = textStyle{}
		case code == 1:
			style.bold = true
		case code == 2:
			style.dim = true
		case code == 3:
			style.italic = true
		case code == 4:
			style.underline = true
		case code == 22:
			style.bold = false
			style.dim = false
		case code == 23:
			style.italic = false
		case code == 24:
			style.underline = false
		case code >= 30 && code <= 37:
			style.fg = ansiColors[code-30]
		case code == 38:
			if i+2 < len(params) && params[i+1] == 5 {
				style.fg = color256ToHex(params[i+2])
				i += 2
			} else if i+4 < len(params) && params[i+1] == 2 {
				style.fg = fmt.Sprintf("rgb(%d,%d,%d)", params[i+2], params[i+3], params[i+4])
				i += 4
			}
		case code == 39:
			style.fg = ""
		case code >= 40 && code <= 47:
			style.bg = ansiColors[code-40]
		case code == 48:
			if i+2 < len(params) && params[i+1] == 5 {
				style.bg = color256ToHex(params[i+2])
				i += 2
			} else if i+4 < len(params) && params[i+1] == 2 {
				style.bg = fmt.Sprintf("rgb(%d,%d,%d)", params[i+2], params[i+3], params[i+4])
				i += 4
			}
		case code == 49:
			style.bg = ""
		case code >= 90 && code <= 97:
			style.fg = ansiColors[code-90+8]
		case code >= 100 && code <= 107:
			style.bg = ansiColors[code-100+8]
		}
	}
}

func parseSGRParams(s string) []int {
	if s == "" {
		return []int{0}
	}
	parts := strings.Split(s, ";")
	out := make([]int, 0, len(parts))
	for _, part := range parts {
		n, err := strconv.Atoi(part)
		if err != nil {
			n = 0
		}
		out = append(out, n)
	}
	return out
}

var htmlEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&#039;")

// ansiRegex matches SGR (ESC[...m), other CSI sequences, OSC sequences and
// APC sequences (the last two terminated by BEL or ST).
var ansiRegex = lazyregexp.New(`\x1b\[([\d;]*)m|\x1b\[[0-?]*[ -/]*[@-~]|\x1b\]([^\x07\x1b]*)(?:\x07|\x1b\\)|\x1b_[^\x07\x1b]*(?:\x07|\x1b\\)`)

// osc8Regex matches an OSC 8 hyperlink payload: "8;params;url".
var osc8Regex = lazyregexp.New(`(?s)^8;[^;]*;(.*)$`)

var safeLinkRegex = lazyregexp.New(`(?i)^(?:https?:|mailto:)`)

// jsWhitespace is ECMAScript WhiteSpace plus LineTerminator, the set String.prototype.trim removes.
const jsWhitespace = "\t\n\v\f\r \u00a0\u1680\u2000\u2001\u2002\u2003\u2004\u2005\u2006\u2007\u2008\u2009\u200a\u2028\u2029\u202f\u205f\u3000\ufeff"

// stripAnsiEscapes removes every escape sequence ansiToHTML converts, plus stray ESC characters.
func stripAnsiEscapes(text string) string {
	return strings.ReplaceAll(ansiRegex.ReplaceAllString(text, ""), "\x1b", "")
}

// stripC0Controls drops C0 controls and DEL: browsers strip them from URLs, so they are removed before the scheme check.
func stripC0Controls(s string) string {
	return strings.Map(func(r rune) rune {
		if r <= 0x1f || r == 0x7f {
			return -1
		}
		return r
	}, s)
}

func ansiToHTML(text string) string {
	style := textStyle{}
	var out strings.Builder
	last := 0
	inSpan := false
	inLink := false

	closeSpan := func() {
		if inSpan {
			out.WriteString("</span>")
			inSpan = false
		}
	}
	openSpan := func() {
		if style.hasStyle() {
			out.WriteString(`<span style="` + style.inlineCSS() + `">`)
			inSpan = true
		}
	}

	for _, m := range ansiRegex.FindAllStringSubmatchIndex(text, -1) {
		if before := text[last:m[0]]; before != "" {
			out.WriteString(htmlEscaper.Replace(strings.ReplaceAll(before, "\x1b", "")))
		}
		switch {
		case m[2] >= 0:
			closeSpan()
			applySGRCodes(parseSGRParams(text[m[2]:m[3]]), &style)
			openSpan()
		case m[4] >= 0:
			if link := osc8Regex.FindStringSubmatch(text[m[4]:m[5]]); link != nil {
				// Keep spans nested inside the link: close and reopen the current style around link changes.
				url := stripC0Controls(strings.Trim(link[1], jsWhitespace))
				closeSpan()
				if inLink {
					out.WriteString("</a>")
					inLink = false
				}
				if url != "" && safeLinkRegex.MatchString(url) {
					out.WriteString(`<a href="` + htmlEscaper.Replace(url) + `">`)
					inLink = true
				}
				openSpan()
			}
			// Other OSC sequences are dropped.
		}
		// Other CSI and APC sequences are dropped.
		last = m[1]
	}
	if remaining := text[last:]; remaining != "" {
		out.WriteString(htmlEscaper.Replace(strings.ReplaceAll(remaining, "\x1b", "")))
	}
	closeSpan()
	if inLink {
		out.WriteString("</a>")
	}
	return out.String()
}

func ansiLinesToHTML(lines []string) string {
	parts := make([]string, 0, len(lines))
	for _, line := range lines {
		body := ansiToHTML(line)
		if body == "" {
			body = "&nbsp;"
		}
		parts = append(parts, `<div class="ansi-line">`+body+`</div>`)
	}
	return strings.Join(parts, "")
}
