package tools

// pi: packages/coding-agent/test/tools.test.ts
// Pi 9e9609216465 (#12355, fixes #12350): bash shows the end of an over-long
// line followed by a blank line instead of "(no output)".

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/agent"
)

// test/tools.test.ts "should show the end of an over-long line followed by a blank line (%i bytes, %j)"
func TestBashToolShowsTheEndOfAnOverLongLineFollowedByABlankLine(t *testing.T) {
	cases := []struct {
		length int
		eol    string
	}{
		{60_000, "\n"},
		{219_000, "\n"},
		{219_000, "\r\n"},
	}
	contentRe := regexp.MustCompile(`^y+END\r?\n`)
	noticeRe := regexp.MustCompile(`\[Showing last 50\.0KB of line 2 `)
	for _, tc := range cases {
		name := fmt.Sprintf("%d_bytes_%q", tc.length, tc.eol)
		t.Run(name, func(t *testing.T) {
			longLine := strings.Repeat("y", tc.length) + "END"
			data := "first line" + tc.eol + longLine + tc.eol + tc.eol
			tool := CreateBashTool(t.TempDir(), &BashToolOptions{
				Operations: portBashOperations(func(_ context.Context, _, _ string, opts BashOperationsExecOptions) (BashOperationsResult, error) {
					for _, chunk := range chunksOf(data, 8192) {
						opts.OnData(chunk)
					}
					return BashOperationsResult{ExitCode: new(0)}, nil
				}),
			})
			result, err := tool.Execute(context.Background(), "test-call-long-line-blank-line", json.RawMessage(`{"command":"long-line"}`), nil)
			if err != nil {
				t.Fatal(err)
			}
			details, ok := result.Details.(*BashDetails)
			if !ok || details.Truncation == nil || !details.Truncation.LastLinePartial {
				t.Fatalf("details = %+v, want lastLinePartial=true", result.Details)
			}
			output := result.Text()
			if !contentRe.MatchString(output) {
				t.Fatalf("output does not start with y+END\\r?\\n: %.80q", output)
			}
			if !noticeRe.MatchString(output) {
				t.Fatalf("output lacks partial-line notice for line 2: %.200q", output[max(0, len(output)-200):])
			}
		})
	}
}

