package sdk

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/extensions/sdk/kit"
)

func TestConvertToLLMBashExecutionTruncationNotes(t *testing.T) {
	cases := []struct {
		name string
		msg  map[string]any
		want string
	}{
		{
			name: "path",
			msg:  map[string]any{"role": "bashExecution", "command": "ls", "output": "a", "truncated": true, "fullOutputPath": "/tmp/out.log"},
			want: "[Output truncated. Full output: /tmp/out.log]",
		},
		{
			name: "error",
			msg:  map[string]any{"role": "bashExecution", "command": "ls", "output": "a", "truncated": true, "fullOutputError": "ENOSPC: no space left"},
			want: "[Output truncated. Could not save the full output: ENOSPC: no space left]",
		},
		{
			name: "bare",
			msg:  map[string]any{"role": "bashExecution", "command": "ls", "output": "a", "truncated": true},
			want: "[Output truncated]",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := ConvertToLlm([]map[string]any{tc.msg})
			if len(out) != 1 {
				t.Fatalf("len = %d, want 1", len(out))
			}
			m := out[0]
			content := m["content"].([]any)
			text := content[0].(map[string]any)["text"].(string)
			if !strings.HasSuffix(text, "\n\n"+tc.want) {
				t.Fatalf("text = %q, want suffix %q", text, tc.want)
			}
		})
	}
}

func TestUserBashEventResultDropsNullFullOutputError(t *testing.T) {
	in := map[string]any{
		"result": map[string]any{
			"output":          "ok",
			"exitCode":        0,
			"cancelled":       false,
			"truncated":       true,
			"fullOutputPath":  nil,
			"fullOutputError": nil,
		},
	}
	var e Extension
	out, err := e.userBashEventResult(in)
	if err != nil {
		t.Fatal(err)
	}
	res := out.(map[string]any)["result"].(map[string]any)
	if _, hasPath := res["fullOutputPath"]; hasPath {
		t.Fatalf("fullOutputPath not stripped: %v", res)
	}
	if _, hasErr := res["fullOutputError"]; hasErr {
		t.Fatalf("fullOutputError not stripped: %v", res)
	}
}

func TestBashResultJSONRoundTripFullOutputError(t *testing.T) {
	r := BashResult{Output: "tail", Truncated: true, FullOutputError: "ENOSPC"}
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"fullOutputError":"ENOSPC"`) {
		t.Fatalf("json = %s", data)
	}
	var decoded BashResult
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded != r {
		t.Fatalf("decoded = %+v, want %+v", decoded, r)
	}
}

func TestKitBashExecutionSetCompleteFullOutputError(t *testing.T) {
	b := kit.NewBashExecution("seq 3000", false)
	b.SetCompleteWithError(new(0), false, true, "", "ENOSPC")
	if b.Complete == nil || b.Complete.FullOutputError != "ENOSPC" || b.Complete.FullOutputPath != "" {
		t.Fatalf("Complete = %+v", b.Complete)
	}
}
