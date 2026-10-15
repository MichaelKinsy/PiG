package codingagent

import (
	"maps"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"slices"
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
	compareContextTrees(t, cases)
}

// Context file names that differ only in case, within a directory and between the agent directory, the working directory or a worktree's gitdir and the tree itself. loadContextFileFromDir (resource-loader.ts:184) probes each candidate name with existsSync and statSync and returns the probed path, and loadProjectContextFiles and findShadowedContextFile compare paths as strings (canonicalizePath is realpathSync, which keeps the case it is given): on a case-insensitive filesystem (APFS, NTFS) a probe finds the entry stored under another case and returns the probed name, and two spellings of one directory are two paths; on a case-sensitive one every spelling is an entry of its own.
func TestLoadProjectContextFilesCaseVariantNamesMatchPi(t *testing.T) {
	compareContextTrees(t, []contextTree{
		{Files: map[string]string{"a/AGENTS.MD": "upper", "a/AGENTS.md": "lower", "a/CLAUDE.MD": "upper claude", "a/CLAUDE.md": "lower claude", "a/b/x": "x"}, Cwd: "a/b"},
		{Files: map[string]string{"a/AGENTS.MD/inner": "a directory", "a/AGENTS.md": "file", "a/b/CLAUDE.md/inner": "a directory", "a/b/CLAUDE.MD": "claude"}, Cwd: "a/b"},
		{Files: map[string]string{"a/AGENTS.md/inner": "a directory", "a/AGENTS.MD": "file", "a/b/x": "x"}, Cwd: "a/b"},
		{Files: map[string]string{"a/CLAUDE.MD": "->{root}/a/b/AGENTS.md", "a/CLAUDE.md": "claude", "a/b/AGENTS.md": "b"}, Cwd: "a/b"},
		{Files: map[string]string{"r/AGENTS.md": "r", "r/b/x": "x"}, Cwd: "r/b", Agent: "R"},
		{Files: map[string]string{"r/AGENTS.md": "r", "r/b/AGENTS.MD": "b"}, Cwd: "R/B"},
		{Files: map[string]string{
			"r/.git/HEAD": "ref: refs/heads/main\n", "r/.git/worktrees/wa/commondir": "../..\n", "r/.git/worktrees/wa/HEAD": "ref: refs/heads/wa\n",
			"r/wt/a/.git": "gitdir: {root}/R/.git/worktrees/wa\n", "r/AGENTS.md": "main", "r/wt/a/AGENTS.md": "worktree", "r/wt/a/src/x": "x",
		}, Cwd: "r/wt/a/src"},
	})
}

// compareContextTrees builds each tree twice (one for Pig, one for Pi), loads the project context files from both, and reports any difference. Both builds write the entries in path order and leave out an entry the filesystem resolves to an earlier one stored under another case (foldsOntoWrittenEntry), so on any filesystem the two trees are the same.
func compareContextTrees(t *testing.T, cases []contextTree) {
	t.Helper()
	type tree struct{ pig, pi string }
	trees := make([]tree, len(cases))
	for i, c := range cases {
		rels := slices.Sorted(maps.Keys(c.Files))
		for k, rootp := range []*string{&trees[i].pig, &trees[i].pi} {
			root := filepath.Join(t.TempDir(), []string{"pig", "pi"}[k])
			*rootp = root
			written := map[string]bool{}
			for _, rel := range rels {
				if foldsOntoWrittenEntry(root, rel, written) {
					continue
				}
				p := filepath.Join(root, rel)
				if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
					t.Fatal(err)
				}
				content := strings.ReplaceAll(c.Files[rel], "{root}", root)
				if target, ok := strings.CutPrefix(content, "->"); ok {
					testenv.Symlink(t, target, p)
				} else if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
					t.Fatal(err)
				}
				for dir := rel; dir != "."; dir = path.Dir(dir) {
					written[dir] = true
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

// foldsOntoWrittenEntry reports whether rel, or a directory on its way, is not among the written entries and their directories but exists all the same: a case-insensitive filesystem (APFS, NTFS) resolves a name that differs only in case to the entry written under the other spelling, so a tree naming both holds one entry, the first written. On a case-sensitive filesystem nothing outside written exists below root.
func foldsOntoWrittenEntry(root, rel string, written map[string]bool) bool {
	parts := strings.Split(rel, "/")
	for n := 1; n <= len(parts); n++ {
		prefix := strings.Join(parts[:n], "/")
		if written[prefix] {
			continue
		}
		_, err := os.Lstat(filepath.Join(root, prefix))
		return err == nil
	}
	return false
}
