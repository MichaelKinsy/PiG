package tui

import (
	"regexp"
	"strings"
	"testing"
)

var (
	markdownSGRPattern  = regexp.MustCompile("\x1b\\[[0-9;]*m")
	markdownOSC8Pattern = regexp.MustCompile("\x1b\\]8;;[^\x1b]*\x1b\\\\")
)

// markdownStripSGR is stripAnsi of packages/tui/test/markdown.test.ts: it removes SGR sequences only.
func markdownStripSGR(line string) string { return markdownSGRPattern.ReplaceAllString(line, "") }

func renderHyperlinkMarkdown(t *testing.T, source string) string {
	t.Helper()
	mdCaseSetup(t)
	SetCapabilities(TerminalCapabilities{TrueColor: false, Hyperlinks: true})
	return strings.Join(NewMarkdownWithOptions(source, 0, 0, mdUpstreamTheme(), nil, nil).Render(160), "")
}

// Ports the markdown.test.ts cases added with fa168515: bare URLs stop at CJK punctuation.
func TestMarkdownBareURLCJKPunctuation(t *testing.T) {
	t.Run("should stop bare URLs at CJK punctuation and preserve the following text", func(t *testing.T) {
		for _, punctuation := range []rune("。，；：！？、“”‘’（）【】《》「」『』…—·・〈〉［］｛｝．｡｢｣､･") {
			for _, url := range []string{"https://example.com", "https://example.com/中文?q=搜索#片段", "www.example.com"} {
				source := url + string(punctuation) + "后面的中文"
				output := renderHyperlinkMarkdown(t, source)
				href := url
				if strings.HasPrefix(url, "www.") {
					href = "http://" + url
				}
				if !strings.Contains(output, "\x1b]8;;"+href+"\x1b\\") {
					t.Fatalf("%q: no hyperlink to %q in %q", source, href, output)
				}
				if !strings.Contains(markdownStripSGR(output), "\x1b]8;;\x1b\\"+string(punctuation)+"后面的中文") {
					t.Fatalf("%q: following text is linked in %q", source, output)
				}
				if got := strings.TrimRight(markdownStripSGR(markdownOSC8Pattern.ReplaceAllString(output, "")), " \t\n"); got != source {
					t.Fatalf("%q: visible text %q", source, got)
				}
			}
		}
	})
	t.Run("should preserve Chinese characters without punctuation in bare URLs", func(t *testing.T) {
		for _, url := range []string{"https://example.com紧接着中文", "https://example.com/中文?q=搜索#片段", "https://example.com/テストー한글"} {
			if output := renderHyperlinkMarkdown(t, url); !strings.Contains(output, "\x1b]8;;"+url+"\x1b\\") {
				t.Fatalf("%q: no hyperlink to the whole URL in %q", url, output)
			}
		}
	})
	t.Run("should preserve CJK punctuation in explicit links and angle autolinks", func(t *testing.T) {
		url := "https://example.com/中文，路径·テスト・한글…—。"
		for _, source := range []string{"[文档](" + url + ")", "<" + url + ">"} {
			if output := renderHyperlinkMarkdown(t, source); !strings.Contains(output, "\x1b]8;;"+url+"\x1b\\") {
				t.Fatalf("%q: no hyperlink to the whole URL in %q", source, output)
			}
		}
	})
	t.Run("should preserve English punctuation trimming before Chinese punctuation", func(t *testing.T) {
		output := renderHyperlinkMarkdown(t, "https://example.com.，后面的中文")
		if !strings.Contains(output, "\x1b]8;;https://example.com\x1b\\") {
			t.Fatalf("no hyperlink to https://example.com in %q", output)
		}
		if !strings.Contains(markdownStripSGR(output), "\x1b]8;;\x1b\\.，后面的中文") {
			t.Fatalf("trailing text is linked in %q", output)
		}
	})
}
