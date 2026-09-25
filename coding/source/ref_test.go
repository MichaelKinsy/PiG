package source

import (
	"path/filepath"
	"testing"
)

func TestParseCanonicalIdentity(t *testing.T) {
	base := t.TempDir()
	cases := []struct {
		name     string
		input    string
		bare     BarePolicy
		wantKind Kind
		wantID   string
	}{
		{name: "npm", input: "npm:@scope/pkg@1.2.3", wantKind: KindNPM, wantID: "npm:@scope/pkg"},
		{name: "bare npm at install boundary", input: "@scope/pkg", bare: BareNPM, wantKind: KindNPM, wantID: "npm:@scope/pkg"},
		{name: "git shorthand", input: "git:github.com/acme/tools@v2", wantKind: KindGit, wantID: "git:github.com/acme/tools"},
		{name: "git https", input: "https://github.com/acme/tools.git@v2", wantKind: KindGit, wantID: "git:github.com/acme/tools"},
		{name: "git ssh prefixed", input: "git:git@github.com:acme/tools@v2", wantKind: KindGit, wantID: "git:github.com/acme/tools"},
		{name: "relative local", input: "resources/tool", bare: BareLocal, wantKind: KindLocal, wantID: "local:" + filepath.Join(base, "resources", "tool")},
		{name: "contributed", input: "marketplace:example/tools", wantKind: KindContributed, wantID: "marketplace:example/tools"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ref, err := Parse(tc.input, Options{BaseDir: base, Bare: tc.bare, AllowContributed: true})
			if err != nil {
				t.Fatalf("Parse(%q): %v", tc.input, err)
			}
			if ref.Kind != tc.wantKind {
				t.Fatalf("kind = %q, want %q", ref.Kind, tc.wantKind)
			}
			identity, err := ref.Identity(base)
			if err != nil {
				t.Fatalf("Identity: %v", err)
			}
			if identity != tc.wantID {
				t.Fatalf("identity = %q, want %q", identity, tc.wantID)
			}
		})
	}
}

func TestParseGitURLMatchesUpstream(t *testing.T) {
	for _, tc := range []struct {
		name   string
		source string
		host   string
		path   string
		ref    string
		repo   string
	}{
		{name: "HTTPS URL", source: "https://github.com/user/repo", host: "github.com", path: "user/repo", repo: "https://github.com/user/repo"},
		{name: "SSH URL", source: "ssh://git@github.com/user/repo", host: "github.com", path: "user/repo", repo: "ssh://git@github.com/user/repo"},
		{name: "protocol URL with ref", source: "https://github.com/user/repo@v1.0.0", host: "github.com", path: "user/repo", ref: "v1.0.0", repo: "https://github.com/user/repo"},
		{name: "prefixed SCP shorthand", source: "git:git@github.com:user/repo", host: "github.com", path: "user/repo", repo: "git@github.com:user/repo"},
		{name: "prefixed host path shorthand", source: "git:github.com/user/repo", host: "github.com", path: "user/repo", repo: "https://github.com/user/repo"},
		{name: "prefixed shorthand with ref", source: "git:git@github.com:user/repo@v1.0.0", host: "github.com", path: "user/repo", ref: "v1.0.0", repo: "git@github.com:user/repo"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Parse(tc.source, Options{Bare: BareReject})
			if err != nil {
				t.Fatalf("Parse(%q): %v", tc.source, err)
			}
			if got.Kind != KindGit || got.GitHost != tc.host || got.GitPath != tc.path || got.GitRef != tc.ref || got.GitRepo != tc.repo {
				t.Fatalf("Parse(%q) = %#v, want host=%q path=%q ref=%q repo=%q", tc.source, got, tc.host, tc.path, tc.ref, tc.repo)
			}
		})
	}
}

func TestParseGitURLRejectsUnsafeInstallPaths(t *testing.T) {
	for _, source := range []string{
		"git:git@evil.example:../../victim/repo",
		"https://evil.example/..%2F..%2Fvictim/repo",
		"https://evil.example/..%2F..%2Fvictim/repo%",
		"git:git@evil.example:/absolute/repo",
		`git:git@evil.example:user\repo/name`,
		"git:git@evil.example:user/repo\x00name",
	} {
		if _, err := Parse(source, Options{Bare: BareReject}); err == nil {
			t.Fatalf("Parse(%q) succeeded", source)
		}
	}
}

func TestParseGitURLRejectsUnprefixedShorthand(t *testing.T) {
	for _, source := range []string{
		"git@github.com:user/repo",
		"github.com/user/repo",
		"user/repo",
	} {
		if _, err := Parse(source, Options{Bare: BareReject}); err == nil {
			t.Fatalf("Parse(%q) succeeded", source)
		}
	}
}

