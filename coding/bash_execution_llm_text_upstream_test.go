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
		{"truncated without a path still says output is truncated", BashResult{Output: "tail", ExitCode: exit(0), Truncated: true}, false, "Ran `cmd`\n```\ntail\n```\n\n[Output truncated]"},
		{"truncated with fullOutputError explains why saving failed", BashResult{Output: "tail", ExitCode: exit(0), Truncated: true, FullOutputError: "ENOSPC: no space left on device"}, false, "Ran `cmd`\n```\ntail\n```\n\n[Output truncated. Could not save the full output: ENOSPC: no space left on device]"},
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

// H1 regression: fullOutputError survives AppendBashExecution -> JSONL persistence -> session resume -> ConvertToLLM.
func TestRecordedBashResultFullOutputErrorPersistsAcrossResume(t *testing.T) {
	cwd := t.TempDir()
	agentDir := t.TempDir()
	sessionDir := t.TempDir()
	services, err := CreateAgentSessionServices(CreateAgentSessionServicesOptions{CWD: cwd, AgentDir: agentDir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(services.Close)
	sess, err := NewSession(services, SessionOptions{SessionDir: sessionDir})
	if err != nil {
		t.Fatal(err)
	}
	// Persist an assistant message first so the session flushes to disk (Pi session-manager only creates the file after an assistant message).
	if _, err := sess.Inner().AppendMessage(agent.AgentMessage{Assistant: &agent.AssistantMessage{Role: agent.RoleAssistant, Content: []ai.AssistantContentBlock{ai.TextContent{Text: "ready"}}, StopReason: ai.StopReasonStop}}); err != nil {
		t.Fatal(err)
	}
	errMsg := "ENOSPC: no space left on device, write"
	if err := sess.RecordBashResult("seq 3000", BashResult{
		Output:          "2999\n3000",
		ExitCode:        new(0),
		Truncated:       true,
		FullOutputError: errMsg,
	}, new(false)); err != nil {
		t.Fatal(err)
	}
	sessionFile := sess.Path()
	if err := sess.Close(); err != nil {
		t.Fatal(err)
	}

	// Resume from disk and verify both the resumed message and ConvertToLLM carry fullOutputError.
	resumed, err := NewSession(services, SessionOptions{SessionDir: sessionDir, ResumePath: sessionFile})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resumed.Close() })

	var foundMsg bool
	for _, msg := range resumed.Messages() {
		if msg.Role() == "bashExecution" {
			foundMsg = true
			if got, _ := msg.Custom["fullOutputError"].(string); got != errMsg {
				t.Fatalf("resumed bashExecution fullOutputError = %q, want %q", got, errMsg)
			}
		}
	}
	if !foundMsg {
		t.Fatal("resumed session missing bashExecution message")
	}
	llm := agent.ConvertToLLM(resumed.Messages())
	if len(llm) != 2 {
		t.Fatalf("ConvertToLLM len = %d, want 2", len(llm))
	}
	user, ok := llm[1].(ai.UserMessage)
	if !ok {
		t.Fatalf("llm[1] = %T, want ai.UserMessage", llm[1])
	}
	blocks, ok := user.Content.(ai.UserContentBlocks)
	if !ok || len(blocks) != 1 {
		t.Fatalf("user content = %#v", user.Content)
	}
	want := "Ran `seq 3000`\n```\n2999\n3000\n```\n\n[Output truncated. Could not save the full output: " + errMsg + "]"
	if got := blocks[0].(ai.TextContent).Text; got != want {
		t.Fatalf("resumed ConvertToLLM text = %q, want %q", got, want)
	}
}
