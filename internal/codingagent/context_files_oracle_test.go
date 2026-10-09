package codingagent

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

type contextTree struct {
	// Files maps a path relative to the tree root to its content; "{root}" in a content becomes the tree root. A "->" prefix makes the entry a symlink to the rest of the content.
	Files map[string]string
	Cwd   string
	Agent string
}

// TestLoadProjectContextFilesMatchesPi compares loadProjectContextFiles of Pi 1.0.4 with LoadProjectContextFiles over directory trees: ancestor layering, override files, name case, symlinks, and the linked-worktree, bare-repository, submodule and corrupt `.git` file layouts that decide which ancestor context a worktree skips.
func TestLoadProjectContextFilesMatchesPi(t *testing.T) {
	wt := func(extra map[string]string) map[string]string {
		files := map[string]string{
			"main/.git/HEAD":                     "ref: refs/heads/main\n",
			"main/.git/worktrees/feat/commondir": "../..\n",
			"main/.git/worktrees/feat/HEAD":      "ref: refs/heads/feat\n",
			"main/wt/feat/.git":                  "gitdir: {root}/main/.git/worktrees/feat\n",
			"main/wt/feat/src/placeholder.txt":   "x",
			"main/AGENTS.md":                     "main instructions",
			"main/wt/feat/AGENTS.md":             "worktree instructions",
			"main/wt/feat/src/AGENTS.md":         "src instructions",
		}
		for k, v := range extra {
			if v == "<delete>" {
				delete(files, k)
			} else {
				files[k] = v
			}
		}
		return files
	}
	cases := []contextTree{
		{Files: wt(nil), Cwd: "main/wt/feat/src"},
		{Files: wt(map[string]string{"main/wt/feat/AGENTS.md": "<delete>"}), Cwd: "main/wt/feat/src"},
		{Files: wt(map[string]string{"main/wt/feat/CLAUDE.md": "worktree claude", "main/wt/feat/AGENTS.md": "<delete>"}), Cwd: "main/wt/feat/src"},
		{Files: wt(map[string]string{"main/CLAUDE.md": "main claude", "main/AGENTS.md": "<delete>"}), Cwd: "main/wt/feat/src"},
		{Files: wt(map[string]string{"main/AGENTS.override.md": "main override"}), Cwd: "main/wt/feat/src"},
		{Files: wt(map[string]string{"AGENTS.md": "above main"}), Cwd: "main/wt/feat/src"},
		{Files: map[string]string{
			"proj/.bare/HEAD": "ref: refs/heads/main\n", "proj/.bare/worktrees/main/commondir": "../..\n", "proj/.git": "gitdir: ./.bare\n",
			"proj/main/.git": "gitdir: {root}/proj/.bare/worktrees/main\n", "proj/AGENTS.md": "container instructions", "proj/main/AGENTS.md": "main instructions", "proj/main/src/x": "x",
		}, Cwd: "proj/main/src"},
		{Files: map[string]string{
			"repo/.git/HEAD": "ref: refs/heads/main\n", "repo/.git/worktrees/a/commondir": "../..\n", "repo/wt/a/.git": "gitdir: {root}/repo/.git/worktrees/a\n",
			"repo/wt/b/.git": "gitdir: {root}/repo/.git/worktrees/a\n", "repo/AGENTS.md": "repo instructions", "repo/wt/b/AGENTS.md": "sibling instructions", "repo/wt/b/src/x": "x",
		}, Cwd: "repo/wt/b/src"},
		{Files: map[string]string{
			"super/AGENTS.md": "superproject instructions", "super/vendor/lib/AGENTS.md": "submodule instructions", "super/.git/modules/vendor/lib/HEAD": "ref: refs/heads/main\n",
			"super/vendor/lib/.git": "gitdir: {root}/super/.git/modules/vendor/lib\n", "super/vendor/lib/src/x": "x",
		}, Cwd: "super/vendor/lib/src"},
		{Files: map[string]string{
			"outer/AGENTS.md": "outer instructions", "outer/repo/.git/HEAD": "ref: refs/heads/main\n", "outer/repo/AGENTS.md": "repo instructions", "outer/repo/src/AGENTS.md": "leaf instructions",
		}, Cwd: "outer/repo/src"},
		{Files: map[string]string{
			"corrupt/.git": "gitdir: /nonexistent/path/worktrees/feat\n", "corrupt/AGENTS.md": "repo instructions", "corrupt/src/AGENTS.md": "src instructions",
		}, Cwd: "corrupt/src"},
		{Files: map[string]string{
			"a/AGENTS.md": "a", "a/b/AGENTS.override.md": "b override", "a/b/AGENTS.md": "b regular", "a/b/c/CLAUDE.md": "c claude", "a/b/c/AGENTS.MD": "c upper", "agent/AGENTS.md": "global",
		}, Cwd: "a/b/c", Agent: "agent"},
		{Files: map[string]string{"a/CLAUDE.MD": "upper claude", "a/b/x": "x"}, Cwd: "a/b"},
		{Files: map[string]string{"a/AGENTS.md": "real", "a/b/AGENTS.md": "->{root}/a/AGENTS.md", "a/b/x": "x"}, Cwd: "a/b"},
		{Files: map[string]string{"a/AGENTS.md": "", "a/b/AGENTS.md": "only b"}, Cwd: "a/b"},
		{Files: map[string]string{"a/AGENTS.md": "\ufeffbom", "a/b/x": "x"}, Cwd: "a/b"},
		{Files: map[string]string{"a/AGENTS.md": "in agent dir and ancestor", "a/b/x": "x"}, Cwd: "a/b", Agent: "a"},
	}
	type tree struct{ pig, pi string }
	trees := make([]tree, len(cases))
	for i, c := range cases {
		for k, rootp := range []*string{&trees[i].pig, &trees[i].pi} {
			root := filepath.Join(t.TempDir(), []string{"pig", "pi"}[k])
			*rootp = root
			for rel, content := range c.Files {
				p := filepath.Join(root, rel)
				if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
					t.Fatal(err)
				}
				content = strings.ReplaceAll(content, "{root}", root)
				if target, ok := strings.CutPrefix(content, "->"); ok {
					testenv.Symlink(t, target, p)
				} else if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.MkdirAll(filepath.Join(root, "agent"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Join(root, c.Cwd), 0o755); err != nil {
				t.Fatal(err)
			}
		}
	}
	type query struct {
		Cwd   string `json:"cwd"`
		Agent string `json:"agent"`
	}
	queries := make([]query, len(cases))
	for i, c := range cases {
		agent := "agent"
		if c.Agent != "" {
			agent = c.Agent
		}
		queries[i] = query{filepath.Join(trees[i].pi, c.Cwd), filepath.Join(trees[i].pi, agent)}
	}
	var want [][][2]string
	pioracle.Run(t, `
const mod = await load("pi-coding-agent/core/resource-loader.js");
emit(input.map((q) => mod.loadProjectContextFiles({ cwd: q.cwd, agentDir: q.agent }).map((f) => [f.path, f.content])));`, queries, &want)
	for i, c := range cases {
		agent := "agent"
		if c.Agent != "" {
			agent = c.Agent
		}
		files := LoadProjectContextFiles(filepath.Join(trees[i].pig, c.Cwd), filepath.Join(trees[i].pig, agent))
		got := make([][2]string, len(files))
		for j, f := range files {
			got[j] = [2]string{strings.Replace(f.Path, trees[i].pig, "{root}", 1), f.Content}
		}
		wantRel := make([][2]string, len(want[i]))
		for j, f := range want[i] {
			wantRel[j] = [2]string{strings.Replace(f[0], trees[i].pi, "{root}", 1), f[1]}
		}
		if len(got) == 0 && len(wantRel) == 0 {
			continue
		}
		if !reflect.DeepEqual(got, wantRel) {
			t.Errorf("case %d (cwd %s):\n  Pig %q\n  Pi  %q", i, c.Cwd, got, wantRel)
		}
	}
}
