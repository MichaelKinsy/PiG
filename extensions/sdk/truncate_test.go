package sdk

import (
	"math"
	"testing"
)

func TestFormatSizeRoundsHalfUpLikeToFixed(t *testing.T) {
	for bytes, want := range map[int]string{0: "0B", 1023: "1023B", 1024: "1.0KB", 1280: "1.3KB", 1536: "1.5KB", 1048575: "1024.0KB", 1048576: "1.0MB", 5505024: "5.3MB"} {
		if got := FormatSize(bytes); got != want {
			t.Errorf("FormatSize(%d) = %q, want %q", bytes, got, want)
		}
	}
}

func TestTruncateTailKeepsTheEndOfAnOversizedLastLine(t *testing.T) {
	r := TruncateTail("日本語のテキスト", TruncationOptions{MaxBytes: new(7)})
	if r.Content != "スト" || !r.LastLinePartial || r.TruncatedBy != "bytes" || r.OutputBytes != 6 {
		t.Fatalf("got %+v", r)
	}
}

func TestTruncateHeadNeverReturnsAPartialLine(t *testing.T) {
	r := TruncateHead("no newline at all", TruncationOptions{MaxBytes: new(4)})
	if r.Content != "" || !r.FirstLineExceedsLimit || r.TruncatedBy != "bytes" {
		t.Fatalf("got %+v", r)
	}
	r = TruncateHead("a\nb\nc\n", TruncationOptions{MaxLines: new(2)})
	if r.Content != "a\nb" || r.TruncatedBy != "lines" || r.TotalLines != 3 || r.OutputLines != 2 {
		t.Fatalf("got %+v", r)
	}
}

func TestTruncateLineCountsUTF16Units(t *testing.T) {
	text, was := TruncateLine("😀😀😀😀", 6)
	if !was || text != "😀😀😀... [truncated]" {
		t.Fatalf("got %q, %v", text, was)
	}
	if text, was := TruncateLine("😀😀😀", 6); was || text != "😀😀😀" {
		t.Fatalf("a line of exactly maxChars units was cut: %q", text)
	}
}

// Values from Pi's truncate.ts (v1.1.0) run in Node. Pi never throws on a negative limit: Buffer.slice past the end is empty and String.prototype.slice
// counts a negative end back from the end of the line.
func TestTruncationNegativeLimitsMatchPi(t *testing.T) {
	r := TruncateTail("abc\ndef", TruncationOptions{MaxBytes: new(-1)})
	if r.Content != "" || r.OutputLines != 1 || r.OutputBytes != 0 || !r.LastLinePartial || r.TruncatedBy != "bytes" {
		t.Fatalf("got %+v", r)
	}
	for _, c := range []struct {
		line     string
		maxChars int
		want     string
	}{{"abcdef", -2, "abcd"}, {"ab", -5, ""}, {"😀😀", -1, "😀\uFFFD"}} {
		if text, was := TruncateLine(c.line, c.maxChars); !was || text != c.want+"... [truncated]" {
			t.Errorf("TruncateLine(%q, %d) = %q, %v; want %q", c.line, c.maxChars, text, was, c.want+"... [truncated]")
		}
	}
}

// Pi formats the double quotient, so the largest int still rounds like toFixed(1): (2^63-1)/2^20 is 8796093022208.0MB in Node.
func TestFormatSizeDoesNotOverflowNearMaxInt(t *testing.T) {
	for bytes, want := range map[int]string{9007199254740991: "8589934592.0MB", math.MaxInt64: "8796093022208.0MB", -5: "-5B"} {
		if got := FormatSize(bytes); got != want {
			t.Errorf("FormatSize(%d) = %q, want %q", bytes, got, want)
		}
	}
}
