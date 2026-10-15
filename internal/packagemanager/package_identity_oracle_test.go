package packagemanager

import (
	"math/rand"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// pi: packages/coding-agent/src/core/package-manager.ts

// getPackageIdentity (the key packages are de-duplicated by) and packageSourcesMatch over npm, git and local sources: user-scope identities resolve a relative
// local path against the agent directory, project-scope against the project config directory, and no scope against the working directory. The corpus spells
// local paths with ~, dot segments, trailing slashes, surrounding JS whitespace, file: URLs (with hosts, escapes and bad escapes) and foreign Windows paths.
// Left out because PiG's source model answers them differently on purpose: the registry query and subdirectory selectors and contributed schemes (D18), a blank
// source or "npm:" without a name (PiG: unsupported), an upper-case "FILE:" scheme, and a Windows drive path on a POSIX host (PiG keeps the spelling).
func TestPackageIdentityMatchesPi(t *testing.T) {
	t.Setenv("HOME", "/home/oracle")
	rng := rand.New(rand.NewSource(239))
	pick := func(items ...string) string { return items[rng.Intn(len(items))] }
	npm := func() string {
		return "npm:" + pick("pkg", "@scope/pkg", "pkg@1.2.3", "@s/p@^1", " pkg ", "pkg@latest", "a/b", "@x", "pkg@a b", "pkg\u00a0", "UPPER@1", "\ufeffpkg", "pkg\ufeff", "\u0085pkg", "pkg\u0085", "\u2028pkg\u3000@1")
	}
	git := func() string {
		return pick("git:github.com/user/repo", "git:github.com/user/repo@v1", "https://github.com/user/repo.git", "https://github.com/user/repo#v1", "git@github.com:user/repo.git",
			"ssh://git@github.com/user/repo", "git:gitlab.com/group/sub/repo", "https://example.com/a/b.git@tag", "git:user/repo", "github.com/user/repo", "user/repo",
			"https://GitHub.com/User/Repo", "git:github.com/user/repo/", "https://github.com/user/repo/tree/main")
	}
	local := func() string {
		return pick(".", "./a", "../b", "a/b", "/abs/path", "/abs//x/", "/abs/./y/../z", "~", "~/x", "~/", "~user/x", " ./a ", "\t./a\n", "\u00a0./a", "./a/", "a/../b",
			"file:///tmp/x", "file:///tmp/x%20y", "file://localhost/tmp/x", "file://host/tmp/x", "file:rel", "file:///tmp/%zz", "file:///tmp/a%2Fb", "file:///",
			`\\srv\share\x`, `.\a\b`, "a\\b", "./é", "./a b")
	}
	var sources []string
	for range 1500 {
		switch rng.Intn(5) {
		case 0:
			sources = append(sources, npm())
		case 1:
			sources = append(sources, git())
		default:
			sources = append(sources, local())
		}
	}
	type pair struct{ A, B string }
	var pairs []pair
	for range 1500 {
		a := sources[rng.Intn(len(sources))]
		b := sources[rng.Intn(len(sources))]
		if rng.Intn(3) == 0 {
			b = a
		}
		pairs = append(pairs, pair{a, b})
	}
	type identity struct {
		User    string `json:"user"`
		Project string `json:"project"`
		None    string `json:"none"`
		Error   string `json:"error"`
	}
	type matches struct {
		User    string `json:"user"`
		Project string `json:"project"`
		Error   string `json:"error"`
	}
	var want struct {
		ProjectDir string     `json:"projectDir"`
		Identities []identity `json:"identities"`
		Matches    []matches  `json:"matches"`
	}
	pioracle.Run(t, `
const { DefaultPackageManager } = await load("pi-coding-agent/core/package-manager.js");
const manager = new DefaultPackageManager({ cwd: "/work/cwd", agentDir: "/agent", settingsManager: { isProjectTrusted: () => true } });
const attempt = (fn) => { try { return { value: String(fn()) }; } catch (error) { return { error: String(error.message) }; } };
const identities = input.sources.map((source) => {
	const user = attempt(() => manager.getPackageIdentity(source, "user"));
	const project = attempt(() => manager.getPackageIdentity(source, "project"));
	const none = attempt(() => manager.getPackageIdentity(source));
	return { user: user.value ?? "", project: project.value ?? "", none: none.value ?? "", error: user.error ?? project.error ?? none.error ?? "" };
});
const matches = input.pairs.map(({ A, B }) => {
	const user = attempt(() => manager.packageSourcesMatch(A, B, "user"));
	const project = attempt(() => manager.packageSourcesMatch(A, B, "project"));
	return { user: user.value ?? "", project: project.value ?? "", error: user.error ?? project.error ?? "" };
});
emit({ projectDir: manager.getBaseDirForScope("project"), identities, matches });`, map[string]any{"sources": sources, "pairs": pairs}, &want, "HOME=/home/oracle", "USERPROFILE=/home/oracle")
	failures, errored := 0, 0
	fail := func(format string, args ...any) {
		if failures++; failures <= 12 {
			t.Errorf(format, args...)
		}
	}
	for i, source := range sources {
		wantID := want.Identities[i]
		var parts [3]string
		var errText string
		for k, dir := range []string{"/agent", want.ProjectDir, "/work/cwd"} {
			got, err := PackageSourceIdentityChecked(dir, source)
			if err != nil {
				errText = err.Error()
				break
			}
			parts[k] = got
		}
		if wantID.Error != "" || errText != "" {
			errored++
			if wantID.Error != errText {
				fail("identity of %q: PiG error %q, Pi error %q", source, errText, wantID.Error)
			}
			continue
		}
		if parts != [3]string{wantID.User, wantID.Project, wantID.None} {
			fail("identity of %q: PiG %q, Pi %q", source, parts, [3]string{wantID.User, wantID.Project, wantID.None})
		}
	}
	for i, pair := range pairs {
		wantMatch := want.Matches[i]
		var got [2]bool
		var errText string
		for k, dir := range []string{"/agent", want.ProjectDir} {
			match, err := PackageSourcesMatch("/work/cwd", dir, pair.A, pair.B)
			if err != nil {
				errText = err.Error()
				break
			}
			got[k] = match
		}
		if wantMatch.Error != "" || errText != "" {
			if wantMatch.Error != errText {
				fail("match %q vs %q: PiG error %q, Pi error %q", pair.A, pair.B, errText, wantMatch.Error)
			}
			continue
		}
		if got != [2]bool{wantMatch.User == "true", wantMatch.Project == "true"} {
			fail("match %q vs %q: PiG %v, Pi %v/%v", pair.A, pair.B, got, wantMatch.User, wantMatch.Project)
		}
	}
	if failures > 0 {
		t.Errorf("%d differences", failures)
	}
	// An oracle whose probes all fail the same way would pass vacuously.
	if errored > len(sources)/4 || errored == 0 {
		t.Fatalf("%d of %d sources errored", errored, len(sources))
	}
}
