package source

import (
	"testing"
)

func TestPackageManagerSSHSourceParsingUpstream(t *testing.T) {
	for _, tc := range []struct {
		name, input           string
		kind                  Kind
		host, path, repo, ref string
	}{
		// .upstream/v0.87.1/packages/coding-agent/test/package-manager-ssh.test.ts:33
		{"should parse https:// URL", "https://github.com/user/repo", KindGit, "github.com", "user/repo", "https://github.com/user/repo", ""},
		// .upstream/v0.87.1/packages/coding-agent/test/package-manager-ssh.test.ts:40
		{"should parse ssh:// URL", "ssh://git@github.com/user/repo", KindGit, "github.com", "user/repo", "ssh://git@github.com/user/repo", ""},
		// .upstream/v0.87.1/packages/coding-agent/test/package-manager-ssh.test.ts:50
		{"should parse git@host:path format", "git:git@github.com:user/repo", KindGit, "github.com", "user/repo", "git@github.com:user/repo", ""},
		// .upstream/v0.87.1/packages/coding-agent/test/package-manager-ssh.test.ts:59
		{"should parse host/path shorthand", "git:github.com/user/repo", KindGit, "github.com", "user/repo", "https://github.com/user/repo", ""},
		// .upstream/v0.87.1/packages/coding-agent/test/package-manager-ssh.test.ts:66
		{"should parse shorthand with ref", "git:git@github.com:user/repo@v1.0.0", KindGit, "github.com", "user/repo", "git@github.com:user/repo", "v1.0.0"},
		// .upstream/v0.87.1/packages/coding-agent/test/package-manager-ssh.test.ts:75
		{"should treat git@host:path as local without git: prefix", "git@github.com:user/repo", KindLocal, "", "", "", ""},
		// .upstream/v0.87.1/packages/coding-agent/test/package-manager-ssh.test.ts:80
		{"should treat host/path shorthand as local without git: prefix", "github.com/user/repo", KindLocal, "", "", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			parsed, err := Parse(tc.input, Options{Bare: BareLocal, AllowContributed: true})
			if err != nil {
				t.Fatal(err)
			}
			if parsed.Kind != tc.kind || parsed.GitHost != tc.host || parsed.GitPath != tc.path || parsed.GitRepo != tc.repo || parsed.GitRef != tc.ref {
				t.Fatalf("parsed=%+v", parsed)
			}
			if tc.kind == KindLocal && parsed.Locator != tc.input {
				t.Fatalf("local path=%q", parsed.Locator)
			}
		})
	}
	// .upstream/v0.87.1/packages/coding-agent/test/package-manager-ssh.test.ts:87
	t.Run("should normalize protocol and shorthand-prefixed URLs to same identity", func(t *testing.T) {
		for _, input := range []string{"git:git@github.com:user/repo", "https://github.com/user/repo", "ssh://git@github.com/user/repo"} {
			parsed, err := Parse(input, Options{Bare: BareLocal, AllowContributed: true})
			if err != nil {
				t.Fatal(err)
			}
			identity, err := parsed.Identity(t.TempDir())
			if err != nil || identity != "git:github.com/user/repo" {
				t.Fatalf("identity=%q, err=%v", identity, err)
			}
		}
	})
}
