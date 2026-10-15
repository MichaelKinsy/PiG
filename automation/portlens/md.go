// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"html"
	"regexp"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	gmhtml "github.com/yuin/goldmark/renderer/html"
)

var mdRenderer = goldmark.New(goldmark.WithExtensions(extension.GFM), goldmark.WithRendererOptions(gmhtml.WithHardWraps()))

var (
	hrefRe     = regexp.MustCompile(`<a href="([^"]*)"([^>]*)>`)
	anchorD    = regexp.MustCompile(`(?i)(?:^|/)DIVERGENCES\.md#d(\d+)\b|^#d(\d+)\b|additive-features\.md#d(\d+)\b`)
	headingRe  = regexp.MustCompile(`<(/?)h([1-6])([ >])`)
	tagSplitRe = regexp.MustCompile(`(<[^>]*>)`)
)

// renderMarkdown renders note markdown to HTML. Raw HTML in the source is escaped. Links to other divergence records point at the report's own anchors, other relative links lose their target, and absolute links open with rel=noopener. Headings drop two levels so the page keeps its own outline. known lists the divergence ids the report has, and a bare D-number outside code links to it.
func renderMarkdown(src string, known map[string]bool) string {
	var buf bytes.Buffer
	if err := mdRenderer.Convert([]byte(src), &buf); err != nil {
		return "<p>" + html.EscapeString(src) + "</p>"
	}
	out := buf.String()
	out = hrefRe.ReplaceAllStringFunc(out, func(m string) string {
		sub := hrefRe.FindStringSubmatch(m)
		href := html.UnescapeString(sub[1])
		if strings.HasPrefix(href, "https://") || strings.HasPrefix(href, "http://") {
			return `<a href="` + sub[1] + `" rel="noopener noreferrer"` + sub[2] + `>`
		}
		if d := anchorD.FindStringSubmatch(href); d != nil {
			for _, g := range d[1:] {
				if g != "" {
					return `<a href="#D` + g + `" data-d="D` + g + `"` + sub[2] + `>`
				}
			}
		}
		return `<span class="deadlink" title="` + html.EscapeString(href) + `"` + sub[2] + `>`
	})
	// A dead link's closing tag still reads </a>; pair it with the span.
	out = fixDeadLinks(out)
	out = headingRe.ReplaceAllStringFunc(out, func(m string) string {
		sub := headingRe.FindStringSubmatch(m)
		lvl := int(sub[2][0] - '0')
		if lvl < 4 {
			lvl += 2
		}
		if lvl > 6 {
			lvl = 6
		}
		return "<" + sub[1] + "h" + string(rune('0'+lvl)) + sub[3]
	})
	return linkDNumbers(out, known)
}

// fixDeadLinks turns the </a> that closes a rewritten <span class="deadlink"> into </span>.
func fixDeadLinks(s string) string {
	var out strings.Builder
	var stack []bool // true when the open element is a dead-link span
	i := 0
	for i < len(s) {
		j := strings.IndexByte(s[i:], '<')
		if j < 0 {
			out.WriteString(s[i:])
			break
		}
		out.WriteString(s[i : i+j])
		i += j
		k := strings.IndexByte(s[i:], '>')
		if k < 0 {
			out.WriteString(s[i:])
			break
		}
		tag := s[i : i+k+1]
		i += k + 1
		switch {
		case strings.HasPrefix(tag, `<span class="deadlink"`):
			stack = append(stack, true)
		case strings.HasPrefix(tag, "<a "):
			stack = append(stack, false)
		case tag == "</a>":
			if n := len(stack); n > 0 {
				if stack[n-1] {
					tag = "</span>"
				}
				stack = stack[:n-1]
			}
		}
		out.WriteString(tag)
	}
	return out.String()
}

// linkDNumbers links D-number words in text outside code, links and pre blocks.
func linkDNumbers(s string, known map[string]bool) string {
	parts := tagSplitRe.Split(s, -1)
	tags := tagSplitRe.FindAllString(s, -1)
	var out strings.Builder
	depth := 0 // inside code, pre or a link
	for i, text := range parts {
		if depth == 0 {
			text = dRef.ReplaceAllStringFunc(text, func(m string) string {
				if known[m] {
					return `<a href="#` + m + `" data-d="` + m + `">` + m + `</a>`
				}
				return m
			})
		}
		out.WriteString(text)
		if i < len(tags) {
			t := tags[i]
			switch {
			case strings.HasPrefix(t, "<code"), strings.HasPrefix(t, "<pre"), strings.HasPrefix(t, "<a "), strings.HasPrefix(t, "<span class=\"deadlink\""):
				depth++
			case t == "</code>", t == "</pre>", t == "</a>", t == "</span>":
				if depth > 0 {
					depth--
				}
			}
			out.WriteString(t)
		}
	}
	return out.String()
}
