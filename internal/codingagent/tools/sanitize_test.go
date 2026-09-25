package tools

import (
	"bytes"
	"strings"
	"testing"
)

func TestStripANSI_CSI(t *testing.T) {
	in := []byte("\x1b[31mred\x1b[0m and \x1b[1;32mbold-green\x1b[m done")
	got := StripANSI(in)
	if string(got) != "red and bold-green done" {
		t.Fatalf("got %q", got)
	}
}

func TestStripANSI_NoEscapes(t *testing.T) {
	in := []byte("plain text\nwith newline\ttab")
	if !bytes.Equal(StripANSI(in), in) {
		t.Fatalf("non-ANSI input should round-trip; got %q", StripANSI(in))
	}
}

func TestStripANSI_OSCBel(t *testing.T) {
	// OSC 0;title BEL: set window title.
	in := []byte("before\x1b]0;mytitle\x07after")
	got := string(StripANSI(in))
	if got != "beforeafter" {
		t.Fatalf("got %q", got)
	}
}

// A lone ESC is not an ANSI sequence; sanitizeBinaryOutput drops it later.
func TestStripANSI_LoneEsc(t *testing.T) {
	if got := string(StripANSI([]byte("hello\x1b"))); got != "hello\x1b" {
		t.Fatalf("got %q", got)
	}
}

// Ported from upstream test/ansi-utils.test.ts.
func TestStripANSI_UpstreamCases(t *testing.T) {
	for _, c := range stripANSICompatibilityCases {
		if got := string(StripANSI([]byte(c.in))); got != c.want {
			t.Errorf("StripANSI(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	if got := string(StripANSI([]byte("\x1bcdone"))); got != "done" {
		t.Errorf("RIS: %q", got)
	}
	for _, r := range "ghijklmrst" {
		if got := string(StripANSI([]byte("\x1b" + string(r) + "ok"))); got != "ok" {
			t.Errorf("ESC %c: %q", r, got)
		}
	}
	in := "a\x1b[31mred\x1b[0m\x1b]8;;https://example.com\x07link\x1b]8;;\x07z"
	if got := string(StripANSI([]byte(in))); got != "aredlinkz" {
		t.Errorf("common sequences: %q", got)
	}
}

func TestSanitizeBinaryOutput_PreservesPrintable(t *testing.T) {
	in := "hello\tworld\nline2\r\n"
	got := SanitizeBinaryOutput(in)
	if got != in {
		t.Fatalf("printable+\\t\\n\\r should round-trip; got %q", got)
	}
}

// Upstream filters control characters (except tab, LF, CR) and U+FFF9..U+FFFB.
func TestSanitizeBinaryOutput_RemovesControlCharacters(t *testing.T) {
	in := "ok\x01\x02\x1b\x7f\u0085\ufff9\ufffb\ufffcend"
	want := "ok\x7f\u0085\ufffcend"
	if got := SanitizeBinaryOutput(in); got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestSanitizeBinaryOutput_PreservesUTF8(t *testing.T) {
	in := "héllo 🌍"
	got := SanitizeBinaryOutput(in)
	if got != in {
		t.Fatalf("UTF-8 must round-trip; got %q", got)
	}
}

func TestTruncateTail_NoTruncation(t *testing.T) {
	in := "line1\nline2\nline3"
	tr := TruncateTail(in, 1024, 100)
	if tr.Truncated {
		t.Errorf("small input should not truncate; got %+v", tr)
	}
	if tr.Content != in {
		t.Errorf("content should round-trip; got %q", tr.Content)
	}
	if tr.TotalLines != 3 || tr.OutputLines != 3 {
		t.Errorf("line counts: %+v", tr)
	}
}

func TestTruncateTail_LineCap(t *testing.T) {
	var b strings.Builder
	for i := 1; i <= 3000; i++ {
		b.WriteString("line\n")
	}
	tr := TruncateTail(b.String(), 1024*1024, 2000)
	if !tr.Truncated {
		t.Fatalf("expected truncation; got %+v", tr)
	}
	if tr.TruncatedBy != "lines" {
		t.Errorf("truncatedBy: %q want lines", tr.TruncatedBy)
	}
	if tr.OutputLines != 2000 {
		t.Errorf("outputLines: %d want 2000", tr.OutputLines)
	}
	// Total: 3000 lines (trailing \n is a terminator, not an extra line).
	if tr.TotalLines != 3000 {
		t.Errorf("totalLines: %d", tr.TotalLines)
	}
}

func TestTruncateTail_ByteCap(t *testing.T) {
	// 100 lines of 100 'x' = 10000 bytes (without newlines), cap at 5000.
	var b strings.Builder
	for range 100 {
		b.WriteString(strings.Repeat("x", 100))
		b.WriteByte('\n')
	}
	tr := TruncateTail(b.String(), 5000, 10000)
	if !tr.Truncated {
		t.Fatalf("expected truncation")
	}
	if tr.TruncatedBy != "bytes" {
		t.Errorf("truncatedBy: %q want bytes", tr.TruncatedBy)
	}
	if tr.OutputBytes > 5000 {
		t.Errorf("outputBytes %d exceeds maxBytes 5000", tr.OutputBytes)
	}
}

func TestTruncateTail_LastLineLargerThanCap(t *testing.T) {
	// Single line of 10000 bytes, cap at 1000.
	in := strings.Repeat("x", 10000)
	tr := TruncateTail(in, 1000, 100)
	if !tr.Truncated {
		t.Fatalf("expected truncation")
	}
	if !tr.LastLinePartial {
		t.Errorf("LastLinePartial must be true on single-line-bigger-than-cap")
	}
	if tr.OutputBytes > 1000 {
		t.Errorf("outputBytes %d > 1000", tr.OutputBytes)
	}
}

func TestFormatSize(t *testing.T) {
	cases := []struct {
		n    int
		want string
	}{
		{0, "0B"},
		{500, "500B"},
		{1024, "1.0KB"},
		{50 * 1024, "50.0KB"},
		{1024 * 1024, "1.0MB"},
	}
	for _, tc := range cases {
		if got := FormatSize(tc.n); got != tc.want {
			t.Errorf("FormatSize(%d) = %q want %q", tc.n, got, tc.want)
		}
	}
}
