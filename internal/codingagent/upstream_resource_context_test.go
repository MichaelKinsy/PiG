package codingagent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// Ports packages/coding-agent/test/resource-loader.test.ts:1010-1141 (all nine nested-worktree cases), plus :380-415 (override layering and directory candidates). The extra cases probe resource-loader.ts:123-124 and footer-data-provider.ts:16-47 against installed Pi 0.87.1.
func TestUpstreamResourceLoaderContextFiles(t *testing.T) {
	data, err := os.ReadFile("testdata/resource-context-cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Name, CWD, Main, LinkedWorktree, WorktreeName string
		RelativeInputs                                bool
		Dirs                                          []string
		Files                                         map[string]string
		Want                                          []ContextFile
	}
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			root := t.TempDir()
			mkdir := func(path string) {
				t.Helper()
				if err := os.MkdirAll(filepath.Join(root, path), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			write := func(path, content string) {
				t.Helper()
				mkdir(filepath.Dir(path))
				writeContextFile(t, filepath.Join(root, path), strings.ReplaceAll(content, "{{ROOT}}", root))
			}
			mkdir("agent")
			mkdir(tc.CWD)
			for _, dir := range tc.Dirs {
				mkdir(dir)
			}
			if tc.LinkedWorktree != "" {
				name := tc.WorktreeName
				if name == "" {
					name = filepath.Base(tc.LinkedWorktree)
				}
				gitDir := filepath.Join(tc.Main, ".git", "worktrees", name)
				write(filepath.Join(tc.Main, ".git", "HEAD"), "ref: refs/heads/main\n")
				write(filepath.Join(gitDir, "HEAD"), "ref: refs/heads/feat\n")
				write(filepath.Join(gitDir, "commondir"), "../..")
				write(filepath.Join(tc.LinkedWorktree, ".git"), "gitdir: "+filepath.Join(root, gitDir)+"\n")
			}
			for path, content := range tc.Files {
				write(path, content)
			}
			cwd, agentDir := filepath.Join(root, tc.CWD), filepath.Join(root, "agent")
			if tc.RelativeInputs {
				t.Chdir(root)
				cwd, agentDir = tc.CWD, "agent"
			}
			getStderr := withCapturedStderr(t)
			got := LoadProjectContextFiles(cwd, agentDir)
			stderr := getStderr()
			// resource-loader.test.ts:399 also requires directory candidates to be ignored without read warnings.
			for _, dir := range tc.Dirs {
				if strings.Contains(stderr, filepath.Join(root, dir)) {
					t.Errorf("directory candidate produced a warning: %s", stderr)
				}
			}
			want := make([]ContextFile, len(tc.Want))
			for i, file := range tc.Want {
				want[i] = ContextFile{Path: filepath.Join(root, file.Path), Content: file.Content}
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("context files = %#v\nwant %#v", got, want)
			}
		})
	}
}
