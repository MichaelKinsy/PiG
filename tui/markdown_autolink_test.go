package tui

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

// Pi pins marked 18.0.11 (packages/tui/package.json:56). Its GFM url/email
// rules are in marked/lib/marked.esm.js:14, token emission in :44, and the
// inlineText/URL dispatch in :58. Compare token types, raw text, and href.
func TestAutoLinkTokensMatchMarked(t *testing.T) {
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
	kinds := map[markedTokenKind]string{markedText: "text", markedLink: "link"}
	for c, want := range cases {
		got := lexMarkedInline(inputs[c], &markedInlineState{})
		if len(got) != len(want) {
			t.Fatalf("input %d: %d tokens, want %d", c, len(got), len(want))
		}
		for i, token := range want {
			if kinds[got[i].kind] != token.Type || got[i].raw != token.Raw || got[i].text != token.Text || got[i].href != token.Href {
				t.Fatalf("input %d token %d: (%s, %q, %q, %q), want (%s, %q, %q, %q)", c, i, kinds[got[i].kind], got[i].raw, got[i].text, got[i].href, token.Type, token.Raw, token.Text, token.Href)
			}
		}
	}
}

// marked's url rule runs only where a token starts; inline text stops before an email local part and a scheme. These starts are boundaries inline text can produce.
func TestAutoLinkURLRuleAtTokenStarts(t *testing.T) {
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
			runes := []rune(tc.input)
			for j, start := range tc.starts {
				token, ok := markedURL(runes[start:])
				want := tc.texts[j]
				if token.text != want || ok != (want != "") {
					t.Fatalf("start %d: text=%q ok=%t, want %q", start, token.text, ok, want)
				}
				if ok {
					wantURL := want
					if strings.ContainsRune(want, '@') {
						wantURL = "mailto:" + want
					}
					if token.href != wantURL || token.raw != want {
						t.Fatalf("start %d: href=%q raw=%q", start, token.href, token.raw)
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

func BenchmarkAutoLinkLexUnbroken(b *testing.B) {
	for _, input := range []struct{ name, text string }{
		{"plain", strings.Repeat("x", 64<<10)},
		{"invalid-email", strings.Repeat("x", 64<<10) + "@invalid"},
		{"multiple-at", strings.Repeat("x@", 32<<10)},
		{"url-prefixes", strings.Repeat("http:", (64<<10)/5)},
		{"closing-parens", "https://example.com" + strings.Repeat(")", 64<<10)},
	} {
		b.Run(input.name, func(b *testing.B) {
			b.SetBytes(int64(len(input.text)))
			b.ReportAllocs()
			for b.Loop() {
				lexMarkedInline(input.text, &markedInlineState{})
			}
		})
	}
}
