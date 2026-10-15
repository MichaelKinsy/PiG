package tools

import (
	"context"
	"slices"
	"strings"
	"testing"
)

// Mirrors pi 1.1.0 utils/ansi.ts splitIncompleteAnsiSuffix (#10504).
func TestSplitIncompleteAnsiSuffix(t *testing.T) {
	long := strings.Repeat("x", 300)
	for _, tc := range []struct {
		name, in, complete, pending string
	}{
		{"plain text", "hello", "hello", ""},
		{"complete SGR", "a\x1b[31mb", "a\x1b[31mb", ""},
		{"lone ESC", "a\x1b", "a", "\x1b"},
		{"ESC [", "a\x1b[", "a", "\x1b["},
		{"CSI with unfinished parameters", "a\x1b[0", "a", "\x1b[0"},
		{"CSI with separators", "a\x1b[38;5;", "a", "\x1b[38;5;"},
		{"8-bit CSI", "a\u009b3", "a", "\u009b3"},
		{"OSC without terminator", "a\x1b]0;title", "a", "\x1b]0;title"},
		{"OSC with a trailing ESC", "a\x1b]0;title\x1b", "a", "\x1b]0;title\x1b"},
		{"OSC closed by BEL", "a\x1b]0;title\x07", "a\x1b]0;title\x07", ""},
		{"OSC closed by ESC backslash", "a\x1b]0;title\x1b\\", "a\x1b]0;title\x1b\\", ""},
		{"OSC closed by 8-bit ST", "a\x1b]0;title\u009c", "a\x1b]0;title\u009c", ""},
		{"complete sequence before an unfinished one", "\x1b[0mab\x1b[3", "\x1b[0mab", "\x1b[3"},
		{"unfinished sequence beyond the window is processed as is", "\x1b]" + long, "\x1b]" + long, ""},
		{"unfinished sequence inside the window is held", "\x1b]" + long[:200], "", "\x1b]" + long[:200]},
		{"OSC with ESC ESC backslash is terminated", "a\x1b]x\x1b\x1b\\", "a\x1b]x\x1b\x1b\\", ""},
		{"CSI with many parameters", "a\x1b[1;2;3;4;5", "a", "\x1b[1;2;3;4;5"},
		{"charset designator", "a\x1b(", "a", "\x1b("},
		{"ESC #", "a\x1b#", "a", "\x1b#"},
		{"window counts UTF-16 code units", "\x1b[" + strings.Repeat("\U0001F600", 130), "\x1b[" + strings.Repeat("\U0001F600", 130), ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			complete, pending := SplitIncompleteAnsiSuffix(tc.in)
			if complete != tc.complete || pending != tc.pending {
				t.Fatalf("SplitIncompleteAnsiSuffix(%q) = (%q, %q), want (%q, %q)", tc.in, complete, pending, tc.complete, tc.pending)
			}
		})
	}
}

type chunkedBashOperations [][]byte

func (c chunkedBashOperations) Exec(_ context.Context, _, _ string, opts BashOperationsExecOptions) (BashOperationsResult, error) {
	for _, chunk := range c {
		opts.OnData(chunk)
	}
	return BashOperationsResult{}, nil
}

// pi 1.1.0 bash-executor.ts flushOutput: an escape sequence still pending when the command exits is flushed through the same
// sanitizer, and chunks that sanitize to nothing reach neither the output nor onChunk.
func TestExecuteBashWithOperationsFlushesPendingANSIAtExit(t *testing.T) {
	for _, tc := range []struct {
		name       string
		input      [][]byte
		wantChunks []string
		wantOutput string
	}{
		// Pi's ansiRegex consumes "ESC ]8;;h" as a CSI sequence ending in "h" (probed with utils/ansi.ts stripAnsi), so the rest of an unterminated OSC survives.
		{"an unterminated OSC keeps its text", [][]byte{[]byte("ok"), []byte("\x1b]8;;http://x")}, []string{"ok", "ttp://x"}, "okttp://x"},
		{"a pending sequence that strips to nothing is not emitted", [][]byte{[]byte("ok"), []byte("\x1b[0")}, []string{"ok"}, "ok"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var chunks []string
			result, err := ExecuteBashWithOperations(t.Context(), "custom", t.TempDir(), chunkedBashOperations(tc.input), BashExecOptions{OnChunk: func(chunk string) { chunks = append(chunks, chunk) }})
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(chunks, tc.wantChunks) || result.Output != tc.wantOutput {
				t.Fatalf("chunks=%q output=%q, want chunks %q output %q", chunks, result.Output, tc.wantChunks, tc.wantOutput)
			}
		})
	}
}
