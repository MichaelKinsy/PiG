package codingagent

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

// interactive-mode.ts:676-700 getAutocompleteSourceTag takes the git part of a tag from utils/git.ts parseGitUrl. The source strings
// here run through the pinned Pi's parseGitUrl, and the tag PiG builds must equal the tag Pi's rule builds from that result.
func TestAutocompleteSourceTagGitPartMatchesPiParseGitUrl(t *testing.T) {
	sources := []string{
		"https://github.com/user/repo", "https://github.com/user/repo.git", "https://github.com/user/repo@v1.2.3", "https://github.com/user/repo.git@main",
		"http://github.com/user/repo", "git@github.com:user/repo.git", "git@github.com:user/repo@dev", "ssh://git@github.com/user/repo.git", "ssh://git@github.com/user/repo@v2",
		"git://github.com/user/repo", "git+https://github.com/user/repo.git", "git+ssh://git@github.com/user/repo.git@ref", "github:user/repo", "github:user/repo@v1",
		"gitlab:group/sub/repo", "gitlab.com/group/sub/repo", "github.com/user/repo", "github.com/user/repo@feature/x", "user/repo", "user/repo@v1",
		"https://gitlab.com/group/sub/repo.git", "https://bitbucket.org/team/repo", "https://example.com/some/path", "https://example.com/some/repo.git",
		"https://github.com/user/repo/tree/main", "https://github.com/user/repo/", "https://github.com/user", "https://github.com", "git@host.com:path", "git@host:a/b/c.git",
		"npm:@scope/pkg", "npm:pkg@1.0.0", "./local/path", "../up", "/abs/path", "~/home/path", "C:\\win\\path", "just-a-name", "https://github.com/user/repo?x=1", "https://github.com/user/repo#frag",
		"https://user:pass@github.com/user/repo.git", "HTTPS://GITHUB.COM/User/Repo", "https://github.com/user/repo.git.git", " https://github.com/user/repo ", "",
	}
	input, err := json.Marshal(sources)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/git_url.mjs", pigversion.UpstreamVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected []*struct{ Host, Path, Ref string }
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	failures := 0
	for i, src := range sources {
		want := "u"
		if git := expected[i]; git != nil && src != "" {
			ref := ""
			if git.Ref != "" {
				ref = "@" + git.Ref
			}
			want = "u:git:" + git.Host + "/" + git.Path + ref
		}
		switch trimmed := trimJS(src); {
		case trimmed == "auto" || trimmed == "local" || trimmed == "cli":
			want = "u"
		case len(trimmed) >= 4 && trimmed[:4] == "npm:":
			want = "u:" + trimmed
		}
		got := autocompleteSourceTag(&PiSourceInfo{Source: src, Scope: "user"})
		if got != want {
			if failures++; failures <= 10 {
				t.Errorf("%q: PiG tag %q, Pi rule %q", src, got, want)
			}
		}
	}
	if failures > 10 {
		t.Errorf("%d of %d sources differ from Pi", failures, len(sources))
	}
}

func trimJS(s string) string {
	start, end := 0, len(s)
	for start < end && (s[start] == ' ' || s[start] == '\t' || s[start] == '\n') {
		start++
	}
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t' || s[end-1] == '\n') {
		end--
	}
	return s[start:end]
}
