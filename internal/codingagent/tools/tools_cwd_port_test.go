package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

func TestToolsContextCWDPort(t *testing.T) {
	for _, tc := range []struct {
		name, file, content string
		params              map[string]any
		want, persisted     string
	}{
		// .upstream/v0.87.1/packages/coding-agent/test/tools.test.ts:966
		{"read", "ctx-cwd-read.txt", "hello from ctx.cwd", map[string]any{"path": "ctx-cwd-read.txt"}, "hello from ctx.cwd", ""},
		// .upstream/v0.87.1/packages/coding-agent/test/tools.test.ts:981
		{"write", "", "", map[string]any{"path": "ctx-cwd-write.txt", "content": "written via ctx.cwd"}, "", "written via ctx.cwd"},
		// .upstream/v0.87.1/packages/coding-agent/test/tools.test.ts:994
		{"edit", "ctx-cwd-edit.txt", "old text", map[string]any{"path": "ctx-cwd-edit.txt", "edits": []editEntry{{"old text", "new text"}}}, "", "new text"},
		// .upstream/v0.87.1/packages/coding-agent/test/tools.test.ts:1009
		{"grep", "ctx-cwd-grep.txt", "match in ctx.cwd", map[string]any{"pattern": "match"}, "ctx-cwd-grep.txt", ""},
		// .upstream/v0.87.1/packages/coding-agent/test/tools.test.ts:1024
		{"find", "ctx-cwd-find.txt", "find me", map[string]any{"pattern": "ctx-cwd-find.txt"}, "ctx-cwd-find.txt", ""},
		// .upstream/v0.87.1/packages/coding-agent/test/tools.test.ts:1038
		{"ls", "ctx-cwd-ls.txt", "list me", map[string]any{}, "ctx-cwd-ls.txt", ""},
		// .upstream/v0.87.1/packages/coding-agent/test/tools.test.ts:1046
		{"bash", "", "", map[string]any{"command": "pwd"}, "cwd", ""},
	} {
		t.Run(tc.name+" uses ctx.cwd when provided", func(t *testing.T) {
			dir := t.TempDir()
			if tc.file != "" {
				if err := os.WriteFile(filepath.Join(dir, tc.file), []byte(tc.content), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			var tool agent.AgentTool
			// Use a distinct empty construction cwd to avoid scanning the host root in a
			// broken implementation; upstream's '/' serves the same fallback distinction.
			for _, candidate := range CreateAllTools(t.TempDir(), nil, "") {
				if candidate.Name() == tc.name {
					tool = candidate
					break
				}
			}
			if tool == nil {
				t.Fatal("missing builtin")
			}
			if bash, ok := tool.(*BashTool); ok {
				bash.HideSessionEnvironment = true
			}
			ctx := extension.WithContext(t.Context(), extension.NewContext(dir, nil, func() error { return nil }, extension.ContextActions{}))
			params := tc.params
			if tc.want == "cwd" && runtime.GOOS == "windows" {
				// Git Bash's pwd prints an MSYS path (/tmp/...); -W prints the
				// Windows path, which may spell the temp directory differently
				// (8.3 short names), so it is compared by file identity.
				params = map[string]any{"command": "pwd -W"}
			}
			result := runFileTool(t, tool, ctx, params)
			want := tc.want
			if want == "cwd" {
				want = dir
			}
			found := strings.Contains(result.Text(), want)
			if tc.want == "cwd" && runtime.GOOS == "windows" {
				found = sameDir(strings.TrimSpace(result.Text()), dir)
			}
			if result.IsError || !found {
				t.Fatalf("result = %+v, want %q", result, want)
			}
			if tc.persisted != "" {
				assertMutationFile(t, filepath.Join(dir, tc.params["path"].(string)), tc.persisted)
			}
			data, err := json.Marshal([]string{tc.name, strings.ReplaceAll(result.Text(), dir, "__CWD__")})
			if err != nil {
				t.Fatal(err)
			}
			fmt.Printf("TOOL_CWD %s\n", data)
		})
	}
}

// sameDir reports whether a and b name the same existing directory.
func sameDir(a, b string) bool {
	infoA, errA := os.Stat(a)
	infoB, errB := os.Stat(b)
	return errA == nil && errB == nil && os.SameFile(infoA, infoB)
}
