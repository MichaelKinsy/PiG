package widthx

import (
	"slices"
	"strings"
	"testing"
)

func TestUpstreamWrapAnsi(t *testing.T) {
	const underlineOn, underlineOff, reset = "\x1b[4m", "\x1b[24m", "\x1b[0m"
	// .upstream/v0.87.1/packages/tui/test/wrap-ansi.test.ts:7
	t.Run("should not apply underline style before the styled text", func(t *testing.T) {
		got := WrapTextWithAnsi("read this thread "+underlineOn+"https://example.com/very/long/path/that/will/wrap"+underlineOff, 40)
		if len(got) < 2 || got[0] != "read this thread" || !strings.HasPrefix(got[1], underlineOn) || !strings.Contains(got[1], "https://") {
			t.Fatalf("lines = %q", got)
		}
	})
	// .upstream/v0.87.1/packages/tui/test/wrap-ansi.test.ts:23
	t.Run("should not have whitespace before underline reset code", func(t *testing.T) {
		got := WrapTextWithAnsi(underlineOn+"underlined text here "+underlineOff+"more", 18)
		if len(got) == 0 || strings.Contains(got[0], " "+underlineOff) {
			t.Fatalf("lines = %q", got)
		}
	})
	// .upstream/v0.87.1/packages/tui/test/wrap-ansi.test.ts:33
	t.Run("should not bleed underline to padding - each line should end with reset for underline only", func(t *testing.T) {
		got := WrapTextWithAnsi("prefix "+underlineOn+"https://example.com/very/long/path/that/will/definitely/wrap"+underlineOff+" suffix", 30)
		for i := 1; i < len(got)-1; i++ {
			line := got[i]
			if strings.Contains(line, underlineOn) && (!strings.HasSuffix(line, underlineOff) || strings.HasSuffix(line, reset)) {
				t.Errorf("line = %q", line)
			}
		}
	})
	// .upstream/v0.87.1/packages/tui/test/wrap-ansi.test.ts:55
	t.Run("should preserve background color across wrapped lines without full reset", func(t *testing.T) {
		got := WrapTextWithAnsi("\x1b[44mhello world this is blue background text"+reset, 15)
		for i, line := range got {
			if !strings.Contains(line, "\x1b[44m") || (i < len(got)-1 && strings.HasSuffix(line, reset)) {
				t.Errorf("line = %q", line)
			}
		}
	})
	// .upstream/v0.87.1/packages/tui/test/wrap-ansi.test.ts:73
	t.Run("should reset underline but preserve background when wrapping underlined text inside background", func(t *testing.T) {
		got := WrapTextWithAnsi("\x1b[41mprefix "+underlineOn+"UNDERLINED_CONTENT_THAT_WRAPS"+underlineOff+" suffix"+reset, 20)
		for i, line := range got {
			if !strings.Contains(line, "[41m") && !strings.Contains(line, ";41m") && !strings.Contains(line, "[41;") {
				t.Errorf("background missing: %q", line)
			}
			on := strings.Contains(line, "[4m") || strings.Contains(line, "[4;") || strings.Contains(line, ";4m")
			if i < len(got)-1 && on && !strings.Contains(line, underlineOff) && (!strings.HasSuffix(line, underlineOff) || strings.HasSuffix(line, reset)) {
				t.Errorf("underline reset missing: %q", line)
			}
		}
	})
	// .upstream/v0.87.1/packages/tui/test/wrap-ansi.test.ts:104
	t.Run("should handle LF, CRLF, and CR line endings", func(t *testing.T) {
		got := WrapTextWithAnsi("first\nsecond\r\nthird\rfourth", 80)
		if !slices.Equal(got, []string{"first", "second", "third", "fourth"}) {
			t.Fatal(got)
		}
	})
	// .upstream/v0.87.1/packages/tui/test/wrap-ansi.test.ts:113
	t.Run("should preserve ANSI state across CRLF and CR line endings", func(t *testing.T) {
		got := WrapTextWithAnsi("\x1b[31mfirst\r\nsecond\rthird"+reset, 80)
		if !slices.Equal(got, []string{"\x1b[31mfirst", "\x1b[31msecond", "\x1b[31mthird" + reset}) {
			t.Fatal(got)
		}
	})
	// .upstream/v0.87.1/packages/tui/test/wrap-ansi.test.ts:124
	t.Run("should wrap plain text correctly", func(t *testing.T) {
		got := WrapTextWithAnsi("hello world this is a test", 10)
		if len(got) <= 1 {
			t.Fatal(got)
		}
		for _, line := range got {
			if VisibleWidth(line) > 10 {
				t.Fatal(line)
			}
		}
	})
	// .upstream/v0.87.1/packages/tui/test/wrap-ansi.test.ts:134
	t.Run("should break CJK runs at grapheme boundaries after Latin text", func(t *testing.T) {
		got := WrapTextWithAnsi("This is an example 中文汉字测试段落内容中文汉字测试段落内容.", 40)
		if !slices.Equal(got, []string{"This is an example 中文汉字测试段落内容", "中文汉字测试段落内容."}) {
			t.Fatal(got)
		}
		for _, line := range got {
			if VisibleWidth(line) > 40 {
				t.Fatal(line)
			}
		}
	})
	// .upstream/v0.87.1/packages/tui/test/wrap-ansi.test.ts:144
	t.Run("should preserve color codes when wrapping CJK runs", func(t *testing.T) {
		got := WrapTextWithAnsi("\x1b[31mThis is an example 中文汉字测试段落内容中文汉字测试段落内容."+reset, 40)
		if !slices.Equal(got, []string{"\x1b[31mThis is an example 中文汉字测试段落内容", "\x1b[31m中文汉字测试段落内容." + reset}) {
			t.Fatal(got)
		}
		for _, line := range got {
			if VisibleWidth(line) > 40 {
				t.Fatal(line)
			}
		}
	})
	// .upstream/v0.87.1/packages/tui/test/wrap-ansi.test.ts:158
	t.Run("should ignore OSC 133 semantic markers in visible width", func(t *testing.T) {
		if got := VisibleWidth("\x1b]133;A\x07hello\x1b]133;B\x07"); got != 5 {
			t.Fatal(got)
		}
	})
	// .upstream/v0.87.1/packages/tui/test/wrap-ansi.test.ts:163
	t.Run("should ignore OSC sequences terminated with ST in visible width", func(t *testing.T) {
		if got := VisibleWidth("\x1b]133;A\x1b\\hello\x1b]133;B\x1b\\"); got != 5 {
			t.Fatal(got)
		}
	})
	// .upstream/v0.87.1/packages/tui/test/wrap-ansi.test.ts:168
	t.Run("should treat isolated regional indicators as width 2", func(t *testing.T) {
		for _, s := range []string{"🇨", "🇨🇳"} {
			if got := VisibleWidth(s); got != 2 {
				t.Errorf("%q width = %d", s, got)
			}
		}
	})
	// .upstream/v0.87.1/packages/tui/test/wrap-ansi.test.ts:173
	t.Run("should truncate trailing whitespace that exceeds width", func(t *testing.T) {
		got := WrapTextWithAnsi("  ", 1)
		if len(got) == 0 || VisibleWidth(got[0]) > 1 {
			t.Fatal(got)
		}
	})
	// .upstream/v0.87.1/packages/tui/test/wrap-ansi.test.ts:178
	t.Run("should preserve color codes across wraps", func(t *testing.T) {
		got := WrapTextWithAnsi("\x1b[31mhello world this is red"+reset, 10)
		for i, line := range got {
			if i > 0 && !strings.HasPrefix(line, "\x1b[31m") {
				t.Errorf("red prefix missing: %q", line)
			}
			if i < len(got)-1 && strings.HasSuffix(line, reset) {
				t.Errorf("full reset: %q", line)
			}
		}
	})
}

