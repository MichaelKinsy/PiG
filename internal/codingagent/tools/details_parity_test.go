package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// The paired oracle executes Pi 0.87.1's published create*Tool functions with the same fixtures and arguments.
func TestBuiltinToolDetailsParityProbe(t *testing.T) {
	cwd := t.TempDir()
	for name, text := range map[string]string{"a.txt": "hello\nsecond\n", "b.txt": "hello\n", "large.txt": strings.Repeat("line\n", 2001)} {
		if err := os.WriteFile(filepath.Join(cwd, name), []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		name string
		tool agent.AgentTool
		args string
	}{
		{"read", &ReadTool{CWD: cwd}, `{"path":"a.txt"}`},
		{"read limited", &ReadTool{CWD: cwd}, `{"path":"a.txt","limit":1}`},
		{"read truncated", &ReadTool{CWD: cwd}, `{"path":"large.txt"}`},
		{"write", &WriteTool{CWD: cwd}, `{"path":"written.txt","content":"ok"}`},
		{"edit", &EditTool{CWD: cwd}, `{"path":"a.txt","edits":[{"oldText":"second","newText":"changed"}]}`},
		{"ls", &LsTool{CWD: cwd}, `{}`},
		{"ls fractional", &LsTool{CWD: cwd}, `{"limit":1.5}`},
		{"ls limited", &LsTool{CWD: cwd}, `{"limit":1}`},
		{"find", &FindTool{CWD: cwd}, `{"pattern":"a.txt"}`},
		{"find limited", &FindTool{CWD: cwd}, `{"pattern":"a.txt","limit":1}`},
		{"grep", &GrepTool{CWD: cwd}, `{"pattern":"hello","path":"a.txt"}`},
		{"grep fractional", &GrepTool{CWD: cwd}, `{"pattern":".","path":"a.txt","limit":1.5}`},
		{"grep limited", &GrepTool{CWD: cwd}, `{"pattern":"hello","path":"a.txt","limit":1}`},
		{"bash", &BashTool{CWD: cwd}, `{"command":"printf hello"}`},
		{"bash truncated", &BashTool{CWD: cwd}, `{"command":"printf '%060000d' 0"}`},
	} {
		var update map[string]any
		r, err := tc.tool.Execute(t.Context(), "call", json.RawMessage(tc.args), func(text string, details any) {
			if text != "" {
				update = map[string]any{"content": []any{map[string]any{"type": "text", "text": text}}}
				if details != nil {
					update["details"] = details
				}
			}
		})
		if err != nil || r.IsError {
			t.Fatalf("%s: %+v %v", tc.name, r, err)
		}
		if d, ok := r.Details.(*BashDetails); ok && d.FullOutputPath != "" {
			if _, err := os.Stat(d.FullOutputPath); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(d.FullOutputPath); err != nil {
				t.Fatal(err)
			}
			r.Content = []ai.ToolResultMessageContent{ai.TextContent{Text: strings.ReplaceAll(r.Text(), d.FullOutputPath, "OUTPUT_FILE")}}
			d.FullOutputPath = "OUTPUT_FILE"
		}
		result := map[string]any{"content": r.Content}
		if r.Details != nil {
			result["details"] = r.Details
		}
		printToolWire(t, tc.name, result)
		if tc.name == "bash" {
			if update == nil {
				t.Fatal("bash emitted no output update")
			}
			printToolWire(t, "bash output update", update)
		}
	}
	wide := filepath.Join(cwd, "wide")
	if err := os.Mkdir(wide, 0700); err != nil {
		t.Fatal(err)
	}
	for i := range 300 {
		if err := os.WriteFile(filepath.Join(wide, fmt.Sprintf("%03d-%s.txt", i, strings.Repeat("a", 190))), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	r, err := (&LsTool{CWD: cwd}).Execute(t.Context(), "call", json.RawMessage(`{"path":"wide","limit":1000}`), nil)
	if err != nil || r.IsError {
		t.Fatalf("wide ls: %+v %v", r, err)
	}
	printToolWire(t, "ls byte truncated", map[string]any{"content": r.Content, "details": r.Details})
}

func printToolWire(t *testing.T, name string, result any) {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"case": name, "result": result})
	if err != nil {
		t.Fatal(err)
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("TOOL_WIRE %s\n", data)
}
