package codingagent

import (
	"math/rand"
	"path"
	"testing"
)

// pi: packages/coding-agent/src/core/resource-loader.ts loadProjectContextFiles against pinned Pi over random directory trees: one to four nested levels carrying any of the five context file names (as files, empty files, byte order marks, directories and symlinks to files of other levels), ordinary repositories, linked worktrees of an ancestor repository, bare layouts, submodule gitdirs, sibling worktrees and corrupt gitdir files, and a global agent directory that may be one of the levels.
func TestLoadProjectContextFilesRandomTreesMatchPi(t *testing.T) {
	rng := rand.New(rand.NewSource(20261010))
	names := []string{"AGENTS.override.md", "AGENTS.md", "AGENTS.MD", "CLAUDE.md", "CLAUDE.MD"}
	var cases []contextTree
	for range 250 {
		depth := 1 + rng.Intn(4)
		levels := make([]string, depth)
		for l := range levels {
			levels[l] = "r"
			if l > 0 {
				levels[l] = path.Join(levels[l-1], "d"+string(rune('a'+rng.Intn(3))))
			}
		}
		files := map[string]string{}
		var repoLevels []int
		for l, dir := range levels {
			for _, name := range names {
				if rng.Intn(4) != 0 {
					continue
				}
				file := path.Join(dir, name)
				switch rng.Intn(8) {
				case 0:
					files[file] = ""
				case 1:
					files[file] = "\ufeffbom " + file
				case 2:
					files[file+"/inner"] = "a directory named like a context file"
				case 3:
					other := levels[rng.Intn(depth)]
					files[file] = "->{root}/" + path.Join(other, names[rng.Intn(len(names))])
				default:
					files[file] = "content of " + file
				}
			}
			switch rng.Intn(5) {
			case 0:
				files[path.Join(dir, ".git/HEAD")] = "ref: refs/heads/main\n"
				repoLevels = append(repoLevels, l)
			case 1:
				if len(repoLevels) == 0 {
					files[path.Join(dir, ".git")] = "gitdir: /nonexistent/worktrees/x\n"
					break
				}
				main := levels[repoLevels[rng.Intn(len(repoLevels))]]
				work := "w" + string(rune('a'+rng.Intn(3)))
				kind := rng.Intn(5)
				gitDir := "{root}/" + main + "/.git/worktrees/" + work
				switch kind {
				case 0, 1:
					files[main+"/.git/worktrees/"+work+"/commondir"] = "../..\n"
					files[main+"/.git/worktrees/"+work+"/HEAD"] = "ref: refs/heads/" + work + "\n"
				case 2:
					gitDir = "{root}/" + main + "/.git/modules/" + work
					files[main+"/.git/modules/"+work+"/HEAD"] = "ref: refs/heads/main\n"
				case 3:
					gitDir = "{root}/" + main + "/.bare/worktrees/" + work
					files[main+"/.bare/HEAD"] = "ref: refs/heads/main\n"
					files[main+"/.bare/worktrees/"+work+"/commondir"] = "../..\n"
				default:
					gitDir = "/nonexistent/path/worktrees/" + work
				}
				files[path.Join(dir, ".git")] = "gitdir: " + gitDir + "\n"
			}
		}
		agent := "agent"
		switch rng.Intn(4) {
		case 0:
			agent = levels[rng.Intn(depth)]
		case 1:
			files["agent/"+names[rng.Intn(len(names))]] = "global agent context"
		}
		cases = append(cases, contextTree{Files: files, Cwd: levels[depth-1], Agent: agent})
	}
	// A worktree of a bare layout (main/.bare plus a nested checkout) whose main directory is also a repository of its own: Pi's
	// findShadowedContextFile (resource-loader.ts:224) shadows nothing, because main/.git is not the common git dir. The random trees
	// rarely line up a same-named context file on both levels of this layout.
	cases = append(cases, contextTree{Files: map[string]string{
		"r/.git/HEAD":                    "ref: refs/heads/main\n",
		"r/.bare/HEAD":                   "ref: refs/heads/main\n",
		"r/.bare/worktrees/wa/commondir": "../..\n",
		"r/.bare/worktrees/wa/HEAD":      "ref: refs/heads/wa\n",
		"r/da/.git":                      "gitdir: {root}/r/.bare/worktrees/wa\n",
		"r/AGENTS.md":                    "main context",
		"r/da/AGENTS.md":                 "worktree context",
	}, Cwd: "r/da", Agent: "agent"})
	compareContextTrees(t, cases)
}
