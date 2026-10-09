package coding

import (
	"context"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// runBashChunks ports the runChunks helper of upstream
// packages/coding-agent/test/suite/agent-session-bash-persistence.test.ts
// ("escape sequences split across output chunks", #10504): a user bash command
// whose operations emit the given chunks. beforeExit sees what was streamed by
// the time the command exits.
func runBashChunks(t *testing.T, chunks [][]byte, beforeExit func(streamed string)) (output, streamed, recorded string) {
	t.Helper()
	h := newRecoveryHarness(t, harnessOptions{emptySessionManager: true})
	var deltas []string
	operations := bashPersistenceOperations(func(_ context.Context, _, _ string, options extension.BashOperationsExecOptions) (extension.BashOperationsResult, error) {
		for _, chunk := range chunks {
			options.OnData(chunk)
		}
		if beforeExit != nil {
			beforeExit(strings.Join(deltas, ""))
		}
		return extension.BashOperationsResult{ExitCode: new(0)}, nil
	})
	result, err := h.session.ExecuteBash(t.Context(), "custom", func(delta string) { deltas = append(deltas, delta) }, &ExecuteBashOptions{Operations: operations})
	if err != nil {
		t.Fatal(err)
	}
	messages := h.session.Messages()
	last := messages[len(messages)-1]
	if last.Role() != agent.RoleBashExecution {
		t.Fatalf("last message = %s, want bashExecution", last.Role())
	}
	recorded, _ = last.Custom["output"].(string)
	return result.Output, strings.Join(deltas, ""), recorded
}

func stringChunks(chunks ...string) [][]byte {
	out := make([][]byte, len(chunks))
	for i, chunk := range chunks {
		out[i] = []byte(chunk)
	}
	return out
}

func TestUpstreamBashEscapeSequencesSplitAcrossOutputChunks(t *testing.T) {
	// upstream: agent-session-bash-persistence.test.ts "strips a color reset split inside its parameters"
	t.Run("strips a color reset split inside its parameters", func(t *testing.T) {
		output, streamed, recorded := runBashChunks(t, stringChunks("\x1b[31mERROR: file.py:1\x1b[0", "m\n"), nil)
		for name, got := range map[string]string{"output": output, "streamed": streamed, "recorded": recorded} {
			if got != "ERROR: file.py:1\n" {
				t.Errorf("%s = %q, want %q", name, got, "ERROR: file.py:1\n")
			}
		}
	})
	// upstream: "strips a color code split right after ESC"
	t.Run("strips a color code split right after ESC", func(t *testing.T) {
		output, streamed, _ := runBashChunks(t, stringChunks("before\x1b", "[32mafter\n"), nil)
		if output != "beforeafter\n" || streamed != "beforeafter\n" {
			t.Fatalf("output = %q, streamed = %q, want %q", output, streamed, "beforeafter\n")
		}
	})
	// upstream: "strips an OSC sequence split before its terminator"
	t.Run("strips an OSC sequence split before its terminator", func(t *testing.T) {
		output, _, _ := runBashChunks(t, stringChunks("a\x1b]0;window ", "title\x1b", "\\b\n"), nil)
		if output != "ab\n" {
			t.Fatalf("output = %q, want %q", output, "ab\n")
		}
	})
	// upstream: "flushes an incomplete multi-byte character at the end of output"
	t.Run("flushes an incomplete multi-byte character at the end of output", func(t *testing.T) {
		output, streamed, _ := runBashChunks(t, [][]byte{[]byte("ok"), []byte("\u00e9")[:1]}, nil)
		if output != "ok\uFFFD" || streamed != "ok\uFFFD" {
			t.Fatalf("output = %q, streamed = %q, want %q", output, streamed, "ok\uFFFD")
		}
	})
	// upstream: "does not hold back output behind a long unterminated sequence"
	t.Run("does not hold back output behind a long unterminated sequence", func(t *testing.T) {
		long := strings.Repeat("x", 300)
		streamedBeforeExit := ""
		runBashChunks(t, stringChunks("\x1b]"+long), func(streamed string) { streamedBeforeExit = streamed })
		if streamedBeforeExit != "]"+long {
			t.Fatalf("streamed before exit = %q, want %q", streamedBeforeExit, "]"+long)
		}
	})
}
