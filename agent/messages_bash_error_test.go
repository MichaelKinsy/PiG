package agent

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

func TestBashExecutionToTextTruncationNotesAndMemberOrder(t *testing.T) {
	cases := []struct {
		name   string
		custom map[string]any
		want   string
	}{
		{
			name:   "fullOutputPath",
			custom: map[string]any{"role": "bashExecution", "command": "ls", "output": "x", "truncated": true, "fullOutputPath": "/tmp/a.log"},
			want:   "\n\n[Output truncated. Full output: /tmp/a.log]",
		},
		{
			name:   "fullOutputError",
			custom: map[string]any{"role": "bashExecution", "command": "ls", "output": "x", "truncated": true, "fullOutputError": "ENOSPC: write"},
			want:   "\n\n[Output truncated. Could not save the full output: ENOSPC: write]",
		},
		{
			name:   "truncated bare",
			custom: map[string]any{"role": "bashExecution", "command": "ls", "output": "x", "truncated": true},
			want:   "\n\n[Output truncated]",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			msgs := ConvertToLLM([]AgentMessage{{Custom: tc.custom}})
			if len(msgs) != 1 {
				t.Fatalf("len = %d, want 1", len(msgs))
			}
			user := msgs[0].(ai.UserMessage)
			text := user.Content.(ai.UserContentBlocks)[0].(ai.TextContent).Text
			if !strings.HasSuffix(text, tc.want) {
				t.Fatalf("text = %q, want suffix %q", text, tc.want)
			}
		})
	}

	// L1: verify JSON member order puts fullOutputError directly after fullOutputPath and before timestamp.
	raw, err := json.Marshal(AgentMessage{Custom: map[string]any{
		"role":            "bashExecution",
		"command":         "ls",
		"output":          "x",
		"cancelled":       false,
		"truncated":       true,
		"fullOutputPath":  "/tmp/a.log",
		"fullOutputError": "ENOSPC",
		"timestamp":       123,
	}})
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	iPath := strings.Index(s, `"fullOutputPath"`)
	iErr := strings.Index(s, `"fullOutputError"`)
	iTS := strings.Index(s, `"timestamp"`)
	if iPath < 0 || iPath >= iErr || iErr >= iTS {
		t.Fatalf("member order wrong in %s", s)
	}
}