func TestParseCustomNPMRegistry(t *testing.T) {
	ref, err := Parse("npm:@acme/tools@1.2.3?registry=https%3A%2F%2Fnpm.example.com%2Fteam", Options{Bare: BareReject})
	if err != nil {
		t.Fatal(err)
	}
	if ref.Kind != KindNPM || ref.NPMName != "@acme/tools" || ref.NPMVer != "1.2.3" || ref.NPMRegistry != "https://npm.example.com/team" {
		t.Fatalf("ref = %#v", ref)
	}
	identity, err := ref.Identity(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if identity != "npm:@acme/tools?registry=https%3A%2F%2Fnpm.example.com%2Fteam" {
		t.Fatalf("identity = %q", identity)
	}
}

func TestParseRejectsUnsafeCustomNPMRegistry(t *testing.T) {
	for _, source := range []string{
		"npm:pkg?registry=http%3A%2F%2Fnpm.example.com",
		"npm:pkg?registry=https%3A%2F%2Ftoken%40npm.example.com",
		"npm:pkg?registry=https%3A%2F%2Fnpm.example.com%23token",
		"npm:pkg?other=value",
	} {
		if _, err := Parse(source, Options{Bare: BareReject}); err == nil {
			t.Fatalf("Parse(%q) succeeded", source)
		}
	}
}

func TestParseGitSubdirectory(t *testing.T) {
	ref, err := Parse("git:https://github.com/acme/tools.git@v2#subdirectory=plugins/review", Options{Bare: BareReject})
	if err != nil {
		t.Fatal(err)
	}
	if ref.Kind != KindGit || ref.GitRepo != "https://github.com/acme/tools.git" || ref.GitRef != "v2" || ref.GitSubdir != "plugins/review" {
		t.Fatalf("ref = %#v", ref)
	}
	identity, err := ref.Identity(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if identity != "git:github.com/acme/tools#subdirectory=plugins/review" {
		t.Fatalf("identity = %q", identity)
	}
}

func TestParseRejectsEscapingGitSubdirectory(t *testing.T) {
	for _, source := range []string{
		"git:https://github.com/acme/tools#subdirectory=../secret",
		"git:https://github.com/acme/tools#subdirectory=/absolute",
		"git:https://github.com/acme/tools#other=value",
	} {
		if _, err := Parse(source, Options{Bare: BareReject}); err == nil {
			t.Fatalf("Parse(%q) succeeded", source)
		}
	}
}

func TestParseRejectsInvalidSources(t *testing.T) {
	cases := []string{
		"",
		"npm:",
		"git:",
		"https://example.com/one-segment",
		"Marketplace:example/tools",
		"marketplace:",
		"unknown:value with space",
	}
	for _, input := range cases {
		t.Run(input, func(t *testing.T) {
			if _, err := Parse(input, Options{BaseDir: t.TempDir(), Bare: BareLocal, AllowContributed: true}); err == nil {
				t.Fatalf("Parse(%q) succeeded", input)
			}
		})
	}
}

func TestParseContributedSchemeRequiresOptIn(t *testing.T) {
	if _, err := Parse("marketplace:example/tools", Options{Bare: BareLocal}); err == nil {
		t.Fatal("contributed source accepted without AllowContributed")
	}
}

func TestParseBarePolicy(t *testing.T) {
	base := t.TempDir()
	local, err := Parse("team/tool", Options{BaseDir: base, Bare: BareLocal})
	if err != nil || local.Kind != KindLocal {
		t.Fatalf("BareLocal = %#v, %v", local, err)
	}
	npm, err := Parse("team-tool", Options{BaseDir: base, Bare: BareNPM})
	if err != nil || npm.Kind != KindNPM {
		t.Fatalf("BareNPM = %#v, %v", npm, err)
	}
	if _, err := Parse("team-tool", Options{BaseDir: base, Bare: BareReject}); err == nil {
		t.Fatal("BareReject accepted untyped source")
	}
}

func TestWindowsDrivePathIsLocalOnEveryHost(t *testing.T) {
	ref, err := Parse(`C:\work\project`, Options{Bare: BareReject})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if ref.Kind != KindLocal {
		t.Fatalf("kind = %q, want local", ref.Kind)
	}
}

func TestParseExplicitLocalSource(t *testing.T) {
	ref, err := Parse("local:./extensions/review", Options{Bare: BareReject, AllowContributed: true})
	if err != nil {
		t.Fatal(err)
	}
	if ref.Kind != KindLocal || ref.Scheme != "local" || ref.Locator != "./extensions/review" {
		t.Fatalf("ref = %#v", ref)
	}
	if _, err := Parse("local:", Options{Bare: BareReject, AllowContributed: true}); err == nil {
		t.Fatal("empty local source accepted")
	}
}

// A local locator is a filesystem path, which may contain spaces (a profile
// such as C:\Users\Jane Smith), and the canonical identity Identity writes
// must parse back to the same local source.
func TestParseLocalSourceWithSpacesRoundTrips(t *testing.T) {
	base := filepath.Join(t.TempDir(), "dir with spaces")
	ref, err := Parse("local:./review tools", Options{BaseDir: base, Bare: BareReject, AllowContributed: true})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if ref.Kind != KindLocal || ref.Locator != "./review tools" {
		t.Fatalf("ref = %#v", ref)
	}
	identity, err := ref.Identity(base)
	if err != nil {
		t.Fatalf("Identity: %v", err)
	}
	if want := "local:" + filepath.Join(base, "review tools"); identity != want {
		t.Fatalf("identity = %q, want %q", identity, want)
	}
	again, err := Parse(identity, Options{Bare: BareReject, AllowContributed: true})
	if err != nil {
		t.Fatalf("Parse(%q): %v", identity, err)
	}
	if roundTrip, err := again.Identity(base); err != nil || roundTrip != identity {
		t.Fatalf("round-trip identity = %q, %v; want %q", roundTrip, err, identity)
	}
}
