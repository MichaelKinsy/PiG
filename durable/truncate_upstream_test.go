package durable

import (
	"encoding/json"
	"testing"
	"unicode/utf8"
)

// Ports packages/durable/test/env-truncate.test.ts

// byteLength is the test's TextEncoder length: a lone UTF-16 surrogate encodes as U+FFFD.
func byteLength(content string) int {
	return len([]byte(string([]rune(content))))
}

func TestTruncateUtilitiesUpstream(t *testing.T) {
	// env-truncate.test.ts:13
	t.Run("reports UTF-8 byte counts in truncation results", func(t *testing.T) {
		content := "aé🙂\nb"
		result := TruncateHead(content, TruncationOptions{MaxBytes: new(100), MaxLines: new(10)})

		if result.Truncated {
			t.Fatalf("truncated = true")
		}
		if result.TotalBytes != byteLength(content) || result.OutputBytes != byteLength(content) || result.TotalBytes != 9 {
			t.Fatalf("totalBytes %d outputBytes %d, want %d and 9", result.TotalBytes, result.OutputBytes, byteLength(content))
		}
	})

	// env-truncate.test.ts:23. Upstream runs the module in a Node process without Buffer to exercise the fallback
	// encoder. Go has one byte counter over UTF-8 strings; the inputs arrive through JSON as in the fixture, so lone
	// surrogates decode to U+FFFD exactly as TextEncoder encodes them.
	t.Run("counts UTF-8 bytes and truncates correctly in a runtime without Buffer", func(t *testing.T) {
		var inputs []string
		raw := `["", "ascii", "é", "中", "🙂", "\ud83d", "\ude42", "a\ud83d\ude42b", "\u07ff\u0800\uffff"]`
		if err := json.Unmarshal([]byte(raw), &inputs); err != nil {
			t.Fatal(err)
		}
		want := []int{0, 5, 2, 3, 4, 3, 3, 6, 8}
		for index, input := range inputs {
			if !utf8.ValidString(input) {
				t.Fatalf("input %d is not UTF-8", index)
			}
			if got := Utf8ByteLength(input); got != want[index] || got != byteLength(input) {
				t.Fatalf("utf8ByteLength(%q) = %d, want %d", input, got, want[index])
			}
		}
		head := TruncateHead("aé🙂\nb", TruncationOptions{MaxBytes: new(7), MaxLines: new(10)})
		if head.Content != "aé🙂" || head.OutputBytes != 7 || head.TruncatedBy != TruncatedByBytes {
			t.Fatalf("head = %+v", head)
		}
	})

	// env-truncate.test.ts:47
	t.Run("does not count a trailing newline as an extra line", func(t *testing.T) {
		head := TruncateHead("line\nline\nline\n", TruncationOptions{MaxBytes: new(100), MaxLines: new(3)})
		if head.Truncated || head.TotalLines != 3 || head.OutputLines != 3 {
			t.Fatalf("head = %+v", head)
		}
	})

	// env-truncate.test.ts:54
	t.Run("truncates head by line limits", func(t *testing.T) {
		head := TruncateHead("one\ntwo\nthree\nfour", TruncationOptions{MaxBytes: new(100), MaxLines: new(2)})
		if head.Content != "one\ntwo" || !head.Truncated || head.TruncatedBy != TruncatedByLines || head.TotalLines != 4 || head.OutputLines != 2 {
			t.Fatalf("head = %+v", head)
		}
	})

	// env-truncate.test.ts:65
	t.Run("reports bytes when only a trailing newline exceeds limits at the line cap", func(t *testing.T) {
		head := TruncateHead("hello\nworld\n", TruncationOptions{MaxBytes: new(11), MaxLines: new(2)})
		if head.Content != "hello\nworld" || !head.Truncated || head.TruncatedBy != TruncatedByBytes || head.TotalLines != 2 || head.OutputLines != 2 {
			t.Fatalf("head = %+v", head)
		}
	})

	// env-truncate.test.ts:75
	t.Run("truncates head on UTF-8 byte limits without partial lines", func(t *testing.T) {
		result := TruncateHead("éé\nabc", TruncationOptions{MaxBytes: new(4), MaxLines: new(10)})
		if result.Content != "éé" || !result.Truncated || result.TruncatedBy != TruncatedByBytes || result.OutputBytes != 4 || result.FirstLineExceedsLimit {
			t.Fatalf("result = %+v", result)
		}
	})

	// env-truncate.test.ts:86
	t.Run("reports head truncation when the first line exceeds the byte limit", func(t *testing.T) {
		result := TruncateHead("éé\nabc", TruncationOptions{MaxBytes: new(3), MaxLines: new(10)})
		if result.Content != "" || !result.Truncated || result.TruncatedBy != TruncatedByBytes || !result.FirstLineExceedsLimit {
			t.Fatalf("result = %+v", result)
		}
	})

	// env-truncate.test.ts:95
	t.Run("formats sizes", func(t *testing.T) {
		for _, tc := range []struct {
			bytes int
			want  string
		}{{1023, "1023B"}, {1536, "1.5KB"}, {3 * 1024 * 1024, "3.0MB"}} {
			if got := FormatSize(tc.bytes); got != tc.want {
				t.Fatalf("FormatSize(%d) = %q, want %q", tc.bytes, got, tc.want)
			}
		}
	})
}

// FormatSize rounds a tie away from zero like Number.prototype.toFixed: 1280 bytes is exactly 1.25KB.
func TestFormatSizeRoundsTiesLikeToFixed(t *testing.T) {
	if got := FormatSize(1280); got != "1.3KB" {
		t.Fatalf("FormatSize(1280) = %q, want 1.3KB", got)
	}
}