// Covers both the streaming onUpdate path and final-result path, boundaries at
// 50KB and 50KB+1, multi-byte UTF-8 at the cut, multiple/whitespace blank lines,
// and a stress input.
func TestBashToolOverLongLineFollowedByBlankLineBoundariesAndStreaming(t *testing.T) {
	t.Run("streaming onUpdate matches final tail", func(t *testing.T) {
		longLine := strings.Repeat("y", 219_000) + "END"
		data := "first line\n" + longLine + "\n\n"
		var updates []agent.AgentToolResult
		tool := CreateBashTool(t.TempDir(), &BashToolOptions{
			Operations: portBashOperations(func(_ context.Context, _, _ string, opts BashOperationsExecOptions) (BashOperationsResult, error) {
				for _, chunk := range chunksOf(data, 8192) {
					opts.OnData(chunk)
				}
				return BashOperationsResult{ExitCode: new(0)}, nil
			}),
		})
		result, err := tool.Execute(context.Background(), "test-call-streaming", json.RawMessage(`{"command":"long-line"}`), func(partial agent.AgentToolResult) {
			updates = append(updates, partial)
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(updates) == 0 {
			t.Fatalf("expected streaming updates")
		}
		lastUpdate := updates[len(updates)-1]
		updateDetails, ok := lastUpdate.Details.(*BashDetails)
		if !ok || updateDetails.Truncation == nil || !updateDetails.Truncation.LastLinePartial {
			t.Fatalf("last update details = %+v, want lastLinePartial=true", lastUpdate.Details)
		}
		if !strings.Contains(lastUpdate.Text(), "yyyyEND\n") {
			t.Fatalf("last streaming update lacks yyyyEND\\n: %.80q", lastUpdate.Text())
		}
		if !strings.Contains(result.Text(), "yyyyEND\n") {
			t.Fatalf("final result lacks yyyyEND\\n: %.80q", result.Text())
		}
	})

	t.Run("exact 50KB and 50KB+1 boundaries with trailing blank line", func(t *testing.T) {
		const limit = DefaultMaxBytesUpstream
		// Exactly 50KB total including trailing \n\n: not truncated.
		exactLine := strings.Repeat("a", limit-2)
		trExact := TruncateTail(exactLine+"\n\n", TruncationOptions{})
		if trExact.Truncated || trExact.LastLinePartial || trExact.Content != exactLine+"\n\n" {
			t.Fatalf("exact 50KB = %+v, want not truncated", trExact)
		}

		// 50KB+1 total (line is limit-1 bytes, plus \n\n = limit+1 bytes): totalBytes exceeds limit so Truncated=true,
		// while the counted content (line + "\n" + "") is exactly limit bytes so the whole line fits without partial cut.
		fitLine := strings.Repeat("a", limit-4) + "END"
		trFit := TruncateTail(fitLine+"\n\n", TruncationOptions{})
		if !trFit.Truncated || trFit.LastLinePartial || trFit.OutputBytes != limit || trFit.OutputLines != 2 {
			t.Fatalf("50KB+1 (counted=50KB) = %+v, want truncated=true lastLinePartial=false outputBytes=%d outputLines=2", trFit, limit)
		}

		// Line is limit bytes (plus \n\n = limit+2 bytes): line + joining "\n" = limit+1 > limit, so partial cut occurs.
		overLine := strings.Repeat("a", limit-3) + "END"
		trOver := TruncateTail(overLine+"\n\n", TruncationOptions{})
		if !trOver.Truncated || !trOver.LastLinePartial || trOver.TruncatedBy != "bytes" {
			t.Fatalf("50KB line + blank = %+v, want truncated by bytes with lastLinePartial=true", trOver)
		}
		if trOver.OutputBytes != limit || trOver.OutputLines != 2 {
			t.Fatalf("50KB+1 outputBytes=%d outputLines=%d, want %d and 2", trOver.OutputBytes, trOver.OutputLines, limit)
		}
		if !strings.HasSuffix(trOver.Content, "END\n") {
			t.Fatalf("50KB+1 content suffix = %q, want END\\n", trOver.Content[max(0, len(trOver.Content)-10):])
		}
	})

	t.Run("multi-byte UTF-8 at cut with trailing blank line", func(t *testing.T) {
		// 3-byte euro and 4-byte emoji over-long lines followed by blank line.
		for _, tc := range []struct {
			name string
			ch   string
			n    int
		}{
			{"3-byte euro", "€", 20_000},
			{"4-byte emoji", "😀", 15_000},
		} {
			t.Run(tc.name, func(t *testing.T) {
				input := "head\n" + strings.Repeat(tc.ch, tc.n) + "END\n\n"
				tr := TruncateTail(input, TruncationOptions{})
				if !tr.Truncated || !tr.LastLinePartial {
					t.Fatalf("%s: got %+v, want truncated + lastLinePartial", tc.name, tr)
				}
				if !utf8.ValidString(tr.Content) {
					t.Fatalf("%s: invalid UTF-8 in truncated content", tc.name)
				}
				if tr.OutputBytes > DefaultMaxBytesUpstream || tr.OutputBytes != len(tr.Content) {
					t.Fatalf("%s: outputBytes=%d len=%d limit=%d", tc.name, tr.OutputBytes, len(tr.Content), DefaultMaxBytesUpstream)
				}
				if !strings.HasSuffix(tr.Content, tc.ch+"END\n") {
					t.Fatalf("%s: unexpected suffix %q", tc.name, tr.Content[max(0, len(tr.Content)-16):])
				}
			})
		}
	})

	t.Run("multiple and whitespace blank lines and stress input", func(t *testing.T) {
		// Whitespace-only trailing lines (spaces, tabs, CR, NBSP U+00A0).
		input := "first\n" + strings.Repeat("x", 60_000) + "END\n  \t \n\u00a0\n"
		tr := TruncateTail(input, TruncationOptions{})
		if !tr.Truncated || !tr.LastLinePartial || tr.OutputLines != 3 {
			t.Fatalf("whitespace blank lines = %+v, want lastLinePartial=true, outputLines=3", tr)
		}
		if !strings.Contains(tr.Content, "xxxEND\n  \t \n\u00a0") {
			t.Fatalf("whitespace blank lines content suffix = %q", tr.Content[max(0, len(tr.Content)-30):])
		}

		// Stress input: >1 MiB single line followed by blank lines through OutputAccumulator.
		acc := NewOutputAccumulator("pi-stress")
		defer func() { _ = acc.CloseTempFile() }()
		if err := acc.Append([]byte("header\n")); err != nil {
			t.Fatal(err)
		}
		chunk := []byte(strings.Repeat("z", 8192))
		for range 130 {
			if err := acc.Append(chunk); err != nil {
				t.Fatal(err)
			}
		}
		if err := acc.Append([]byte("TAIL\n\n")); err != nil {
			t.Fatal(err)
		}
		acc.Finish()
		snap := acc.Snapshot(false)
		if !snap.Truncation.Truncated || !snap.Truncation.LastLinePartial {
			t.Fatalf("stress snapshot = %+v, want lastLinePartial=true", snap.Truncation)
		}
		if !strings.HasSuffix(snap.Content, "TAIL\n") || snap.Truncation.OutputBytes > DefaultMaxBytesUpstream {
			t.Fatalf("stress snapshot content suffix = %q, outputBytes = %d", snap.Content[max(0, len(snap.Content)-20):], snap.Truncation.OutputBytes)
		}
	})
}