func TestUpstreamWrapAnsiHyperlinks(t *testing.T) {
	const open, close = "\x1b]8;;https://example.com\x1b\\", "\x1b]8;;\x1b\\"
	// .upstream/v0.87.1/packages/tui/test/wrap-ansi.test.ts:199
	t.Run("re-emits OSC 8 open at the start of continuation lines", func(t *testing.T) {
		for _, line := range WrapTextWithAnsi(open+"0123456789"+close, 6) {
			if strings.TrimSpace(StripAnsi(line)) != "" && !strings.Contains(line, open) {
				t.Fatalf("missing reopen: %q", line)
			}
		}
	})
	// .upstream/v0.87.1/packages/tui/test/wrap-ansi.test.ts:221
	t.Run("closes OSC 8 before each line break", func(t *testing.T) {
		lines := WrapTextWithAnsi(open+"0123456789"+close, 6)
		for _, line := range lines[:len(lines)-1] {
			if strings.Contains(line, open) && !strings.HasSuffix(line, close) {
				t.Fatalf("missing close: %q", line)
			}
		}
	})
	// .upstream/v0.87.1/packages/tui/test/wrap-ansi.test.ts:238
	t.Run("preserves BEL terminators when wrapping OAuth-style hyperlinks", func(t *testing.T) {
		url := "https://example.com/oauth/" + strings.Repeat("a", 32)
		lines := WrapTextWithAnsi("\x1b]8;;"+url+"\x07"+url+"\x1b]8;;\x07", 20)
		if len(lines) <= 1 {
			t.Fatal(lines)
		}
		for i, line := range lines {
			if !strings.Contains(line, "\x1b]8;;"+url+"\x07") || strings.Contains(line, "\x1b]8;;"+url+"\x1b\\") {
				t.Errorf("wrong opener: %q", line)
			}
			if i < len(lines)-1 && !strings.HasSuffix(line, "\x1b]8;;\x07") {
				t.Errorf("wrong close: %q", line)
			}
		}
	})
	// .upstream/v0.87.1/packages/tui/test/wrap-ansi.test.ts:253
	t.Run("does not emit OSC 8 sequences on lines that are outside the hyperlink", func(t *testing.T) {
		lines := WrapTextWithAnsi("before "+open+"link"+close+" after", 80)
		if len(lines) != 1 || strings.Count(lines[0], open) != 1 || strings.Count(lines[0], close) != 1 {
			t.Fatal(lines)
		}
	})
}
