package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/source"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/packagemanager"
)

// package-manager.ts:1446-1470 treats every unprefixed, non-Git spelling as a local source, even when it does not exist.
func TestPackageBareSourcesNeverInvokeNpm(t *testing.T) {
	for _, input := range []string{"github.com/user/repo", "demo", "@scope/pkg"} {
		t.Run(input, func(t *testing.T) {
			cwd, dir := t.TempDir(), t.TempDir()
			t.Chdir(cwd)
			t.Setenv("PIG_HOME", t.TempDir())
			t.Setenv("PIG_CODING_AGENT_DIR", dir)
			node, err := exec.LookPath("node")
			if err != nil {
				t.Fatal(err)
			}
			marker := filepath.Join(cwd, "npm-called")
			script := filepath.Join(cwd, "npm.mjs")
			if err := os.WriteFile(script, []byte("import{writeFileSync}from'node:fs';writeFileSync("+fmt.Sprintf("%q", marker)+",'called');"), 0o644); err != nil {
				t.Fatal(err)
			}
			sm := codingagent.NewSettingsManager(cwd, dir)
			if err := sm.SetNpmCommand([]string{node, script}); err != nil {
				t.Fatal(err)
			}
			stdout, stderr, code := captureStdoutStderr(t, func() int { return runPackageCommand([]string{"install", input}) })
			if code != 1 || stdout != "Installing "+input+"...\n" || stderr != "Error: Path does not exist: "+filepath.Join(cwd, input)+"\n" {
				t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
			}
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatalf("npm executed for local source: %v", err)
			}
		})
	}
}

