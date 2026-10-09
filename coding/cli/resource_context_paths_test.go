package cli

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// Pi resource-loader.ts:101-155 resolves context paths and uses the shared Git boundary before reloading the system prompt.
func TestResourceReloadContextPathsReachSystemPrompt(t *testing.T) {
	for _, nestedRepo := range []bool{false, true} {
		name := "absolute-commondir"
		if nestedRepo {
			name = "nested-uninitialized-repo"
		}
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			t.Chdir(root)
			worktree := filepath.Join("main", "worktrees", "feat")
			cwd := filepath.Join(worktree, "nested", "src")
			gitDir := filepath.Join("main", ".git", "worktrees", "feat")
			for _, dir := range []string{cwd, gitDir, "agent"} {
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			if nestedRepo {
				if err := os.Mkdir(filepath.Join(worktree, "nested", ".git"), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			for path, content := range map[string]string{
				filepath.Join("main", ".git", "HEAD"): "ref: refs/heads/main\n",
				filepath.Join(gitDir, "HEAD"):         "ref: refs/heads/feat\n",
				filepath.Join(gitDir, "commondir"):    filepath.Join(root, "main", ".git") + "\n",
				filepath.Join(worktree, ".git"):       "gitdir: " + filepath.Join(root, gitDir) + "\n",
				filepath.Join("main", "AGENTS.md"):    "main instructions",
				filepath.Join(worktree, "AGENTS.md"):  "worktree instructions",
				filepath.Join("agent", "AGENTS.md"):   "global instructions",
			} {
				if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			// The relative cwd resolves like path.resolve against process.cwd(), the physical path: on macOS the temporary
			// directory /var/... is a symlink to /private/var/..., so the context files are reported under the physical root.
			physical := physicalPath(t, root)
			sm := codingagent.NewSettingsManager(cwd, "agent")
			snapshot := reloadResourceSnapshotProvider(cwd, "agent", sm, Args{}, nil)()
			prompt, options := systemPromptRebuilder(cwd, "agent", false, Args{}, nil)(nil, snapshot.ContextFiles)
			want := []extension.SystemPromptContextFile{{Path: filepath.Join(physical, "agent", "AGENTS.md"), Content: "global instructions"}}
			if nestedRepo {
				want = append(want, extension.SystemPromptContextFile{Path: filepath.Join(physical, "main", "AGENTS.md"), Content: "main instructions"})
			}
			want = append(want, extension.SystemPromptContextFile{Path: filepath.Join(physical, worktree, "AGENTS.md"), Content: "worktree instructions"})
			if !slices.Equal(options.ContextFiles, want) {
				t.Fatalf("rebuilt context = %#v, want %#v", options.ContextFiles, want)
			}
			parts := []string{"Project-specific instructions and guidelines:"}
			for _, file := range want {
				parts = append(parts, `<project_instructions path="`+file.Path+`">`+"\n"+file.Content+"\n</project_instructions>")
			}
			wantSection := "<project_context>\n" + strings.Join(parts, "\n\n") + "\n</project_context>"
			if !strings.Contains(prompt, wantSection) {
				t.Fatalf("rebuilt system prompt missing exact context section %q:\n%s", wantSection, prompt)
			}
		})
	}
}
