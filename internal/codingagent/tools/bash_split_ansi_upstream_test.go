package tools

// pi: packages/coding-agent/src/core/bash-executor.ts

import (
	"context"
	"strings"
	"testing"
)

type chunkOperations struct {
	chunks     [][]byte
	beforeExit func(streamed string)
	streamed   *strings.Builder
}

func (o chunkOperations) Exec(_ context.Context, _, _ string, opts BashOperationsExecOptions) (BashOperationsResult, error) {
	for _, chunk := range o.chunks {
		opts.OnData(chunk)
	}
	if o.beforeExit != nil {
		o.beforeExit(o.streamed.String())
	}
	return BashOperationsResult{ExitCode: new(0)}, nil
}

// Ports packages/coding-agent/test/suite/agent-session-bash-persistence.test.ts:341-401 ("escape sequences split across output
// chunks", regression tests for earendil-works/pi#10504) at the executor that `!` commands and RPC bash share
// (core/bash-executor.ts:74-118 pendingAnsi / splitIncompleteAnsiSuffix).
func TestBashExecutorStripsEscapeSequencesSplitAcrossChunks(t *testing.T) {
	run := func(t *testing.T, beforeExit func(string), chunks ...[]byte) (output, streamed string) {
		t.Helper()
		var deltas strings.Builder
		result, err := ExecuteBashWithOperations(context.Background(), "custom", t.TempDir(),
			chunkOperations{chunks: chunks, beforeExit: beforeExit, streamed: &deltas},
			BashExecOptions{OnChunk: func(chunk string) { deltas.WriteString(chunk) }})
		if err != nil {
			t.Fatal(err)
		}
		return result.Output, deltas.String()
	}
	t.Run("a color reset split inside its parameters", func(t *testing.T) {
		output, streamed := run(t, nil, []byte("\x1b[31mERROR: file.py:1\x1b[0"), []byte("m\n"))
		if output != "ERROR: file.py:1\n" || streamed != "ERROR: file.py:1\n" {
			t.Fatalf("output %q streamed %q", output, streamed)
		}
	})
	t.Run("a color code split right after ESC", func(t *testing.T) {
		output, streamed := run(t, nil, []byte("before\x1b"), []byte("[32mafter\n"))
		if output != "beforeafter\n" || streamed != "beforeafter\n" {
			t.Fatalf("output %q streamed %q", output, streamed)
		}
	})
	t.Run("an OSC sequence split before its terminator", func(t *testing.T) {
		output, _ := run(t, nil, []byte("a\x1b]0;window "), []byte("title\x1b"), []byte("\\b\n"))
		if output != "ab\n" {
			t.Fatalf("output %q", output)
		}
	})
	t.Run("an incomplete multi-byte character at the end of output", func(t *testing.T) {
		output, streamed := run(t, nil, []byte("ok"), []byte("\u00e9")[:1])
		if output != "ok\uFFFD" || streamed != "ok\uFFFD" {
			t.Fatalf("output %q streamed %q", output, streamed)
		}
	})
	t.Run("a long unterminated sequence is not held back", func(t *testing.T) {
		long := strings.Repeat("x", 300)
		var before string
		run(t, func(streamed string) { before = streamed }, []byte("\x1b]"+long))
		if before != "]"+long {
			t.Fatalf("streamed before exit = %q", before)
		}
	})
}

// utils/ansi.ts splitIncompleteAnsiSuffix: the unfinished OSC or CSI sequence at the end is split off; a complete one, none, or one
// outside the 256-unit window is not.
func TestSplitIncompleteAnsiSuffixMatchesPi(t *testing.T) {
	for _, tc := range []struct{ in, complete, pending string }{
		{"plain", "plain", ""},
		{"a\x1b", "a", "\x1b"},
		{"a\x1b[", "a", "\x1b["},
		{"a\x1b[31", "a", "\x1b[31"},
		{"a\x1b[31m", "a\x1b[31m", ""},
		{"a\x1b]0;title", "a", "\x1b]0;title"},
		{"a\x1b]0;title\x07", "a\x1b]0;title\x07", ""},
		{"a\x1b]0;title\x1b", "a", "\x1b]0;title\x1b"},
		{"a\x1b]0;title\x1b\\", "a\x1b]0;title\x1b\\", ""},
		{"a\u009b31", "a", "\u009b31"},
		{"\x1b]" + strings.Repeat("x", 300), "\x1b]" + strings.Repeat("x", 300), ""},
		{"", "", ""},
		{"a\x1b]0;t\u009cb", "a\x1b]0;t\u009cb", ""},
		{"a\x1b]0;ti\x1btle", "a", "\x1b]0;ti\x1btle"},
		{"a\x1b]0;t\x1b\x1b\\", "a\x1b]0;t\x1b\x1b\\", ""},
		{"head\x1b]" + strings.Repeat("y", 200), "head", "\x1b]" + strings.Repeat("y", 200)},
		{"\x1b[1mA\x1b[2", "\x1b[1mA", "\x1b[2"},
		// The window counts UTF-16 code units: an escape starting 256 units from the end is held, one starting 258 is not.
		{"a\x1b]" + strings.Repeat("\U0001F600", 127), "a", "\x1b]" + strings.Repeat("\U0001F600", 127)},
		{"a\x1b]" + strings.Repeat("\U0001F600", 128), "a\x1b]" + strings.Repeat("\U0001F600", 128), ""},
	} {
		if complete, pending := SplitIncompleteAnsiSuffix(tc.in); complete != tc.complete || pending != tc.pending {
			t.Errorf("SplitIncompleteAnsiSuffix(%q) = %q, %q; want %q, %q", tc.in, complete, pending, tc.complete, tc.pending)
		}
	}
}

// bash-executor.ts appendText returns early for empty text, so a chunk that is only the start of an escape sequence reaches
// neither the callback nor the buffers.
func TestBashExecutorSkipsEmptyChunks(t *testing.T) {
	var chunks []string
	result, err := ExecuteBashWithOperations(context.Background(), "custom", t.TempDir(),
		chunkOperations{chunks: [][]byte{[]byte("\x1b[3"), []byte("1m")}, streamed: &strings.Builder{}},
		BashExecOptions{OnChunk: func(chunk string) { chunks = append(chunks, chunk) }})
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks) != 0 || result.Output != "" {
		t.Fatalf("chunks %q, output %q; want none", chunks, result.Output)
	}
}
