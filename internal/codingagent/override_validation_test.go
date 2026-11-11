package codingagent

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The one pattern minimatch 11.0.3 cannot compile is one over 65,536 UTF-16 units (a TypeError); through 10.2.6 "[[:alpha:]]-a" threw a SyntaxError whose text named the pattern, so the order of the overrides is no longer visible in the message.
var filterThrow = strings.Repeat("a", 65537)

// tooLong is the message of that TypeError.
const tooLong = "pattern is too long"

// Pi's addAutoDiscoveredResources checks each auto-discovered resource against its settings array's overrides in this order (package-manager.ts:2413-2575): project extensions, skills, ancestor .agents skills, prompts and themes when the project is trusted, then the user ones with ~/.agents skills after the user skills. A "!" pattern minimatch cannot compile throws when the first resource reaches it, so the first resource met decides which error resolve() rejects with, and a pattern no resource reaches never throws.
func TestAutoDiscoveredOverrideErrorsFollowPiOrder(t *testing.T) {
	const skill = "---\nname: s\ndescription: d\n---\nBody\n"
	for _, tc := range []struct {
		name          string
		trusted       bool
		project, user string
		files         func(root, cwd, agentDir, home string) []string
		want          string
	}{
		{"an ancestor .agents skill", true, `{"skills":["!` + filterThrow + `"]}`, `{}`, func(root, _, _, _ string) []string {
			return []string{filepath.Join(root, ".agents", "skills", "s", "SKILL.md")}
		}, tooLong},
		{"a ~/.agents skill", true, `{}`, `{"skills":["!` + filterThrow + `"]}`, func(_, _, _, home string) []string {
			return []string{filepath.Join(home, ".agents", "skills", "s", "SKILL.md")}
		}, tooLong},
		{"the project before the user", true, `{"prompts":["!` + filterThrow + `"]}`, `{"skills":["!` + filterThrow + `"]}`, func(_, cwd, agentDir, _ string) []string {
			return []string{filepath.Join(agentDir, "skills", "s", "SKILL.md"), filepath.Join(ProjectConfigDir(cwd), "prompts", "p.md")}
		}, tooLong},
		{"an untrusted project", false, `{"prompts":["!` + filterThrow + `"]}`, `{"skills":["!` + filterThrow + `"]}`, func(_, cwd, agentDir, _ string) []string {
			return []string{filepath.Join(agentDir, "skills", "s", "SKILL.md"), filepath.Join(ProjectConfigDir(cwd), "prompts", "p.md")}
		}, tooLong},
		{"skills before prompts", true, `{"prompts":["!` + filterThrow + `"],"skills":["!` + filterThrow + `"]}`, `{}`, func(root, cwd, _, _ string) []string {
			return []string{filepath.Join(ProjectConfigDir(cwd), "prompts", "p.md"), filepath.Join(root, ".agents", "skills", "s", "SKILL.md")}
		}, tooLong},
		{"no resource reaches it", true, `{"prompts":["!` + filterThrow + `"]}`, `{"themes":["!` + filterThrow + `"]}`, func(string, string, string, string) []string { return nil }, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			cwd, agentDir, home := filepath.Join(root, "project"), filepath.Join(root, "agent"), filepath.Join(root, "home")
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home)
			// A .git directory bounds the ancestor .agents walk at root.
			for _, dir := range []string{filepath.Join(root, ".git"), cwd, agentDir, home} {
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			writeOverrideFixture(t, filepath.Join(ProjectConfigDir(cwd), "settings.json"), tc.project)
			writeOverrideFixture(t, filepath.Join(agentDir, "settings.json"), tc.user)
			for _, file := range tc.files(root, cwd, agentDir, home) {
				content := "x\n"
				if filepath.Base(file) == "SKILL.md" {
					content = skill
				}
				writeOverrideFixture(t, file, content)
			}
			sm := NewSettingsManagerWithProjectTrust(cwd, agentDir, tc.trusted)
			err := ValidateAutoDiscoveredOverrides(cwd, agentDir, sm, tc.trusted)
			if tc.want == "" && err != nil || tc.want != "" && (err == nil || err.Error() != tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
}

func writeOverrideFixture(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A directory discovery cannot list may still hold an extension found by name: Pi's collectAutoExtensionEntries checks index.ts with existsSync where readdirSync fails, so the user extensions' override still throws before the prompts' one. Probed with Pi 1.1.0 (r27) with the listing of the user extensions directory denied: it exits 1 with the extensions override's SyntaxError.
func TestAutoDiscoveredOverridesReachAnUnlistableDirectory(t *testing.T) {
	root := t.TempDir()
	cwd, agentDir, home := filepath.Join(root, "project"), filepath.Join(root, "agent"), filepath.Join(root, "home")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	for _, dir := range []string{filepath.Join(root, ".git"), cwd, home} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	extensions := filepath.Join(agentDir, "extensions")
	writeOverrideFixture(t, filepath.Join(extensions, "index.ts"), "export default function() {}\n")
	writeOverrideFixture(t, filepath.Join(agentDir, "prompts", "p.md"), "x\n")
	writeOverrideFixture(t, filepath.Join(agentDir, "settings.json"), `{"extensions":["!`+filterThrow+`"],"prompts":["!`+filterThrow+`"]}`)
	denyListing(t, extensions)
	sm := NewSettingsManagerWithProjectTrust(cwd, agentDir, false)
	err := ValidateAutoDiscoveredOverrides(cwd, agentDir, sm, false)
	if want := tooLong; err == nil || err.Error() != want {
		t.Fatalf("error = %v, want %q", err, want)
	}
}

// denyListing makes dir unlistable while its entries stay reachable by name, and undoes it before the test's directories are removed.
func denyListing(t *testing.T, dir string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		icacls, err := exec.LookPath("icacls")
		if err != nil {
			t.Skip("icacls is not available")
		}
		// *S-1-1-0 is Everyone; RD denies listing the directory and nothing else.
		if out, err := exec.Command(icacls, dir, "/deny", "*S-1-1-0:(RD)").CombinedOutput(); err != nil {
			t.Fatalf("icacls /deny: %v: %s", err, out)
		}
		t.Cleanup(func() {
			if out, err := exec.Command(icacls, dir, "/remove:d", "*S-1-1-0").CombinedOutput(); err != nil {
				t.Errorf("icacls /remove:d: %v: %s", err, out)
			}
		})
	} else {
		if os.Geteuid() == 0 {
			t.Skip("root lists every directory")
		}
		if err := os.Chmod(dir, 0o311); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := os.Chmod(dir, 0o755); err != nil {
				t.Error(err)
			}
		})
	}
	if _, err := os.ReadDir(dir); err == nil {
		t.Skip("the directory can still be listed")
	}
	if _, err := os.Stat(filepath.Join(dir, "index.ts")); err != nil {
		t.Skipf("the directory's entries cannot be reached by name either: %v", err)
	}
}
