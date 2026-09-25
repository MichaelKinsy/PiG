package tools

import (
	"strings"
	"testing"
)

// TruncateHead + FormatTruncationWarning parity tests.
//
// Mirrors upstream's behavior in
// `.upstream/current/packages/coding-agent/src/core/tools/truncate.ts:67-149`
// (TruncateHead) and the warning strings that consumers
// (`read.ts:108-116`) emit.

func TestTruncateHeadNotTruncated(t *testing.T) {
	in := "a\nb\nc"
	tr := TruncateHead(in, 1024, 100)
	if tr.Truncated {
		t.Errorf("not-truncated input flagged Truncated: %+v", tr)
	}
	if tr.Content != in {
		t.Errorf("content modified: got %q want %q", tr.Content, in)
	}
	if got := FormatTruncationWarning(tr); got != "" {
		t.Errorf("not-truncated warning: got %q want empty", got)
	}
}

func TestTruncateHeadLineCapBeforeByteCap(t *testing.T) {
	// Build 10 short lines (well under any byte cap) and request
	// maxLines=3 so the line cap fires before the byte cap.
	in := strings.Repeat("xx\n", 10)
	in = strings.TrimRight(in, "\n") // 10 lines, ~30 bytes
	tr := TruncateHead(in, 10000, 3)
	if !tr.Truncated || tr.TruncatedBy != "lines" {
		t.Errorf("expected Truncated=true, TruncatedBy=lines; got %+v", tr)
	}
	if tr.OutputLines != 3 {
		t.Errorf("OutputLines=%d want 3", tr.OutputLines)
	}
	if tr.TotalLines != 10 {
		t.Errorf("TotalLines=%d want 10", tr.TotalLines)
	}
	want := "[Truncated: showing 3 of 10 lines (3 line limit)]"
	if got := FormatTruncationWarning(tr); got != want {
		t.Errorf("warning: got %q\nwant %q", got, want)
	}
}

func TestTruncateHeadByteCapBeforeLineCap(t *testing.T) {
	// 50 lines of "xxxxxxxxx" (10 bytes) → ~500 bytes total.
	// maxBytes=30 should cut off after ~3 lines (10+1+10+1+10 = 32 hits the cap).
	in := strings.Repeat("xxxxxxxxx\n", 50)
	in = strings.TrimRight(in, "\n")
	tr := TruncateHead(in, 30, 100)
	if !tr.Truncated || tr.TruncatedBy != "bytes" {
		t.Errorf("expected Truncated=true, TruncatedBy=bytes; got %+v", tr)
	}
	if tr.FirstLineExceedsLimit {
		t.Errorf("FirstLineExceedsLimit unexpectedly true (first line is 10B, cap is 30B)")
	}
	got := FormatTruncationWarning(tr)
	// Format: [Truncated: N lines shown (SIZE limit)]
	if !strings.HasPrefix(got, "[Truncated: ") || !strings.HasSuffix(got, " limit)]") {
		t.Errorf("warning format: got %q want `[Truncated: N lines shown (SIZE limit)]`", got)
	}
	if !strings.Contains(got, "lines shown") {
		t.Errorf("warning should say `lines shown` (bytes case): got %q", got)
	}
}

func TestTruncateHeadFirstLineExceedsLimit(t *testing.T) {
	// Single line of 100 bytes, cap at 50.
	in := strings.Repeat("y", 100)
	tr := TruncateHead(in, 50, 100)
	if !tr.Truncated || !tr.FirstLineExceedsLimit {
		t.Errorf("expected FirstLineExceedsLimit=true; got %+v", tr)
	}
	if tr.Content != "" {
		t.Errorf("first-line-exceeds: content should be empty, got %q", tr.Content)
	}
	want := "[First line exceeds 50B limit]"
	if got := FormatTruncationWarning(tr); got != want {
		t.Errorf("warning: got %q want %q", got, want)
	}
}

// TestTruncateHeadExactSuffixMatchesUpstream is the regression
// guard. The exact warning strings come from upstream's
// `read.ts:108-116` (lines / bytes cases) and the same `[Truncated:`
// prefix is shared with `bash.ts:247-263`. If upstream changes the
// format, this test fires and the sync agent sees the diff.
func TestTruncateHeadExactSuffixMatchesUpstream(t *testing.T) {
	cases := []struct {
		name     string
		in       string
		maxBytes int
		maxLines int
		want     string
	}{
		{
			name:     "lines case: exact upstream format",
			in:       strings.Repeat("a\n", 100),
			maxBytes: 100000,
			maxLines: 5,
			// 100 lines (trailing \n is a terminator, not an extra line).
			want: "[Truncated: showing 5 of 100 lines (5 line limit)]",
		},
		{
			name:     "bytes case: exact upstream format",
			in:       strings.Repeat("aaaa\n", 200), // 1000 bytes
			maxBytes: 50,
			maxLines: 100000,
			// Output count varies by exact layout; just assert structure.
			want: "", // checked below by prefix/suffix
		},
		{
			name:     "first-line-exceeds: exact upstream format",
			in:       strings.Repeat("z", 200),
			maxBytes: 100,
			maxLines: 1000,
			want:     "[First line exceeds 100B limit]",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tr := TruncateHead(tc.in, tc.maxBytes, tc.maxLines)
			got := FormatTruncationWarning(tr)
			if tc.want != "" {
				if got != tc.want {
					t.Errorf("got %q\nwant %q", got, tc.want)
				}
			} else {
				if !strings.HasPrefix(got, "[Truncated: ") || !strings.Contains(got, " lines shown (") || !strings.HasSuffix(got, " limit)]") {
					t.Errorf("bytes-case warning shape mismatch: got %q", got)
				}
			}
		})
	}
}

// TOOL-20: a zero limit is a limit, as upstream's `??` keeps an explicit 0.
func TestTruncateZeroLimitsAreLimits(t *testing.T) {
	if tr := TruncateHead("abc\ndef", 0, 10); !tr.Truncated || tr.Content != "" || !tr.FirstLineExceedsLimit {
		t.Errorf("TruncateHead maxBytes 0 = %+v", tr)
	}
	if tr := TruncateHead("abc\ndef", 1024, 0); !tr.Truncated || tr.Content != "" || tr.TruncatedBy != "lines" {
		t.Errorf("TruncateHead maxLines 0 = %+v", tr)
	}
	if tr := TruncateTail("abc\ndef", 1024, 0); !tr.Truncated || tr.Content != "" {
		t.Errorf("TruncateTail maxLines 0 = %+v", tr)
	}
}
