package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/tui"
)

func TestPackageAgentsSkillDiscoveryOriginal(t *testing.T) {
	// .upstream/v0.87.1/packages/coding-agent/test/package-manager.test.ts:389
	t.Run("should scan .agents/skills from cwd up to git repo root", func(t *testing.T) {
		f := newPackageResourceFixture(t)
		t.Setenv("HOME", t.TempDir())
		original := f.cwd
		repo := filepath.Join(original, "repo")
		f.cwd = filepath.Join(repo, "packages", "feature")
		if err := os.MkdirAll(f.cwd, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
			t.Fatal(err)
		}
		above := filepath.Join(original, ".agents", "skills", "above-repo", "SKILL.md")
		root := filepath.Join(repo, ".agents", "skills", "repo-root", "SKILL.md")
		nested := filepath.Join(repo, "packages", ".agents", "skills", "nested", "SKILL.md")
		writePackageResource(t, above, "---\nname: above-repo\ndescription: above\n---\n")
		writePackageResource(t, root, "---\nname: repo-root\ndescription: repo\n---\n")
		writePackageResource(t, nested, "---\nname: nested\ndescription: nested\n---\n")
		items := f.items(t)
		requirePackageResource(t, items, root, tui.ResourceSkills, true)
		requirePackageResource(t, items, nested, tui.ResourceSkills, true)
		for _, item := range items {
			if item.Path == above {
				t.Fatalf("escaped git root: %+v", item)
			}
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/package-manager.test.ts:419
	t.Run("should scan .agents/skills up to filesystem root when not in a git repo", func(t *testing.T) {
		f := newPackageResourceFixture(t)
		t.Setenv("HOME", t.TempDir())
		root := filepath.Join(f.cwd, "non-repo")
		f.cwd = filepath.Join(root, "a", "b")
		if err := os.MkdirAll(f.cwd, 0o755); err != nil {
			t.Fatal(err)
		}
		first := filepath.Join(root, ".agents", "skills", "root", "SKILL.md")
		second := filepath.Join(root, "a", ".agents", "skills", "middle", "SKILL.md")
		writePackageResource(t, first, "---\nname: root\ndescription: root\n---\n")
		writePackageResource(t, second, "---\nname: middle\ndescription: middle\n---\n")
		items := f.items(t)
		requirePackageResource(t, items, first, tui.ResourceSkills, true)
		requirePackageResource(t, items, second, tui.ResourceSkills, true)
	})
	// .upstream/v0.87.1/packages/coding-agent/test/package-manager.test.ts:443
	t.Run("should ignore root markdown files in .agents/skills but discover nested markdown skills", func(t *testing.T) {
		f := newPackageResourceFixture(t)
		t.Setenv("HOME", t.TempDir())
		skills := filepath.Join(f.cwd, ".agents", "skills")
		f.cwd = filepath.Join(f.cwd, "work")
		if err := os.MkdirAll(f.cwd, 0o755); err != nil {
			t.Fatal(err)
		}
		paths := []string{filepath.Join(skills, "root-file.md"), filepath.Join(skills, "nested-skill", "SKILL.md"), filepath.Join(skills, "third-party", "child-skill.md"), filepath.Join(skills, "third-party", "vendor", "pack", "deep-skill.md")}
		texts := []string{"---\nname: root-file\ndescription: Root markdown file\n---\n", "---\nname: nested-skill\ndescription: Nested skill\n---\n", "---\nname: child-skill\ndescription: Nested markdown skill\n---\n", "---\nname: deep-skill\ndescription: Deep markdown skill\n---\n"}
		for i, path := range paths {
			writePackageResource(t, path, texts[i])
		}
		items := f.items(t)
		for _, item := range items {
			if item.Path == paths[0] {
				t.Fatalf("root markdown discovered: %+v", item)
			}
		}
		for _, path := range paths[1:] {
			requirePackageResource(t, items, path, tui.ResourceSkills, true)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/package-manager.test.ts:471
	t.Run("should keep ~/.agents/skills user-scoped when cwd is under home in a non-git directory", func(t *testing.T) {
		f := newPackageResourceFixture(t)
		home := f.cwd
		f.cwd = filepath.Join(home, "scratch", "nested")
		f.agent = filepath.Join(home, codingagent.CONFIG_DIR_NAME, "agent")
		if err := os.MkdirAll(f.cwd, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(f.agent, 0o755); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(home, ".agents", "skills", "home-skill", "SKILL.md")
		writePackageResource(t, path, "---\nname: home-skill\ndescription: home\n---\n")
		count := 0
		for _, item := range f.items(t) {
			if item.Path == path {
				count++
				if !item.Enabled || item.Scope != "user" || item.Source != "auto" {
					t.Fatalf("metadata=%+v", item)
				}
			}
		}
		if count != 1 {
			t.Fatalf("home skill count=%d, want1", count)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/package-manager.test.ts:507
	t.Run("should dedupe user skill entries when ~/.pi/agent/skills is a symlink to ~/.agents/skills", func(t *testing.T) {
		f := newPackageResourceFixture(t)
		skills := filepath.Join(f.cwd, ".agents", "skills")
		if err := os.MkdirAll(skills, 0o755); err != nil {
			t.Fatal(err)
		}
		requirePackageDirectoryLink(t, skills, filepath.Join(f.agent, "skills"))
		writePackageResource(t, filepath.Join(skills, "foo", "SKILL.md"), "---\nname: foo\ndescription: foo\n---\n")
		count := 0
		for _, item := range f.items(t) {
			if strings.HasSuffix(filepath.ToSlash(item.Path), "foo/SKILL.md") {
				count++
			}
		}
		if count != 1 {
			t.Fatalf("foo skill count=%d, want1", count)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/package-manager.test.ts:538
	t.Run("should respect .gitignore in skill directories", func(t *testing.T) {
		f := newPackageResourceFixture(t)
		skills := filepath.Join(f.agent, "skills")
		writePackageResource(t, filepath.Join(skills, ".gitignore"), "venv\n__pycache__\n")
		good := filepath.Join(skills, "good-skill", "SKILL.md")
		writePackageResource(t, good, "---\nname: good-skill\ndescription: Good\n---\nContent")
		writePackageResource(t, filepath.Join(skills, "venv", "bad-skill", "SKILL.md"), "---\nname: bad-skill\ndescription: Bad\n---\nContent")
		if err := f.settings.SetSkillPaths([]string{"skills"}); err != nil {
			t.Fatal(err)
		}
		items := f.items(t)
		requirePackageResource(t, items, good, tui.ResourceSkills, true)
		for _, item := range items {
			if item.Enabled && strings.Contains(item.Path, "venv") {
				t.Fatalf("ignored resource=%+v", item)
			}
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/package-manager.test.ts:558
	t.Run("should not apply parent .gitignore to .pi auto-discovery", func(t *testing.T) {
		f := newPackageResourceFixture(t)
		writePackageResource(t, filepath.Join(f.cwd, ".gitignore"), codingagent.CONFIG_DIR_NAME+"\n")
		path := filepath.Join(f.cwd, codingagent.CONFIG_DIR_NAME, "skills", "auto-skill", "SKILL.md")
		writePackageResource(t, path, "---\nname: auto-skill\ndescription: Auto\n---\nContent")
		requirePackageResource(t, f.items(t), path, tui.ResourceSkills, true)
	})
}

// Pi's getHomeDir (package-manager.ts:221-223) falls back to the OS user database when the platform home variable is absent. Unix also preserves an explicitly empty HOME. Go's os.UserHomeDir returns an error for both states.
func TestPackageManagerHomeDirFallsBackToSystemHome(t *testing.T) {
	// Resolve the binary before changing HOME so a version-manager shim cannot use the altered home to provision tools.
	binary, err := exec.CommandContext(t.Context(), "node", "-p", "process.execPath").Output()
	if err != nil {
		t.Fatal(err)
	}
	states := []bool{true}
	if runtime.GOOS != "windows" {
		// Node rejects an empty USERPROFILE on Windows; it is not a successful home-directory lookup.
		states = append(states, false)
	}
	for _, unset := range states {
		name := "empty"
		if unset {
			name = "absent"
		}
		t.Run(name, func(t *testing.T) {
			keys := []string{"HOME"}
			if runtime.GOOS == "windows" {
				keys = append(keys, "USERPROFILE")
			}
			for _, key := range keys {
				t.Setenv(key, "")
				if unset {
					if err := os.Unsetenv(key); err != nil {
						t.Fatal(err)
					}
				}
			}
			out, err := exec.CommandContext(t.Context(), strings.TrimSpace(string(binary)), "-p", `process.env.HOME || require('node:os').homedir()`).Output()
			if err != nil {
				t.Fatal(err)
			}
			want := strings.TrimRight(string(out), "\r\n")
			if got := packageManagerHomeDir(); got != want {
				t.Fatalf("system-home fallback=%q; Node getHomeDir()=%q", got, want)
			}
		})
	}
}

// Pi's getHomeDir (package-manager.ts:221-223) prefers HOME on every platform, so on Windows the runtime skill inputs read ~/.agents/skills under HOME rather than USERPROFILE, as resolve() does.
func TestSkillInputsReadUserAgentsSkillsUnderHOME(t *testing.T) {
	f := newPackageResourceFixture(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := filepath.Join(home, ".agents", "skills", "home-skill", "SKILL.md")
	writePackageResource(t, path, "---\nname: home-skill\ndescription: home\n---\n")
	inputs := collectSkillInputs(f.cwd, f.agent, f.settings, Args{}, nil)
	count := 0
	for _, input := range inputs {
		if input == filepath.Dir(path) {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("home skill inputs=%d in %q, want 1", count, inputs)
	}
	requirePackageResource(t, f.items(t), path, tui.ResourceSkills, true)
}
