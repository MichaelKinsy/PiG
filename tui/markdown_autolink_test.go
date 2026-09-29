package tui

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

// Pi pins marked 18.0.11 (packages/tui/package.json:56). Its GFM url/email
// rules are in marked/lib/marked.esm.js:14, token emission in :44, and the
// inlineText/URL dispatch in :58. Compare text, href, and rune boundaries.
func TestAutoLinkScannerMatchesMarked(t *testing.T) {
	inputs := []string{"", "ordinary", "(user@example.com)", "日本user@example.com!", "a@b@c.com", "(https://example.com/a(b)).", "http://example.com! next@example.com?", strings.Repeat("x", 64<<10)}
	input, err := json.Marshal(inputs)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "--input-type=module", "-e", `
import fs from "node:fs";
import { Lexer } from "./widthx/testdata/pi/marked/lib/marked.esm.js";
console.log(JSON.stringify(JSON.parse(fs.readFileSync(0, "utf8")).map(text => Lexer.lexInline(text))));`)
	cmd.Stdin = strings.NewReader(string(input))
	raw, err := cmd.Output()
	if err != nil {
		t.Fatalf("pinned marked oracle: %v", err)
	}
	var cases [][]struct{ Type, Raw, Text, Href string }
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) != len(inputs) {
		t.Fatal("oracle did not return every input")
	}
	for c, tokens := range cases {
		var scanner autoLinkScanner
		runes := []rune(inputs[c])
		start := 0
		for _, token := range tokens {
			end := start + len([]rune(token.Raw))
			for start < end {
				text, href, next, ok := scanner.parseAutoLink(runes, start)
				switch token.Type {
				case "text":
					if ok {
						t.Fatalf("input %d at %d: unexpected link %q", c, start, text)
					}
					start++
				case "link":
					if !ok || text != token.Text || href != token.Href || next != end {
						t.Fatalf("input %d at %d: link (%q, %q, %d, %t), want (%q, %q, %d)", c, start, text, href, next, ok, token.Text, token.Href, end)
					}
					start = next
				default:
					t.Fatalf("unexpected oracle token %q", token.Type)
				}
			}
		}
		if start != len(runes) {
			t.Fatalf("input %d: oracle omitted source", c)
		}
	}
}

func TestAutoLinkScannerKeepsSuffixesAndTokenJumps(t *testing.T) {
	for _, tc := range []struct {
		input  string
		starts []int
		texts  []string
	}{
		{"ordinary", []int{0, 1, 7}, []string{"", "", ""}},
		{"(user@example.com)", []int{0, 1}, []string{"", "user@example.com"}},
		{"日本user@example.com!", []int{0, 1, 2}, []string{"", "", "user@example.com"}},
		{"user@example.com", []int{0, 2, 4}, []string{"user@example.com", "er@example.com", ""}},
		{"a@b@c.com", []int{0, 1, 2}, []string{"", "", "b@c.com"}},
		{"ordinary user@example.com", []int{0, 9}, []string{"", "user@example.com"}},
		{"(https://example.com/a(b)).", []int{0, 1}, []string{"", "https://example.com/a(b)"}},
		{"http://example.com! next@example.com?", []int{0, 19, 20}, []string{"http://example.com", "", "next@example.com"}},
	} {
		t.Run(tc.input, func(t *testing.T) {
			var scanner autoLinkScanner
			runes := []rune(tc.input)
			for j, start := range tc.starts {
				text, url, next, ok := scanner.parseAutoLink(runes, start)
				want := tc.texts[j]
				if text != want || ok != (want != "") {
					t.Fatalf("start %d: text=%q ok=%t, want %q", start, text, ok, want)
				}
				if ok {
					wantURL := want
					if strings.ContainsRune(want, '@') {
						wantURL = "mailto:" + want
					}
					if url != wantURL || next != start+len([]rune(want)) {
						t.Fatalf("start %d: url=%q next=%d", start, url, next)
					}
				}
			}
		})
	}
}

func TestAutoLinkScannerRenderingPrecedence(t *testing.T) {
	old := GetCapabilities()
	SetCapabilities(TerminalCapabilities{Hyperlinks: false})
	t.Cleanup(func() { SetCapabilities(old) })
	// Pi markdown.ts:686-710 applies the link color around its underline decoration.
	link := func(s string) string { return ActiveTheme().MDLink + "\x1b[4m" + s + SGRUnderlineReset + SGRFgReset }
	for _, tc := range []struct{ input, want string }{
		{"Contact user@example.com for help", "Contact " + link("user@example.com") + " for help"},
		{"Visit https://example.com for more", "Visit " + link("https://example.com") + " for more"},
		{"(user@example.com)", "(" + link("user@example.com") + ")"},
		{"`first`user@example.com", ActiveTheme().MDCode + "first" + SGRFgReset + link("user@example.com")},
		{"**bold**user@example.com", "\x1b[1mbold" + SGRBoldDimReset + link("user@example.com")},
		{"https://example.com/a(b)).", link("https://example.com/a(b)") + ")."},
	} {
		if got := NewMarkdown("").inlineMarkdown(tc.input); got != tc.want {
			t.Errorf("%q: got %q, want %q", tc.input, got, tc.want)
		}
	}
}

func BenchmarkAutoLinkScanUnbroken(b *testing.B) {
	for _, input := range []struct{ name, text string }{
		{"plain", strings.Repeat("x", 64<<10)},
		{"invalid-email", strings.Repeat("x", 64<<10) + "@invalid"},
		{"multiple-at", strings.Repeat("x@", 32<<10)},
		{"url-prefixes", strings.Repeat("http:", (64<<10)/5)},
		{"closing-parens", "https://example.com" + strings.Repeat(")", 64<<10)},
	} {
		b.Run(input.name, func(b *testing.B) {
			runes := []rune(input.text)
			b.SetBytes(int64(len(input.text)))
			b.ReportAllocs()
			for b.Loop() {
				var scanner autoLinkScanner
				for i := 0; i < len(runes); {
					_, _, next, ok := scanner.parseAutoLink(runes, i)
					if ok {
						i = next
					} else {
						i++
					}
				}
			}
		})
	}
}
