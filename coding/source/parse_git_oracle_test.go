package source

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

// pi: packages/coding-agent/src/utils/git.ts

// Parse of git sources against pinned Pi's parseGitUrl: the git: shorthand family (hosted-git-info forms for github, gitlab, bitbucket, gist,
// sourcehut), scp-like and protocol URLs, refs after '#' or '@', .git suffixes, and unsafe or malformed locators that Pi rejects with null.
//
// Inputs in knownGitDifferences are the two where PiG differs from Pi on purpose (D110): Pi turns "git://host/..." into the repository
// "https:////host/..." and an upper-case scheme into "https://HTTPS://host/...", neither of which can be cloned. They are asserted to STILL differ,
// so changing them fails this test; every other input must match Pi exactly.
var knownGitDifferences = map[string]string{
	"git://github.com/user/repo":   "D110: Pi's repo is https:////github.com/user/repo",
	"HTTPS://github.com/user/repo": "D110: Pi's repo is https://HTTPS://github.com/user/repo",
}

func TestParseGitMatchesPi(t *testing.T) {
	inputs := []string{
		"https://gitlab.com/group/sub/repo.git", "https://gitlab.com/a/b.git#v1", "git:gitlab.com/group/repo.git", "git:user:pw@github.com/user/repo", "git:foo@github.com:user/repo", "git:user@github.com/user/repo",
		"git:tok@gitlab.com/group/repo.git", "git:a:b@example.com/o/r", "git:example.com:o/r", "git:github.com:user/repo", "git:ssh:git@github.com/user/repo",
		"https://www.github.com/user/repo", "https://github.com/user/repo/", "https://github.com/user/repo/tree/main", "https://github.com/user/repo/tree", "https://github.com/user/repo/tree/feature/x",
		"https://github.com/user/repo/blob/main/a.txt", "https://github.com/user/repo/archive/x.zip", "https://github.com/user/repo.git/", "git+https://github.com/user/repo.git", "git+ssh://git@github.com/user/repo.git#semver:^1.0",
		"https://user:pw@github.com/user/repo", "https://token@github.com/user/repo.git", "https://github.com:443/user/repo", "ssh://git@github.com:22/user/repo", "ssh://GIT@GITHUB.COM/user/repo",
		"https://gitlab.com/group/sub/repo", "https://gitlab.com/group/sub/repo.git@v1", "https://gitlab.com/group/repo/-/tree/main", "https://gitlab.com/user", "gitlab:group/sub/repo", "gitlab:group/sub/repo#v2",
		"https://bitbucket.org/user/repo", "https://bitbucket.org/user/repo/get/main.tar.gz", "bitbucket:user/repo.git#dev", "https://git.sr.ht/~user/repo", "https://git.sr.ht/~user/repo/archive/x.tar.gz", "sourcehut:~user/repo",
		"https://gist.github.com/abc123", "https://gist.github.com/user/abc123", "https://gist.github.com/user/abc123/raw", "gist:user/abc123", "gist:abc123#rev", "git:gist:user/abc123",
		"git:github:user/repo.git", "git:github:@scope/repo", "git:github:user/repo@v1", "git:github:user/repo#v1@v2", "git:github:user", "git:github:", "git:github:user/repo/extra", "git:github:us%20er/re%2Fpo", "git:github:user/re%zzpo",
		"git:www.github.com/user/repo", "git:GitHub.com/user/repo", "git:github.com/User/Repo@Branch", "git:github.com/user/repo@feature/x", "git:github.com/user/repo@", "git:github.com/user/repo/", "git:github.com//repo",
		"git:user/repo@v1", "git:user/repo#v1", "git:user/repo.git", "git:./user/repo", "git:user/repo/sub", "git:u/r", "git:@user/repo", "git:user /repo", "git:user/re po",
		"git:git@gitlab.com:group/sub/repo.git", "git:git@bitbucket.org:user/repo", "git:git@git.sr.ht:~user/repo", "git:git@gist.github.com:abc123", "git:git@example.com:user/repo.git@v1", "git:git@example.com:a/b#c",
		"git:ssh://git@gitlab.com/group/repo", "git:ssh://git@example.com:2222/group/repo.git", "git:https://example.com:8080/a/b", "git:https://user@example.com/a/b@v1", "git:https://example.com/a/b/c/d.git@tag",
		"git:ssh://git@github.com/user/repo@v1", "git:https://github.com/user/repo@v1#ignored", "git:https://github.com/user/repo@v1.0.0", "https://github.com/user/repo@main",
		"git:https://EXAMPLE.com/a/b", "git:https://example.com/%7Euser/repo", "git:https://example.com/a/b%00c", "git:https://example.com/a/./b", "git:https://example.com/a//b",

		"git:github.com/user/repo", "git:github.com/user/repo@v1", "git:github.com/user/repo#v1", "git:github.com/user/repo.git", "git:github.com/user/repo.git@main",
		"git:user/repo", "git:github:user/repo", "git:github:user/repo#main", "git:gitlab:user/repo", "git:bitbucket:user/repo", "git:gist:abc123",
		"git:git@github.com:user/repo", "git:git@github.com:user/repo.git", "git:git@github.com:user/repo.git@v2", "git:git@github.com:user/repo#v2", "git:git@gitlab.com:group/sub/repo",
		"git:https://github.com/user/repo", "git:https://github.com/user/repo.git", "git:https://github.com/user/repo@v1", "git:https://github.com/user/repo#v1",
		"git:ssh://git@github.com/user/repo", "git:ssh://git@github.com/user/repo.git#v1", "git:git://github.com/user/repo", "git:http://example.com/user/repo",
		"git:example.com/user/repo", "git:example.com/user/repo@v1", "git:example.com/group/sub/repo.git", "git:localhost/user/repo", "git:localhost:3000/user/repo",
		"git:gitlab.com/user/repo", "git:gitlab.com/group/sub/repo", "git:bitbucket.org/user/repo", "git:codeberg.org/user/repo", "git:git.sr.ht/~user/repo",
		"https://github.com/user/repo", "https://github.com/user/repo.git", "https://github.com/user/repo#v1", "https://github.com/user/repo@v1", "http://github.com/user/repo",
		"ssh://git@github.com/user/repo", "git://github.com/user/repo", "HTTPS://github.com/user/repo", "https://GitHub.com/User/Repo",
		"https://github.com/user", "https://github.com/", "https://github.com", "https://example.com/repo", "https://example.com/a/b/c.git", "https://example.com/a/../b",
		"https://example.com/a/%2e%2e/b", "https://example.com/a/b%2Fc", "https://example.com/a\\b", "https://user@example.com/a/b", "https://example.com:8443/a/b",
		"git:foo", "git:", "git: ", "git:/abs/path", "git:../x/y", "git:a/b", "git:x.y/z", "git:github.com", "git:github.com/", "git:github.com/user", "git:github.com/user/",
		"npm:pkg", "pkg", "./local", "/abs/path", "github.com/user/repo", "user/repo", "ftp://example.com/a/b", "  git:github.com/user/repo  ", "git:  github.com/user/repo",
		"git:github.com/user/repo#", "git:github.com/user/repo@", "git:github.com/user/repo#a#b", "git:github.com/user/repo@v1@v2", "git:github.com/us er/repo",
		"git:git@github.com:/user/repo", "git:git@:user/repo", "git:git@host:repo", "git:git@host:a/b/c.git", "git:git@host.com:a/..",
	}
	input, err := json.Marshal(inputs)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/parse_git.mjs", pigversion.UpstreamVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected []*struct {
		Repo   string `json:"repo"`
		Host   string `json:"host"`
		Path   string `json:"path"`
		Ref    string `json:"ref"`
		Pinned bool   `json:"pinned"`
		Err    string `json:"err"`
	}
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	stale := map[string]bool{}
	for in := range knownGitDifferences {
		stale[in] = true
	}
	for i, in := range inputs {
		want := expected[i]
		got, err := Parse(in, Options{BaseDir: t.TempDir(), Bare: BareReject})
		isGit := err == nil && got.Kind == KindGit
		differs := false
		switch {
		case want == nil:
			differs = isGit
		case want.Err != "":
			t.Fatalf("Pi threw for %q: %s", in, want.Err)
		case !isGit:
			differs = true
		default:
			differs = got.GitRepo != want.Repo || got.GitHost != want.Host || got.GitPath != want.Path || got.GitRef != want.Ref
		}
		if _, known := knownGitDifferences[in]; known {
			delete(stale, in)
			if !differs {
				t.Errorf("Parse(%q) now matches Pi: remove it from knownGitDifferences", in)
			}
			continue
		}
		if differs {
			t.Errorf("Parse(%q) = %+v (err %v), Pi %+v", in, got, err, want)
		}
	}
	for in := range stale {
		t.Errorf("knownGitDifferences entry %q is not in the input list", in)
	}
}
