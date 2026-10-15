package codingagent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// Pi's findGitPaths and FooterDataProvider branch resolution (core/footer-data-provider.js) on one directory tree of repository layouts: .git as a directory or a worktree file, relative and absolute gitdir, commondir, missing HEAD, a .git file without a gitdir line (the walk continues upward), and HEAD or gitdir text with the whitespace spellings JavaScript trim() strips (BOM, NBSP) and does not (U+0085).
func TestFindGitPathsMatchesPiOracle(t *testing.T) {
	root := t.TempDir()
	write := func(rel, content string) {
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	heads := map[string]string{
		"main": "ref: refs/heads/main\n", "slash": "ref: refs/heads/feature/x\n", "crlf": "ref: refs/heads/main\r\n", "nonl": "ref: refs/heads/main", "bom": "\ufeffref: refs/heads/main\n", "nbsp": "\u00a0ref: refs/heads/main\u00a0\n",
		"nel": "\u0085ref: refs/heads/main\u0085\n", "tag": "ref: refs/tags/v1\n", "sha": "0123456789abcdef0123456789abcdef01234567\n", "empty": "", "spaces": "  ref: refs/heads/main  \n", "noref": "ref: refs/heads/", "dots": "ref: refs/heads/.hidden",
		"uni": "ref: refs/heads/é\U0001F600\n", "twoline": "ref: refs/heads/main\nextra\n", "lower": "REF: refs/heads/main", "tab": "ref:\trefs/heads/main", "single": "ref: refs/heads/a b\n",
	}
	var cwds []string
	for name, head := range heads {
		write(filepath.Join("dir-"+name, ".git", "HEAD"), head)
		cwds = append(cwds, filepath.Join(root, "dir-"+name), filepath.Join(root, "dir-"+name, "sub", "deeper"))
		if err := os.MkdirAll(filepath.Join(root, "dir-"+name, "sub", "deeper"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// Worktrees: .git files with each gitdir spelling.
	write("main-repo/.git/HEAD", "ref: refs/heads/main\n")
	write("main-repo/.git/worktrees/wt/HEAD", "ref: refs/heads/wtbranch\n")
	write("main-repo/.git/worktrees/wt/commondir", "../..\n")
	write("wt-rel/.git", "gitdir: ../main-repo/.git/worktrees/wt\n")
	write("wt-abs/.git", "gitdir: "+filepath.Join(root, "main-repo", ".git", "worktrees", "wt")+"\n")
	write("wt-spaces/.git", "  gitdir: ../main-repo/.git/worktrees/wt  \n")
	write("wt-bom/.git", "\ufeffgitdir: ../main-repo/.git/worktrees/wt\n")
	write("wt-nbsp/.git", "gitdir: ../main-repo/.git/worktrees/wt\u00a0\n")
	write("wt-nel/.git", "gitdir: ../main-repo/.git/worktrees/wt\u0085\n")
	write("wt-two-spaces/.git", "gitdir:  ../main-repo/.git/worktrees/wt\n")
	write("wt-nocolon/.git", "gitdir ../main-repo/.git/worktrees/wt\n")
	write("wt-upper/.git", "GITDIR: ../main-repo/.git/worktrees/wt\n")
	write("wt-missing/.git", "gitdir: ../nowhere\n")
	write("wt-nocommon/.git", "gitdir: ../main-repo/.git/worktrees/wt2\n")
	write("main-repo/.git/worktrees/wt2/HEAD", "ref: refs/heads/wt2\n")
	write("wt-commondir-abs/.git", "gitdir: ../main-repo/.git/worktrees/wt3\n")
	write("main-repo/.git/worktrees/wt3/HEAD", "ref: refs/heads/wt3\n")
	write("main-repo/.git/worktrees/wt3/commondir", filepath.Join(root, "main-repo", ".git")+"\n")
	write("wt-commondir-bad/.git", "gitdir: ../main-repo/.git/worktrees/wt4\n")
	write("main-repo/.git/worktrees/wt4/HEAD", "ref: refs/heads/wt4\n")
	write("main-repo/.git/worktrees/wt4/commondir", "\u00a0../..\u00a0\n")
	write("wt-empty/.git", "")
	write("wt-garbage/.git", "not a gitdir line\n")
	write("outer/.git/HEAD", "ref: refs/heads/outer\n")
	write("outer/inner-garbage/.git", "garbage\n")
	write("outer/inner-garbage/src/f.txt", "x")
	write("nohead/.git/config", "")
	write("nohead/sub/f.txt", "x")
	write("outer-nohead/.git/HEAD", "ref: refs/heads/o\n")
	write("outer-nohead/inner/.git/config", "")
	write("outer-nohead/inner/sub/f.txt", "x")
	for _, rel := range []string{"main-repo", "wt-rel", "wt-abs", "wt-spaces", "wt-bom", "wt-nbsp", "wt-nel", "wt-two-spaces", "wt-nocolon", "wt-upper", "wt-missing", "wt-nocommon", "wt-commondir-abs", "wt-commondir-bad", "wt-empty", "wt-garbage", "outer/inner-garbage/src", "nohead/sub", "outer-nohead/inner/sub", "outer"} {
		cwds = append(cwds, filepath.Join(root, rel))
	}
	type result struct {
		Found        bool   `json:"found"`
		RepoDir      string `json:"repoDir"`
		CommonGitDir string `json:"commonGitDir"`
		HeadPath     string `json:"headPath"`
		Branch       string `json:"branch"`
	}
	var want []result
	pioracle.Run(t, `
const mod = await load("pi-coding-agent/core/footer-data-provider.js");
emit(input.map((cwd) => {
	const paths = mod.findGitPaths(cwd);
	if (!paths) return { found: false };
	const provider = new mod.FooterDataProvider(cwd);
	const branch = provider.getGitBranch();
	provider.dispose();
	return { found: true, repoDir: paths.repoDir, commonGitDir: paths.commonGitDir, headPath: paths.headPath, branch: branch ?? "" };
}));`, cwds, &want)
	for i, cwd := range cwds {
		paths, ok := findGitPaths(cwd)
		have := result{Found: ok}
		if ok {
			have = result{Found: true, RepoDir: paths.repoDir, CommonGitDir: paths.commonGitDir, HeadPath: paths.headPath, Branch: resolveGitBranchFromPaths(paths)}
		}
		if have != want[i] {
			t.Errorf("cwd=%s:\n Go %+v\n Pi %+v", cwd, have, want[i])
		}
	}
}
