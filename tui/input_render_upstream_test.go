package tui

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

func TestUpstreamInputSubmit(t *testing.T) {
	// .upstream/v0.87.1/packages/tui/test/input.test.ts:7.
	t.Run("submits value including backslash on Enter", func(t *testing.T) {
		input := NewInput(InputOptions{})
		var submitted *string
		input.OnSubmit = func(value string) { submitted = &value }
		for _, data := range []string{"h", "e", "l", "l", "o", "\\", "\r"} {
			input.HandleInput(data)
		}
		if submitted == nil || *submitted != "hello\\" {
			t.Fatalf("submitted=%v, want hello\\", submitted)
		}
	})
}

func TestUpstreamInputRender(t *testing.T) {
	// .upstream/v0.87.1/packages/tui/test/input.test.ts:38.
	t.Run("supports a custom prompt and styled placeholder", func(t *testing.T) {
		input := NewInput(InputOptions{Prompt: new(""), Placeholder: "Find transcript", PlaceholderStyle: func(text string) string { return "\x1b[2m" + text + "\x1b[22m" }})
		input.Focused = true
		empty := input.Render(20)[0]
		if !strings.Contains(empty, "\x1b[2m") || widthx.JSTrimEnd(widthx.StripTerminalSequences(empty)) != "Find transcript" {
			t.Fatalf("empty=%q", empty)
		}
		input.HandleInput("n")
		if populated := input.Render(20)[0]; widthx.JSTrimEnd(widthx.StripTerminalSequences(populated)) != "n" {
			t.Fatalf("populated=%q", populated)
		}
	})
	// .upstream/v0.87.1/packages/tui/test/input.test.ts:55, all four texts and three cursor positions.
	t.Run("does not overflow with wide CJK and fullwidth text", func(t *testing.T) {
		const width = 93
		cases := []string{
			"가나다라마바사아자차카타파하 한글 텍스트가 터미널 너비를 초과하면 크래시가 발생합니다 이것은 재현용 테스트입니다",
			"これはテスト文章です。日本語のテキストが正しく表示されるかどうかを確認するためのサンプルテキストです。あいうえお",
			"这是一段测试文本，用于验证中文字符在终端中的显示宽度是否被正确计算，如果不正确就会导致用户界面崩溃的问题",
			"ＡＢＣＤＥＦＧＨＩＪＫＬＭＮＯＰＱＲＳＴＵＶＷＸＹＺ０１２３４５６７８９ａｂｃｄｅｆｇｈｉｊｋｌｍ",
		}
		for _, text := range cases {
			for _, position := range []struct {
				label string
				move  func(*TextInput)
			}{
				{"start", func(*TextInput) {}},
				{"middle", func(input *TextInput) {
					for range 10 {
						input.HandleInput("\x1b[C")
					}
				}},
				{"end", func(input *TextInput) { input.HandleInput("\x05") }},
			} {
				input := NewInput(InputOptions{})
				input.SetText(text)
				input.Focused = true
				position.move(input)
				lines := input.Render(width)
				if len(lines) == 0 || lines[0] == "" || widthx.VisibleWidth(lines[0]) > width {
					t.Fatalf("overflow for %s at %s: %q", text, position.label, lines)
				}
			}
		}
	})
	// .upstream/v0.87.1/packages/tui/test/input.test.ts:88.
	t.Run("keeps the cursor visible when horizontally scrolling wide text", func(t *testing.T) {
		input := NewInput(InputOptions{})
		const width = 20
		input.SetText("가나다라마바사아자차카타파하")
		input.Focused = true
		input.HandleInput("\x01")
		for range 5 {
			input.HandleInput("\x1b[C")
		}
		lines := input.Render(width)
		if len(lines) == 0 || lines[0] == "" || widthx.VisibleWidth(lines[0]) > width {
			t.Fatalf("line=%q", lines)
		}
	})
}
