package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

func runGitUpdateTest(t *testing.T, cwd string, args ...string) string {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "git", args...)
	cmd.Dir = cwd
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %q: %v\n%s", args, err, output)
	}
	return strings.TrimSpace(string(output))
}
func commitGitUpdateFile(t *testing.T, cwd, content, message string) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(cwd, "extension.ts"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	runGitUpdateTest(t, cwd, "add", "extension.ts")
	runGitUpdateTest(t, cwd, "commit", "-m", message)
	return runGitUpdateTest(t, cwd, "rev-parse", "HEAD")
}
func newGitUpdateFixture(t *testing.T) (string, string, string, *codingagent.SettingsManager) {
	t.Helper()
	cwd := t.TempDir()
	agentDir := filepath.Join(cwd, "agent")
	remote := filepath.Join(cwd, "remote")
	t.Setenv("PIG_CODING_AGENT_DIR", agentDir)
	t.Setenv("PIG_OFFLINE", "")
	t.Setenv("PI_OFFLINE", "")
	if err := os.MkdirAll(remote, 0o755); err != nil {
		t.Fatal(err)
	}
	runGitUpdateTest(t, remote, "init", "--initial-branch=main")
	runGitUpdateTest(t, remote, "config", "--local", "user.email", "test@test.com")
	runGitUpdateTest(t, remote, "config", "--local", "user.name", "Test")
	commitGitUpdateFile(t, remote, "// v1", "Initial commit")
	installed := filepath.Join(agentDir, "git", "github.com", "test", "extension")
	if err := os.MkdirAll(filepath.Dir(installed), 0o755); err != nil {
		t.Fatal(err)
	}
	runGitUpdateTest(t, cwd, "clone", remote, installed)
	sm := codingagent.NewSettingsManager(cwd, agentDir)
	if err := sm.SetPackages([]codingagent.PackageSource{{Source: "git:github.com/test/extension"}}); err != nil {
		t.Fatal(err)
	}
	return cwd, remote, installed, sm
}
func assertGitUpdateHead(t *testing.T, installed, commit, content string) {
	t.Helper()
	if got := runGitUpdateTest(t, installed, "rev-parse", "HEAD"); got != commit {
		t.Fatalf("HEAD=%s, want %s", got, commit)
	}
	data, err := os.ReadFile(filepath.Join(installed, "extension.ts"))
	if err != nil || string(data) != content {
		t.Fatalf("extension=%q, error=%v, want %q", data, err, content)
	}
}
func TestGitUpdatesUpstream(t *testing.T) {
	// .upstream/v0.87.1/packages/coding-agent/test/git-update.test.ts:125
	t.Run("should skip reset, clean, and install when already up to date", func(t *testing.T) {
		cwd, remote, installed, sm := newGitUpdateFixture(t)
		// Upstream :128 writes this manifest without adding it to the commit. It must not cause npm installation in the clone.
		if err := os.WriteFile(filepath.Join(remote, "package.json"), []byte(`{"name":"test-extension","version":"1.0.0"}`), 0o600); err != nil {
			t.Fatal(err)
		}
		realGit, err := exec.LookPath("git")
		if err != nil {
			t.Fatal(err)
		}
		log := filepath.Join(t.TempDir(), "git.log")
		bin := t.TempDir()
		script := fmt.Sprintf("#!/bin/sh\nprintf '%%s\\n' \"$*\" >> %q\nexec %q \"$@\"\n", filepath.ToSlash(log), filepath.ToSlash(realGit))
		writeStubScript(t, filepath.Join(bin, "git"), script)
		writeStubScript(t, filepath.Join(bin, "npm"), fmt.Sprintf("#!/bin/sh\nprintf 'npm %%s\\n' \"$*\" >> %q\n", filepath.ToSlash(log)))
		t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
		if err := packageManagerFor(cwd, sm, nil).Update(""); err != nil {
			t.Fatal(err)
		}
		commands, err := os.ReadFile(log)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(commands), "fetch --prune --no-tags origin +refs/heads/main:refs/remotes/origin/main") {
			t.Fatalf("missing targeted fetch: %s", commands)
		}
		for _, unexpected := range []string{"reset --hard", "clean -fdx", "fetch --prune origin", "npm install"} {
			if strings.Contains(string(commands), unexpected) {
				t.Errorf("unexpected %q in %s", unexpected, commands)
			}
		}
		data, err := os.ReadFile(filepath.Join(installed, "extension.ts"))
		if err != nil || string(data) != "// v1" {
			t.Fatalf("current extension=%q err=%v", data, err)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/git-update.test.ts:165
	t.Run("should update to latest commit when remote has new commits", func(t *testing.T) {
		cwd, remote, installed, sm := newGitUpdateFixture(t)
		assertGitUpdateHead(t, installed, runGitUpdateTest(t, remote, "rev-parse", "HEAD"), "// v1")
		commit := commitGitUpdateFile(t, remote, "// v2", "Second commit")
		if err := packageManagerFor(cwd, sm, nil).Update(""); err != nil {
			t.Fatal(err)
		}
		assertGitUpdateHead(t, installed, commit, "// v2")
	})
	// .upstream/v0.87.1/packages/coding-agent/test/git-update.test.ts:182
	t.Run("should handle complete history rewrite", func(t *testing.T) {
		cwd, remote, installed, sm := newGitUpdateFixture(t)
		initial := runGitUpdateTest(t, remote, "rev-parse", "HEAD")
		commitGitUpdateFile(t, remote, "// v2", "v2")
		latest := commitGitUpdateFile(t, remote, "// v3", "v3")
		if err := packageManagerFor(cwd, sm, nil).Update(""); err != nil {
			t.Fatal(err)
		}
		assertGitUpdateHead(t, installed, latest, "// v3")
		// commit-tree constructs the same rewritten ancestry without a destructive reset command in the fixture.
		if err := os.WriteFile(filepath.Join(remote, "extension.ts"), []byte("// rewrite-a"), 0o600); err != nil {
			t.Fatal(err)
		}
		runGitUpdateTest(t, remote, "add", "extension.ts")
		tree := runGitUpdateTest(t, remote, "write-tree")
		rewritten := runGitUpdateTest(t, remote, "commit-tree", tree, "-p", initial, "-m", "Rewrite A")
		runGitUpdateTest(t, remote, "update-ref", "refs/heads/main", rewritten)
		final := commitGitUpdateFile(t, remote, "// rewrite-b", "Rewrite B")
		if err := packageManagerFor(cwd, sm, nil).Update(""); err != nil {
			t.Fatal(err)
		}
		assertGitUpdateHead(t, installed, final, "// rewrite-b")
		data, err := os.ReadFile(filepath.Join(installed, "extension.ts"))
		if err != nil {
			t.Fatal(err)
		}
		fmt.Printf("GIT_REWRITE %s\n", data)
	})
	// .upstream/v0.87.1/packages/coding-agent/test/git-update.test.ts:206
	t.Run("should checkout the configured pinned git ref during full and targeted updates", func(t *testing.T) {
		cwd, remote, installed, sm := newGitUpdateFixture(t)
		first := runGitUpdateTest(t, remote, "rev-parse", "HEAD")
		runGitUpdateTest(t, remote, "tag", "v1")
		second := commitGitUpdateFile(t, remote, "// v2", "Second commit")
		runGitUpdateTest(t, remote, "tag", "v2")
		runGitUpdateTest(t, installed, "fetch", "--tags", "origin")
		runGitUpdateTest(t, installed, "checkout", "v1")
		assertGitUpdateHead(t, installed, first, "// v1")
		source := "git:github.com/test/extension@v2"
		if err := sm.SetPackages([]codingagent.PackageSource{{Source: source}}); err != nil {
			t.Fatal(err)
		}
		if err := packageManagerFor(cwd, sm, nil).Update(""); err != nil {
			t.Fatal(err)
		}
		assertGitUpdateHead(t, installed, second, "// v2")
		runGitUpdateTest(t, installed, "checkout", "v1")
		if err := packageManagerFor(cwd, sm, nil).Update(source); err != nil {
			t.Fatal(err)
		}
		assertGitUpdateHead(t, installed, second, "// v2")
	})
}
