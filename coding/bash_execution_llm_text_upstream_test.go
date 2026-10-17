package coding

import (
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// Ports bashExecutionToText through convertToLlm (packages/coding-agent/src/core/messages.ts:90-108, 148-200): a recorded user-bash result reaches the next model turn as one user message with the command, the fenced output, and the exit status, cancellation, and truncation notes. This drives the live Session path, so the exit code travels from BashResult through the persisted entry to the LLM message.
func TestRecordedBashResultReachesModelContextUpstream(t *testing.T) {
	exit := func(code int) *int { return &code }
	cases := []struct {
		name    string
		result  BashResult
		exclude bool
		want    string
	}{
		{"nonzero exit code", BashResult{Output: "boom", ExitCode: exit(3)}, false, "Ran `cmd`\n```\nboom\n```\n\nCommand exited with code 3"},
		{"zero exit code adds no note", BashResult{Output: "ok", ExitCode: exit(0)}, false, "Ran `cmd`\n```\nok\n```"},
		{"no output", BashResult{ExitCode: exit(0)}, false, "Ran `cmd`\n(no output)"},
		{"cancelled outranks the exit code", BashResult{Output: "x", ExitCode: exit(130), Cancelled: true}, false, "Ran `cmd`\n```\nx\n```\n\n(command cancelled)"},
		{"truncated output names the full output file", BashResult{Output: "tail", ExitCode: exit(1), Truncated: true, FullOutputPath: "/tmp/full.log"}, false, "Ran `cmd`\n```\ntail\n```\n\nCommand exited with code 1\n\n[Output truncated. Full output: /tmp/full.log]"},
		{"truncated without a path adds nothing", BashResult{Output: "tail", ExitCode: exit(0), Truncated: true}, false, "Ran `cmd`\n```\ntail\n```"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newRecoveryHarness(t, harnessOptions{}, fauxReply("done", ai.StopReasonStop, 0))
			if err := h.session.RecordBashResult("cmd", tc.result, &tc.exclude); err != nil {
				t.Fatal(err)
			}
			var got []string
			for _, message := range agent.ConvertToLLM(h.session.Messages()) {
				if user, ok := message.(ai.UserMessage); ok {
					if blocks, ok := user.Content.(ai.UserContentBlocks); ok {
						for _, block := range blocks {
							if text, ok := block.(ai.TextContent); ok {
								got = append(got, text.Text)
							}
						}
					}
				}
			}
			if len(got) != 1 || got[0] != tc.want {
				t.Errorf("model context = %q, want [%q]", got, tc.want)
			}
		})
	}

	t.Run("excluded from context", func(t *testing.T) {
		h := newRecoveryHarness(t, harnessOptions{}, fauxReply("done", ai.StopReasonStop, 0))
		if err := h.session.RecordBashResult("cmd", BashResult{Output: "secret", ExitCode: exit(0)}, new(true)); err != nil {
			t.Fatal(err)
		}
		if got := agent.ConvertToLLM(h.session.Messages()); len(got) != 0 {
			t.Errorf("an excluded bash result reached the model: %+v", got)
		}
	})
}
