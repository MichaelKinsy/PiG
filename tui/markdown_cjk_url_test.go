package tui

import (
	"regexp"
	"strings"
	"testing"
)

var (
	cjkURLSGR  = regexp.MustCompile(`\x1b\[[0-9;]*m`)
	cjkURLOSC8 = regexp.MustCompile(`\x1b\]8;;[^\x1b]*\x1b\\`)
)

func cjkURLRender(t *testing.T, source string) string {
	t.Helper()
	previous := GetCapabilities()
	t.Cleanup(func() { SetCapabilities(previous) })
	SetCapabilities(TerminalCapabilities{TrueColor: false, Hyperlinks: true})
	return strings.Join(NewMarkdownWithOptions(source, 0, 0, mdUpstreamTheme(), nil, nil).Render(160), "")
}

// TestMarkdownBareURLStopsAtCJKPunctuationPiCases ports Pi fa168515 (earendil-works/pi#10933, packages/tui/test/markdown.test.ts): a bare URL ends at CJK punctuation and the following text stays, explicit links and angle autolinks keep it, and CJK letters stay part of the URL.
func TestMarkdownBareURLStopsAtCJKPunctuationPiCases(t *testing.T) {
	t.Run("stops at CJK punctuation and preserves the following text", func(t *testing.T) {
		for _, punctuation := range "。，；：！？、“”‘’（）【】《》「」『』…—·・〈〉［］｛｝．｡｢｣､･" {
			for _, url := range []string{"https://example.com", "https://example.com/中文?q=搜索#片段", "www.example.com"} {
				source := url + string(punctuation) + "后面的中文"
				output := cjkURLRender(t, source)
				href := url
				if strings.HasPrefix(url, "www.") {
					href = "http://" + url
				}
				if !strings.Contains(output, "\x1b]8;;"+href+"\x1b\\") {
					t.Fatalf("%q: no hyperlink to %q in %q", source, href, output)
				}
				if plain := cjkURLSGR.ReplaceAllString(output, ""); !strings.Contains(plain, "\x1b]8;;\x1b\\"+string(punctuation)+"后面的中文") {
					t.Fatalf("%q: the text after the link does not start at the punctuation: %q", source, plain)
				}
				if got := strings.TrimRight(cjkURLSGR.ReplaceAllString(cjkURLOSC8.ReplaceAllString(output, ""), ""), " "); got != source {
					t.Fatalf("%q: visible text %q", source, got)
				}
			}
		}
	})
	t.Run("preserves Chinese characters without punctuation in bare URLs", func(t *testing.T) {
		for _, url := range []string{"https://example.com紧接着中文", "https://example.com/中文?q=搜索#片段", "https://example.com/テストー한글"} {
			if output := cjkURLRender(t, url); !strings.Contains(output, "\x1b]8;;"+url+"\x1b\\") {
				t.Fatalf("%q: no hyperlink to the whole URL in %q", url, output)
			}
		}
	})
	t.Run("preserves CJK punctuation in explicit links and angle autolinks", func(t *testing.T) {
		url := "https://example.com/中文，路径·テスト・한글…—。"
		for _, source := range []string{"[文档](" + url + ")", "<" + url + ">"} {
			if output := cjkURLRender(t, source); !strings.Contains(output, "\x1b]8;;"+url+"\x1b\\") {
				t.Fatalf("%q: no hyperlink to the whole URL in %q", source, output)
			}
		}
	})
	t.Run("preserves English punctuation trimming before Chinese punctuation", func(t *testing.T) {
		output := cjkURLRender(t, "https://example.com.，后面的中文")
		if !strings.Contains(output, "\x1b]8;;https://example.com\x1b\\") {
			t.Fatalf("no hyperlink to https://example.com in %q", output)
		}
		if plain := cjkURLSGR.ReplaceAllString(output, ""); !strings.Contains(plain, "\x1b]8;;\x1b\\.，后面的中文") {
			t.Fatalf("the text after the link is not \".，后面的中文\": %q", plain)
		}
	})
}
