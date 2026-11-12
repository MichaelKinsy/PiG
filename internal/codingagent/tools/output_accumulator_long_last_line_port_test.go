package tools

// pi: packages/coding-agent/src/core/tools/output-accumulator.ts
// Pi 935f0ac0ccfc (#12092, fixes #12062), post-v1.5.2: backported in 0.8.0.

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"
)

func chunksOf(s string, size int) [][]byte {
	var chunks [][]byte
	for i := 0; i < len(s); i += size {
		chunks = append(chunks, []byte(s[i:min(i+size, len(s))]))
	}
	return chunks
}

// test/tools.test.ts "should show the end of an over-long last line after a short line": a short line followed by one line far past the limit returned no output at all.
func TestBashToolShowsTheEndOfAnOverLongLastLineAfterAShortLine(t *testing.T) {
	longLine := strings.Repeat("y", 219_000) + "END"
	tool := CreateBashTool(t.TempDir(), &BashToolOptions{
		Operations: portBashOperations(func(_ context.Context, _, _ string, opts BashOperationsExecOptions) (BashOperationsResult, error) {
			for _, chunk := range chunksOf("first line\n"+longLine+"\n", 8192) {
				opts.OnData(chunk)
			}
			return BashOperationsResult{ExitCode: new(0)}, nil
		}),
	})
	result, err := tool.Execute(context.Background(), "test-call-long-last-line", json.RawMessage(`{"command":"grep -n x bundle.min.js"}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	details, ok := result.Details.(*BashDetails)
	if !ok || details.Truncation == nil || !details.Truncation.LastLinePartial {
		t.Fatalf("details = %+v, want a partial last line", result.Details)
	}
	output := result.Text()
	if !strings.Contains(output, "yyyyEND") {
		t.Fatalf("output lacks the end of the long line: %.80q", output)
	}
	if !regexp.MustCompile(`\[Showing last 50\.0KB of line 2 `).MatchString(output) {
		t.Fatalf("output lacks the partial-line notice: %.200q", output[max(0, len(output)-200):])
	}
}

// The accumulator keeps the end of the last line whenever that line alone fills the rolling tail, for every shape of output Pi's snapshot clips.
func TestOutputAccumulatorKeepsTheEndOfAnOverLongLastLine(t *testing.T) {
	const limit = 51200
	fillerLines := strings.Repeat("filler line\n", 20_000)
	long := func(n int) string { return strings.Repeat("y", n-3) + "END" }
	cases := []struct {
		name    string
		output  string
		line    int    // the line number the notice names
		wantEnd string // the content ends with this
	}{
		{"short line then one over-long line", "first line\n" + long(219_003) + "\n", 2, "yyyyEND"},
		{"one over-long line alone", long(219_003) + "\n", 1, "yyyyEND"},
		{"no trailing newline", "first line\n" + long(219_003), 2, "yyyyEND"},
		{"CRLF", "first line\r\n" + long(219_003) + "\r\n", 2, "yyyyEND"},
		{"several over-long lines", long(150_003) + "\n" + long(150_003) + "\n" + long(150_003) + "\n", 3, "yyyyEND"},
		{"multi-byte characters at the cut", "first line\n" + strings.Repeat("€", 70_000) + "END\n", 2, "€€€END"},
		{"4-byte characters at the cut", "first line\n" + strings.Repeat("😀", 55_000) + "END\n", 2, "😀😀END"},
		{"last line one byte short of the rolling tail", fillerLines + long(102_399) + "\n", 20_001, "yyyyEND"},
		{"last line exactly the rolling tail", fillerLines + long(102_400) + "\n", 20_001, "yyyyEND"},
		{"last line one byte past the rolling tail", fillerLines + long(102_401) + "\n", 20_001, "yyyyEND"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			output := tc.output
			acc := NewOutputAccumulator("pi-test")
			for _, chunk := range chunksOf(output, 8192) {
				if err := acc.Append(chunk); err != nil {
					t.Fatal(err)
				}
			}
			acc.Finish()
			snap := acc.Snapshot(false)
			defer func() { _ = acc.CloseTempFile() }()
			tr := snap.Truncation
			if !tr.Truncated || !tr.LastLinePartial || snap.Content == "" || tr.OutputLines != 1 {
				t.Fatalf("truncation = %+v, content len %d; want the partial end of the last line", tr, len(snap.Content))
			}
			if !strings.Contains(snap.Content, tc.wantEnd) || !utf8.ValidString(snap.Content) || len(snap.Content) > limit || tr.OutputBytes != len(snap.Content) {
				t.Fatalf("content len %d valid=%v outputBytes=%d, want <=%d bytes ending in %q", len(snap.Content), utf8.ValidString(snap.Content), tr.OutputBytes, limit, tc.wantEnd)
			}
			if tr.TotalLines != tc.line {
				t.Fatalf("TotalLines = %d, want %d", tr.TotalLines, tc.line)
			}
		})
	}
}