func TestPackageHTTPSParsingOriginal(t *testing.T) {
	for _, tc := range []struct {
		name, source, host, path, ref string
		local                         bool
	}{
		// .upstream/v0.87.1/packages/coding-agent/test/package-manager.test.ts:1387
		{"should parse HTTPS GitHub URLs correctly", "https://github.com/user/repo", "github.com", "user/repo", "", false},
		// .upstream/v0.87.1/packages/coding-agent/test/package-manager.test.ts:1395
		{"should parse HTTPS URLs with git prefix", "git:https://github.com/user/repo", "github.com", "user/repo", "", false},
		// .upstream/v0.87.1/packages/coding-agent/test/package-manager.test.ts:1402
		{"should parse HTTPS URLs with ref", "https://github.com/user/repo@v1.2.3", "github.com", "user/repo", "v1.2.3", false},
		// .upstream/v0.87.1/packages/coding-agent/test/package-manager.test.ts:1411
		{"should parse host/path shorthand only with git prefix", "git:github.com/user/repo", "github.com", "user/repo", "", false},
		// .upstream/v0.87.1/packages/coding-agent/test/package-manager.test.ts:1418
		{"should treat host/path shorthand as local without git prefix", "github.com/user/repo", "", "", "", true},
		// .upstream/v0.87.1/packages/coding-agent/test/package-manager.test.ts:1423
		{"should parse HTTPS URLs with git suffix", "https://github.com/user/repo.git", "github.com", "user/repo", "", false},
		// .upstream/v0.87.1/packages/coding-agent/test/package-manager.test.ts:1430
		{"should parse GitLab HTTPS URLs", "https://gitlab.com/user/repo", "gitlab.com", "user/repo", "", false},
		// .upstream/v0.87.1/packages/coding-agent/test/package-manager.test.ts:1437
		{"should parse Bitbucket HTTPS URLs", "https://bitbucket.org/user/repo", "bitbucket.org", "user/repo", "", false},
		// .upstream/v0.87.1/packages/coding-agent/test/package-manager.test.ts:1444
		{"should parse Codeberg HTTPS URLs", "https://codeberg.org/user/repo", "codeberg.org", "user/repo", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.local {
				if got := packagemanager.DetectSourceKind(tc.source); got != "local" {
					t.Fatalf("kind=%q", got)
				}
				return
			}
			parsed, ok := parseGitPackageSource(tc.source)
			if !ok || parsed.host != tc.host || parsed.path != tc.path || parsed.ref != tc.ref || parsed.pinned != (tc.ref != "") {
				t.Fatalf("parsed=%+v, ok=%t", parsed, ok)
			}
		})
	}
	// .upstream/v0.87.1/packages/coding-agent/test/package-manager.test.ts:1451
	t.Run("should generate correct package identity for protocol and git-prefixed URLs", func(t *testing.T) {
		for _, source := range []string{"https://github.com/user/repo", "https://github.com/user/repo@v1.0.0", "git:github.com/user/repo", "https://github.com/user/repo.git"} {
			if got := packagemanager.PackageSourceIdentity(t.TempDir(), source); got != "git:github.com/user/repo" {
				t.Errorf("identity(%q)=%q", source, got)
			}
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/package-manager.test.ts:1464
	t.Run("should deduplicate git URLs with different supported formats", func(t *testing.T) {
		root, agent := t.TempDir(), t.TempDir()
		pkg := filepath.Join(root, "https-dedup-pkg", "extensions")
		if err := os.MkdirAll(pkg, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(pkg, "test.ts"), []byte("export default function() {}"), 0o644); err != nil {
			t.Fatal(err)
		}
		sm := codingagent.NewSettingsManager(root, agent)
		sources := []string{"https://github.com/user/repo", "git:github.com/user/repo", "https://github.com/user/repo.git"}
		var packages []codingagent.PackageSource
		for _, source := range sources {
			packages = append(packages, codingagent.PackageSource{Source: source})
		}
		if err := sm.SetPackages(packages); err != nil {
			t.Fatal(err)
		}
		first := packagemanager.PackageSourceIdentity(root, sources[0])
		for _, source := range sources[1:] {
			if got := packagemanager.PackageSourceIdentity(root, source); got != first {
				t.Fatalf("identity(%q)=%q, want %q", source, got, first)
			}
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/package-manager.test.ts:1487
	t.Run("should handle HTTPS URLs with refs in resolve", func(t *testing.T) {
		for _, ref := range []string{"main", "feature/branch"} {
			parsed, ok := parseGitPackageSource("https://github.com/user/repo@" + ref)
			if !ok || parsed.ref != ref || !parsed.pinned {
				t.Fatalf("parsed=%+v, ok=%t", parsed, ok)
			}
		}
	})
}

func TestPackageSourceDocsExamplesOriginal(t *testing.T) {
	// .upstream/v0.87.1/packages/coding-agent/test/package-manager.test.ts:1231
	t.Run("should parse package source types from docs examples", func(t *testing.T) {
		for _, tc := range []struct {
			source string
			pinned bool
		}{{"npm:@scope/pkg@1.2.3", true}, {"npm:@scope/pkg@^1.2.3", false}, {"npm:pkg", false}} {
			if got := packagemanager.DetectSourceKind(tc.source); got != "npm" {
				t.Fatalf("kind(%q)=%q", tc.source, got)
			}
			if got := isPinnedNpm(tc.source); got != tc.pinned {
				t.Fatalf("pinned(%q)=%t", tc.source, got)
			}
		}
		for _, source := range []string{"git:github.com/user/repo@v1", "https://github.com/user/repo@v1", "git:git@github.com:user/repo@v1", "ssh://git@github.com/user/repo@v1"} {
			if got := packagemanager.DetectSourceKind(source); got != "git" {
				t.Fatalf("kind(%q)=%q", source, got)
			}
		}
		for _, source := range []string{"/absolute/path/to/package", "./relative/path/to/package", "../relative/path/to/package"} {
			if got := packagemanager.DetectSourceKind(source); got != "local" {
				t.Fatalf("kind(%q)=%q", source, got)
			}
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/package-manager.test.ts:1254
	t.Run("should never parse dot-relative paths as git", func(t *testing.T) {
		for _, input := range []string{"./packages/agent-timers", "../packages/agent-timers"} {
			if got := packagemanager.DetectSourceKind(input); got != "local" {
				t.Fatalf("kind(%q)=%q", input, got)
			}
			// The CLI keeps local source spelling until resolving it against the selected settings scope.
			parsed, err := source.Parse(input, source.Options{Bare: source.BareLocal, AllowContributed: true})
			if err != nil {
				t.Fatal(err)
			}
			if parsed.Locator != input {
				t.Fatalf("path=%q, want %q", parsed.Locator, input)
			}
		}
	})
}
