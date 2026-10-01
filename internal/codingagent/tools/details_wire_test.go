package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
)

// Pi 0.87.1 ls.ts:141, find.ts:147 and grep.ts:282 retain Number.MAX_SAFE_INTEGER in byte-truncation metadata.
func TestBuiltinByteTruncationWireLimit(t *testing.T) {
	cwd := t.TempDir()
	for i := range 300 {
		name := fmt.Sprintf("%03d-%s.txt", i, strings.Repeat("a", 190))
		if err := os.WriteFile(filepath.Join(cwd, name), []byte("needle\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		name string
		tool agent.AgentTool
		args string
	}{
		{"ls", &LsTool{CWD: cwd}, `{"limit":1000}`},
		{"find", &FindTool{CWD: cwd}, `{"pattern":"*.txt","limit":1000}`},
		{"grep", &GrepTool{CWD: cwd}, `{"pattern":"needle","limit":1000}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, err := tc.tool.Execute(t.Context(), "call", json.RawMessage(tc.args), nil)
			if err != nil || r.IsError {
				t.Fatalf("result=%+v error=%v", r, err)
			}
			data, err := json.Marshal(r.Details)
			if err != nil {
				t.Fatal(err)
			}
			var wire map[string]any
			if err := json.Unmarshal(data, &wire); err != nil {
				t.Fatal(err)
			}
			tr, ok := wire["truncation"].(map[string]any)
			if !ok || tr["maxLines"] != float64(1<<53-1) || tr["truncatedBy"] != "bytes" || tr["maxBytes"] != float64(51200) || len(wire) != 1 {
				t.Fatalf("truncation metadata=%s", data)
			}
		})
	}
}

// Pi 0.87.1 bash.ts:270-276 always emits a details object for a nonempty output update; bash.ts:324-338 adds the complete sparse details to truncated results. PowerShell uses this same shell definition.
func TestShellResultAndUpdateDetailsWire(t *testing.T) {
	var mu sync.Mutex
	var updates []any
	r, err := (&BashTool{CWD: t.TempDir()}).Execute(t.Context(), "call", json.RawMessage(`{"command":"printf hello"}`), func(partial agent.AgentToolResult) {
		if partial.Text() != "" {
			mu.Lock()
			updates = append(updates, partial.Details)
			mu.Unlock()
		}
	})
	if err != nil || r.IsError || r.Details != nil {
		t.Fatalf("result=%+v error=%v", r, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(updates) == 0 {
		t.Fatal("no output updates")
	}
	for _, update := range updates {
		data, _ := json.Marshal(update)
		if string(data) != "{}" {
			t.Fatalf("update details=%s", data)
		}
	}
	large, err := (&BashTool{CWD: t.TempDir()}).Execute(t.Context(), "call", json.RawMessage(`{"command":"printf '%060000d' 0"}`), nil)
	if err != nil || large.IsError {
		t.Fatalf("shell error=%v result=%+v", err, large)
	}
	data, _ := json.Marshal(large.Details)
	var details map[string]any
	_ = json.Unmarshal(data, &details)
	tr, ok := details["truncation"].(map[string]any)
	path, hasPath := details["fullOutputPath"].(string)
	if !ok || !hasPath || len(details) != 2 || tr["totalBytes"] != float64(60000) || tr["outputBytes"] != float64(51200) || tr["lastLinePartial"] != true {
		t.Fatalf("shell details=%s", data)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
}

// ls.ts:143-160; find.ts:149-165; grep.ts:284-307; bash.ts:324-374.
// Compare the actual tool result, not ToolResultDetailsFor (the old extension-only projection hid RPC/session leaks).
func TestBuiltinToolResultDetailsWire(t *testing.T) {
	cwd := t.TempDir()
	for name, text := range map[string]string{"a.txt": "hello\nsecond\n", "b.txt": "hello\n", "large.txt": strings.Repeat("line\n", 2001)} {
		if err := os.WriteFile(filepath.Join(cwd, name), []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		name       string
		tool       agent.AgentTool
		args, want string
	}{
		{"read ordinary", &ReadTool{CWD: cwd}, `{"path":"a.txt"}`, `{}`},
		{"read user limited", &ReadTool{CWD: cwd}, `{"path":"a.txt","limit":1}`, `{}`},
		{"write", &WriteTool{CWD: cwd}, `{"path":"written.txt","content":"ok"}`, `{}`},
		{"ls ordinary", &LsTool{CWD: cwd}, `{}`, `{}`},
		{"ls fractional limit", &LsTool{CWD: cwd}, `{"limit":1.5}`, `{"details":{"entryLimitReached":1.5}}`},
		{"ls limited", &LsTool{CWD: cwd}, `{"limit":1}`, `{"details":{"entryLimitReached":1}}`},
		{"find ordinary", &FindTool{CWD: cwd}, `{"pattern":"a.txt"}`, `{}`},
		{"find limited", &FindTool{CWD: cwd}, `{"pattern":"*.txt","limit":1}`, `{"details":{"resultLimitReached":1}}`},
		{"grep ordinary", &GrepTool{CWD: cwd}, `{"pattern":"hello","path":"a.txt"}`, `{}`},
		{"grep fractional limit", &GrepTool{CWD: cwd}, `{"pattern":"hello","limit":1.5}`, `{"details":{"matchLimitReached":1.5}}`},
		{"grep limited", &GrepTool{CWD: cwd}, `{"pattern":"hello","limit":1}`, `{"details":{"matchLimitReached":1}}`},
		{"bash ordinary", &BashTool{CWD: cwd}, `{"command":"printf hello"}`, `{}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, err := tc.tool.Execute(context.Background(), "call", json.RawMessage(tc.args), nil)
			if err != nil || result.IsError {
				t.Fatalf("execute: %+v, %v", result, err)
			}
			data, err := json.Marshal(struct {
				Details any `json:"details,omitempty"`
			}{result.Details})
			if err != nil {
				t.Fatal(err)
			}
			var got, want any
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal([]byte(tc.want), &want); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("result = %s, want %s", data, tc.want)
			}
		})
	}
	t.Run("read truncated", func(t *testing.T) {
		result, err := (&ReadTool{CWD: cwd}).Execute(context.Background(), "call", json.RawMessage(`{"path":"large.txt"}`), nil)
		if err != nil || result.IsError {
			t.Fatalf("read: %+v, %v", result, err)
		}
		data, err := json.Marshal(result.Details)
		if err != nil {
			t.Fatal(err)
		}
		expected := map[string]any{"truncation": map[string]any{"content": strings.TrimSuffix(strings.Repeat("line\n", 2000), "\n"), "truncated": true, "truncatedBy": "lines", "totalLines": 2001, "totalBytes": 10005, "outputLines": 2000, "outputBytes": 9999, "lastLinePartial": false, "firstLineExceedsLimit": false, "maxLines": 2000, "maxBytes": 51200}}
		want, _ := json.Marshal(expected)
		var a, b any
		_ = json.Unmarshal(data, &a)
		_ = json.Unmarshal(want, &b)
		if !reflect.DeepEqual(a, b) {
			t.Fatalf("read truncation differs: got %s, want %s", data, want)
		}
	})
}
